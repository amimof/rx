package spec

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
)

// Inferrer guesses whether an unknown flag takes a value, typically from
// history. ok is false when there is no evidence either way.
type Inferrer interface {
	TakesValue(cmd, flag string) (takes, ok bool)
}

// Aliases maps alias names to their expansion.
type Aliases map[string]string

// Resolve returns the command name a word ultimately refers to.
func (a Aliases) Resolve(word string) string {
	seen := map[string]bool{}
	for !seen[word] {
		seen[word] = true
		exp, ok := a[word]
		if !ok {
			break
		}
		f := strings.Fields(exp)
		if len(f) == 0 {
			break
		}
		// Skip leading env assignments and common wrappers in the alias.
		i := 0
		for i < len(f)-1 && (strings.Contains(f[i], "=") || f[i] == "command" || f[i] == "noglob") {
			i++
		}
		word = f[i]
	}
	return filepath.Base(word)
}

// Classifier turns parsed segments into slot commands.
type Classifier struct {
	Specs   *Registry
	Infer   Inferrer
	Aliases Aliases
}

// Classify builds a Command from segment seg of line.
func (c *Classifier) Classify(line *parse.Line, seg int) *model.Command {
	s := line.Segments[seg]
	cmd := &model.Command{
		Head:   s.Words[0],
		Name:   c.Aliases.Resolve(s.Words[0].Value),
		Source: line.Text[s.Start:s.End],
	}
	sp := c.Specs.Lookup(cmd.Name)
	words := s.Words[1:]
	pos := 0
	addPos := func(w parse.Word) {
		key, label := fmt.Sprintf("pos:%d", pos), fmt.Sprintf("arg %d", pos)
		if p := sp.Positional(pos); p != nil && p.Name != "" {
			key, label = p.Name, p.Name
		}
		cmd.Slots = append(cmd.Slots, model.Slot{
			Kind: model.Positional, Key: key, Label: label, Value: w.Value, Index: pos, Raw: w.Raw,
		})
		pos++
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.Dynamic || !strings.HasPrefix(w.Value, "-") || w.Value == "-" || isNumber(w.Value) {
			addPos(w)
			continue
		}
		if w.Value == "--" {
			cmd.Slots = append(cmd.Slots, model.Slot{Kind: model.Bool, Key: "--", Name: "--", Label: "--", Raw: w.Raw})
			for _, rest := range words[i+1:] {
				addPos(rest)
			}
			break
		}
		name, val, hasEq := strings.Cut(w.Value, "=")
		long := strings.HasPrefix(name, "--")
		fs := sp.Flag(name)
		key := strings.TrimLeft(name, "-")
		label := name
		if fs != nil {
			key, label = fs.Name, "--"+fs.Name
		}
		slot := model.Slot{Kind: model.Flag, Key: key, Name: name, Label: label, Raw: w.Raw}
		switch {
		case hasEq:
			slot.Value, slot.Style = val, model.Equals
		case !long && len(name) > 2 && fs == nil:
			// "-nfoo": attached value if the single-letter flag takes one.
			if f := sp.Flag(name[:2]); f != nil && c.takes(cmd.Name, f, name[:2]) {
				slot.Key, slot.Name, slot.Label = f.Name, name[:2], "--"+f.Name
				slot.Value, slot.Style = name[2:], model.Attached
			} else {
				slot.Kind = model.Bool
			}
		case c.takes(cmd.Name, fs, name) && i+1 < len(words):
			i++
			slot.Value = words[i].Value
			slot.Raw = line.Text[w.Start:words[i].End]
		default:
			slot.Kind = model.Bool
		}
		cmd.Slots = append(cmd.Slots, slot)
	}
	return cmd
}

func (c *Classifier) takes(cmd string, fs *FlagSpec, name string) bool {
	if fs != nil {
		switch fs.Takes {
		case "value":
			return true
		case "none":
			return false
		}
	}
	if c.Infer != nil {
		if t, ok := c.Infer.TakesValue(cmd, name); ok {
			return t
		}
	}
	return fs != nil && (len(fs.Values) > 0 || fs.Provider != "")
}

func isNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
