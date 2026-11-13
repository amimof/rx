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
	// ID identifies the slot within its command while slots move, e.g. to
	// revert a rule. 0 until assigned by AssignIDs or Add.
	ID int
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

	lastID int
}

// AssignIDs gives every slot without an ID a new one.
func (c *Command) AssignIDs() {
	for i := range c.Slots {
		if c.Slots[i].ID == 0 {
			c.lastID++
			c.Slots[i].ID = c.lastID
		}
	}
}

// FindID returns the index of the slot with the given ID, or -1.
func (c *Command) FindID(id int) int {
	for i := range c.Slots {
		if c.Slots[i].ID == id {
			return i
		}
	}
	return -1
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

// Clone returns a copy of c that can be changed without affecting c.
func (c *Command) Clone() *Command {
	out := *c
	out.Slots = append([]Slot(nil), c.Slots...)
	return &out
}

// Add adds a slot and returns its index. Flags are appended, but before a
// "--" separator, after which everything is an operand.
func (c *Command) Add(s Slot) int {
	s.Dirty = true
	c.Changed = true
	at := len(c.Slots)
	if s.Kind != Positional {
		if i := c.Find("--"); i >= 0 {
			at = i
		}
	} else {
		// Keep positionals in order: insert after the last positional with
		// a lower index, or before everything if there is none.
		at = 0
		for i, x := range c.Slots {
			if x.Kind == Positional && x.Index < s.Index {
				at = i + 1
			}
		}
	}
	c.Slots = append(c.Slots, Slot{})
	copy(c.Slots[at+1:], c.Slots[at:])
	c.Slots[at] = s
	c.AssignIDs()
	return at
}

// Insert puts s at index at as it is, without marking it changed, and
// returns at. A positional takes the position of the first positional at or
// after at, and those move up one position; without one it keeps its
// Index. Extract and Insert undo each other.
func (c *Command) Insert(at int, s Slot) int {
	if s.Kind == Positional {
		shifted := false
		for i := at; i < len(c.Slots); i++ {
			if c.Slots[i].Kind != Positional {
				continue
			}
			if !shifted {
				s.Index, shifted = c.Slots[i].Index, true
			}
			c.Slots[i].Index++
		}
	}
	c.Changed = true
	c.Slots = append(c.Slots, Slot{})
	copy(c.Slots[at+1:], c.Slots[at:])
	c.Slots[at] = s
	c.AssignIDs()
	return at
}

// InsertPositional inserts positional s at its Index, before the
// positionals at that position or later, which move up one position.
func (c *Command) InsertPositional(s Slot) int {
	at := 0
	for i, x := range c.Slots {
		if x.Kind != Positional {
			continue
		}
		if x.Index >= s.Index {
			return c.Insert(i, s)
		}
		at = i + 1
	}
	return c.Insert(at, s)
}

// Extract removes slot i and returns it. The positionals after a removed
// positional move down one position, so `get pods NAME` without "pods" has
// NAME at position 1.
func (c *Command) Extract(i int) Slot {
	s := c.Slots[i]
	c.Remove(i)
	if s.Kind == Positional {
		for j := i; j < len(c.Slots); j++ {
			if c.Slots[j].Kind == Positional {
				c.Slots[j].Index--
			}
		}
	}
	return s
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
