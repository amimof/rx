package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Editor actions that keys can be bound to.
const (
	ActAccept   = "accept"
	ActRun      = "run"
	ActCancel   = "cancel"
	ActBack     = "back"
	ActUp       = "up"
	ActDown     = "down"
	ActNextSlot = "next-slot"
	ActPrevSlot = "prev-slot"
	ActSource   = "source"
	ActDrop     = "drop"
	ActClear    = "clear"
	// ActUndo reverts the last edit, along with what rules did after it.
	ActUndo = "undo"
)

// Keymap maps editor actions to the keys that trigger them. Keys are named
// as bubbletea's KeyMsg.String() names them: "enter", "ctrl+e", "shift+tab",
// "alt+y".
type Keymap map[string][]string

// DefaultKeys returns the default keymap.
func DefaultKeys() Keymap {
	return Keymap{
		ActAccept:   {"enter"},
		ActRun:      {"ctrl+e"},
		ActCancel:   {"ctrl+c"},
		ActBack:     {"esc"},
		ActUp:       {"up", "ctrl+p"},
		ActDown:     {"down", "ctrl+n"},
		ActNextSlot: {"tab"},
		ActPrevSlot: {"shift+tab"},
		ActSource:   {"ctrl+s"},
		ActDrop:     {"ctrl+d"},
		ActClear:    {"ctrl+u", "ctrl+w"},
		ActUndo:     {"ctrl+z"},
	}
}

// Validate reports unknown actions, keys that are empty or reserved for
// typing a search, and keys bound to more than one action.
func (k Keymap) Validate() error {
	known := DefaultKeys()
	actions := make([]string, 0, len(k))
	for a := range k {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	bound := map[string]string{}
	for _, a := range actions {
		if _, ok := known[a]; !ok {
			return fmt.Errorf("keys: unknown action %q", a)
		}
		for _, key := range k[a] {
			switch {
			case key == "":
				return fmt.Errorf("keys.%s: empty key", a)
			case key == " " || key == "space" || utf8.RuneCountInString(key) == 1:
				return fmt.Errorf("keys.%s: %q is used for typing", a, key)
			}
			if b, ok := bound[key]; ok && b != a {
				return fmt.Errorf("keys: %q is bound to both %s and %s", key, b, a)
			}
			bound[key] = a
		}
	}
	return nil
}

// actions returns the action for each key.
func (k Keymap) actions() map[string]string {
	out := map[string]string{}
	for a, keys := range k {
		for _, key := range keys {
			out[key] = a
		}
	}
	return out
}

// label returns the first key bound to action as shown in the help line,
// or "" if it is unbound.
func (k Keymap) label(action string) string {
	keys := k[action]
	if len(keys) == 0 {
		return ""
	}
	return keyLabel(keys[0])
}

// keyLabel returns key as shown in the help line, e.g. "^e" for "ctrl+e".
func keyLabel(key string) string {
	switch key {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "shift+tab":
		return "s-tab"
	}
	for _, p := range [][2]string{{"ctrl+", "^"}, {"alt+", "M-"}, {"shift+", "S-"}} {
		if rest, ok := strings.CutPrefix(key, p[0]); ok {
			return p[1] + rest
		}
	}
	return key
}

// helpItems returns the help line's entries for the editor's own keys,
// e.g. "^e run". undo is shown only if there is something to undo or the
// plugin has transforms or rules.
func (k Keymap) helpItems(undo bool) []string {
	var parts []string
	add := func(desc string, actions ...string) {
		var ls []string
		for _, a := range actions {
			if l := k.label(a); l != "" {
				ls = append(ls, l)
			}
		}
		if len(ls) > 0 {
			sep := "/"
			if slices.Equal(ls, []string{"↑", "↓"}) {
				sep = ""
			}
			parts = append(parts, strings.Join(ls, sep)+" "+desc)
		}
	}
	add("slot", ActNextSlot, ActPrevSlot)
	add("pick", ActUp, ActDown)
	add("accept", ActAccept)
	add("run", ActRun)
	add("source", ActSource)
	add("drop", ActDrop)
	if undo {
		add("undo", ActUndo)
	}
	add("cancel", ActBack)
	return parts
}
