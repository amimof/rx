// Package complete defines completion backends and the synthetic command
// lines used to ask them for the values of a single slot.
package complete

import (
	"context"
	"strings"

	"github.com/amimof/rx/internal/model"
)

// Candidate is a completion value.
type Candidate struct {
	Value  string `json:"v"`
	Desc   string `json:"d,omitempty"`
	Group  string `json:"g,omitempty"`
	Source string `json:"-"`
}

// Directive is cobra's ShellCompDirective bitmask.
type Directive int

const (
	DirectiveError Directive = 1 << iota
	DirectiveNoSpace
	DirectiveNoFileComp
	DirectiveFilterFileExt
	DirectiveFilterDirs
	DirectiveKeepOrder
)

// Result is the output of one completion request.
type Result struct {
	Candidates []Candidate `json:"c"`
	Directive  Directive   `json:"dir"`
	// Message is a hint from the completer, e.g. zsh's _message.
	Message string `json:"m,omitempty"`
}

// Request describes a completion of the last word of a command.
type Request struct {
	// Argv is the command with aliases resolved and unquoted words. The last
	// element is the (possibly empty) word being completed.
	Argv []string
	// Line is the same command as shell text for the zsh backend, keeping
	// the command word as typed so the shell resolves aliases itself. The
	// cursor is at the end of Line.
	Line string
}

// Key identifies a request for caching.
func (r Request) Key() string { return strings.Join(r.Argv, "\x00") }

// Backend completes requests.
type Backend interface {
	Name() string
	Complete(ctx context.Context, req Request) (Result, error)
}

// Synthesize builds the request for completing slot focus of cmd. The
// focused slot's value is removed and moved to the end, so other slots
// still provide context (e.g. -n for pod names). Positionals after the
// focused positional are dropped since they would shift its index.
func Synthesize(cmd *model.Command, focus int) Request {
	f := cmd.Slots[focus]
	argv := []string{cmd.Name}
	line := []string{cmd.Head.Raw}
	add := func(s model.Slot) {
		if r := s.Render(); r != "" {
			argv = append(argv, slotArgs(s)...)
			line = append(line, r)
		}
	}
	var after []model.Slot
	for i, s := range cmd.Slots {
		switch {
		case i == focus || s.Key == "--":
		case s.Kind == model.Positional && f.Kind == model.Positional && s.Index > f.Index:
		case s.Kind == model.Positional:
			add(s)
		default:
			after = append(after, s)
		}
	}
	// Flags go after positionals so a focused positional's index is right.
	for _, s := range after {
		add(s)
	}
	if f.Kind != model.Positional {
		argv = append(argv, f.Name)
		line = append(line, f.Name)
	}
	argv = append(argv, "")
	return Request{Argv: argv, Line: strings.Join(line, " ") + " "}
}

// slotArgs returns the unquoted argv words of a slot.
func slotArgs(s model.Slot) []string {
	switch s.Kind {
	case model.Positional:
		return []string{s.Value}
	case model.Bool:
		return []string{s.Name}
	}
	switch s.Style {
	case model.Equals:
		return []string{s.Name + "=" + s.Value}
	case model.Attached:
		return []string{s.Name + s.Value}
	}
	return []string{s.Name, s.Value}
}

// SynthesizeFlags builds a request that completes flag names for cmd, i.e.
// the command with a trailing "-".
func SynthesizeFlags(cmd *model.Command) Request {
	argv := []string{cmd.Name}
	line := []string{cmd.Head.Raw}
	for _, s := range cmd.Slots {
		if s.Key == "--" {
			break
		}
		if r := s.Render(); r != "" {
			line = append(line, r)
			argv = append(argv, slotArgs(s)...)
		}
	}
	argv = append(argv, "-")
	return Request{Argv: argv, Line: strings.Join(line, " ") + " -"}
}

// Escape encodes s for the line protocol between rx and the zsh widget.
func Escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\t", `\t`).Replace(s)
}

// Unescape reverses Escape.
func Unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			default:
				sb.WriteByte(s[i])
			}
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}
