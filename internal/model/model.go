// Package model holds the slot representation of a command and renders it
// back to shell text.
package model

import (
	"strings"

	"github.com/amimof/rx/internal/parse"
)

// Kind is the kind of a slot.
type Kind int

const (
	// Positional is a non-flag argument.
	Positional Kind = iota
	// Flag is a flag that takes a value.
	Flag
	// Bool is a flag without a value.
	Bool
)

func (k Kind) String() string {
	switch k {
	case Flag:
		return "flag"
	case Bool:
		return "bool"
	}
	return "positional"
}

// Style is how a flag and its value are joined.
type Style int

const (
	// Separate renders "-n value".
	Separate Style = iota
	// Equals renders "--namespace=value".
	Equals
	// Attached renders "-nvalue".
	Attached
)

// Slot is one editable part of a command.
type Slot struct {
	Kind Kind
	// Key identifies the slot: the canonical long flag name without dashes,
	// the positional name from the spec, or "pos:N".
	Key string
	// Name is the flag as written, e.g. "-n" or "--namespace". Empty for
	// positionals.
	Name string
	// Label is a human readable name shown in the editor.
	Label string
	Value string
	Style Style
	// Index is the positional index, counted after the command word.
	Index int
	// Raw is the original source text of the slot. It is used verbatim when
	// rendering an unmodified slot, preserving quoting.
	Raw   string
	Dirty bool
}

// Command is a parsed simple command broken into slots.
type Command struct {
	// Head is the command word as typed (possibly an alias).
	Head parse.Word
	// Name is the resolved command name, e.g. "kubectl" for "k".
	Name  string
	Slots []Slot
	// Source is the original text of the command and Changed reports
	// whether anything was modified since parsing.
	Source  string
	Changed bool
}

// Find returns the index of the first slot with the given key, or -1.
func (c *Command) Find(key string) int {
	for i := range c.Slots {
		if c.Slots[i].Key == key {
			return i
		}
	}
	return -1
}

// SetValue changes the value of slot i.
func (c *Command) SetValue(i int, v string) {
	if c.Slots[i].Value == v {
		return
	}
	c.Slots[i].Value = v
	c.Slots[i].Dirty = true
	c.Changed = true
}

// Remove deletes slot i.
func (c *Command) Remove(i int) {
	c.Slots = append(c.Slots[:i], c.Slots[i+1:]...)
	c.Changed = true
}

// RemoveKey deletes every slot with the given key.
func (c *Command) RemoveKey(key string) {
	for i := len(c.Slots) - 1; i >= 0; i-- {
		if c.Slots[i].Key == key {
			c.Remove(i)
		}
	}
}

// Add appends a flag slot and returns its index.
func (c *Command) Add(s Slot) int {
	s.Dirty = true
	c.Changed = true
	if s.Kind != Positional {
		c.Slots = append(c.Slots, s)
		return len(c.Slots) - 1
	}
	// Keep positionals in order: insert after the last positional with a
	// lower index, or before everything if there is none.
	at := 0
	for i, x := range c.Slots {
		if x.Kind == Positional && x.Index < s.Index {
			at = i + 1
		}
	}
	c.Slots = append(c.Slots, Slot{})
	copy(c.Slots[at+1:], c.Slots[at:])
	c.Slots[at] = s
	return at
}

// Render returns the command as shell text. An unmodified command renders
// as its original source byte for byte.
func (c *Command) Render() string {
	if !c.Changed {
		return c.Source
	}
	parts := []string{c.Head.Raw}
	for _, s := range c.Slots {
		if r := s.Render(); r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, " ")
}

// Render returns the slot as shell text. Slots with an empty value render as
// nothing, so a freshly added flag doesn't produce a dangling argument.
func (s Slot) Render() string {
	if !s.Dirty && s.Raw != "" {
		return s.Raw
	}
	switch s.Kind {
	case Bool:
		return s.Name
	case Flag:
		if s.Value == "" {
			return ""
		}
		switch s.Style {
		case Equals:
			return s.Name + "=" + parse.Quote(s.Value)
		case Attached:
			return s.Name + parse.Quote(s.Value)
		}
		return s.Name + " " + parse.Quote(s.Value)
	}
	if s.Value == "" {
		return ""
	}
	return parse.Quote(s.Value)
}
