// Package transform implements slot edits: set, unset, toggle and cycle,
// honouring mutually exclusive flags from the spec.
package transform

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/spec"
)

// ErrNoValues is returned by Cycle when neither the caller nor the spec
// provides values. The editor then fills them from history and completion.
var ErrNoValues = errors.New("no values to cycle")

// Apply runs t against cmd.
func Apply(cmd *model.Command, sp *spec.Spec, t spec.Transform) error {
	target := t.Target()
	switch t.Op {
	case "toggle":
		return Toggle(cmd, sp, target)
	case "cycle":
		return Cycle(cmd, sp, target, t.Values)
	case "set":
		return Set(cmd, sp, target, t.Value)
	case "unset":
		cmd.RemoveKey(target)
		return nil
	case "focus":
		// Moves the editor to the slot; the command is unchanged.
		return nil
	}
	return fmt.Errorf("unknown op %q", t.Op)
}

// Set assigns value to the slot with key, adding the slot if absent, and
// removes slots that are mutually exclusive with it.
func Set(cmd *model.Command, sp *spec.Spec, key, value string) error {
	if i := cmd.Find(key); i >= 0 {
		cmd.SetValue(i, value)
	} else {
		s, err := NewSlot(cmd, sp, key)
		if err != nil {
			return err
		}
		s.Value = value
		cmd.Add(s)
	}
	exclude(cmd, sp, key)
	return nil
}

// SetAt assigns value to slot i (which matters when a flag is repeated) and
// removes slots that are mutually exclusive with it. It returns the slot's
// index afterwards, since removals may shift it.
func SetAt(cmd *model.Command, sp *spec.Spec, i int, value string) int {
	cmd.SetValue(i, value)
	return reexclude(cmd, sp, i)
}

// AddFlag adds the flag written as name (e.g. "-n" or "--namespace") and
// returns its index. takes reports whether the flag takes a value.
func AddFlag(cmd *model.Command, sp *spec.Spec, name string, takes bool) int {
	s := model.Slot{Kind: model.Bool, Key: strings.TrimLeft(name, "-"), Name: name, Label: name}
	if f := sp.Flag(name); f != nil {
		s.Key, s.Label = f.Name, "--"+f.Name
	}
	if takes {
		s.Kind = model.Flag
	}
	return reexclude(cmd, sp, cmd.Add(s))
}

func reexclude(cmd *model.Command, sp *spec.Spec, i int) int {
	s := cmd.Slots[i]
	exclude(cmd, sp, s.Key)
	for j := range cmd.Slots {
		if cmd.Slots[j] == s {
			return j
		}
	}
	return -1
}

// Toggle adds a boolean flag if absent and removes it if present.
func Toggle(cmd *model.Command, sp *spec.Spec, key string) error {
	if cmd.Find(key) >= 0 {
		cmd.RemoveKey(key)
		return nil
	}
	s, err := NewSlot(cmd, sp, key)
	if err != nil {
		return err
	}
	s.Kind = model.Bool
	cmd.Add(s)
	exclude(cmd, sp, key)
	return nil
}

// Cycle advances the slot to the next value in values (or the spec's values
// for the slot). Flags cycle through "absent" after the last value;
// positionals wrap around.
func Cycle(cmd *model.Command, sp *spec.Spec, key string, values []string) error {
	isFlag := true
	if len(values) == 0 {
		if f := sp.Flag("--" + key); f != nil {
			values = f.Values
		} else if p := sp.PositionalByName(key); p != nil {
			values, isFlag = p.Values, false
		}
	} else if sp.PositionalByName(key) != nil {
		isFlag = false
	}
	if len(values) == 0 {
		return fmt.Errorf("%w for %q", ErrNoValues, key)
	}
	i := cmd.Find(key)
	if i < 0 {
		return Set(cmd, sp, key, values[0])
	}
	next := slices.Index(values, cmd.Slots[i].Value) + 1
	if next >= len(values) {
		if isFlag {
			cmd.Remove(i)
			return nil
		}
		next = 0
	}
	cmd.SetValue(i, values[next])
	exclude(cmd, sp, key)
	return nil
}

// NewSlot creates an empty slot for key using the spec to decide its shape.
// Long flag names are used for flags the user hasn't typed yet.
func NewSlot(cmd *model.Command, sp *spec.Spec, key string) (model.Slot, error) {
	if p := sp.PositionalByName(key); p != nil {
		return model.Slot{Kind: model.Positional, Key: key, Label: key, Index: p.Index}, nil
	}
	if f := sp.Flag("--" + key); f != nil {
		s := model.Slot{Kind: model.Flag, Key: f.Name, Name: "--" + f.Name, Label: "--" + f.Name}
		if f.Short != "" {
			// Prefer the short form if that's what the user writes elsewhere
			// in this command; otherwise use it anyway as it's shorter.
			s.Name = "-" + f.Short
		}
		if f.Takes == "none" {
			s.Kind = model.Bool
		}
		return s, nil
	}
	var idx int
	if _, err := fmt.Sscanf(key, "pos:%d", &idx); err == nil {
		return model.Slot{Kind: model.Positional, Key: key, Label: fmt.Sprintf("arg %d", idx), Index: idx}, nil
	}
	name := "--" + key
	if len(key) == 1 {
		name = "-" + key
	}
	return model.Slot{Kind: model.Flag, Key: key, Name: name, Label: name}, nil
}

// exclude removes slots that are mutually exclusive with key, in either
// direction of the mutex declaration.
func exclude(cmd *model.Command, sp *spec.Spec, key string) {
	if sp == nil {
		return
	}
	if f := sp.Flag("--" + key); f != nil {
		for _, m := range f.Mutex {
			cmd.RemoveKey(m)
		}
	}
	for _, f := range sp.Flags {
		if slices.Contains(f.Mutex, key) {
			cmd.RemoveKey(f.Name)
		}
	}
}
