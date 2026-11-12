// Package shell describes the shells rx integrates with. Each shell has a
// widget script (printed by `rx init <shell>`) that runs `rx edit` and
// speaks the protocol in package proto, and a history format.
//
// Adding a shell takes a widget under internal/shell/<name>, a history
// reader and an entry in the registry below.
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/shell/bash"
	"github.com/amimof/rx/internal/shell/fish"
	"github.com/amimof/rx/internal/shell/zsh"
)

// CursorUnit is how a shell counts its cursor position.
type CursorUnit int

const (
	// Chars counts characters (zsh CURSOR, bash 5 READLINE_POINT, fish
	// commandline -C).
	Chars CursorUnit = iota
	// Bytes counts bytes.
	Bytes
)

// ScriptOptions are the values `rx init` bakes into a widget script: the rx
// binary and the configured key binding and log file.
type ScriptOptions struct {
	// Bin is the rx binary the widget runs.
	Bin string
	// Key is the default key binding in caret notation (^X^X). RX_KEY set
	// in the shell still overrides it.
	Key string
	// Log is the default log file, overridden by RX_LOG. Empty keeps the
	// widget's default.
	Log string
}

// Shell describes one supported shell.
type Shell struct {
	Name string
	// Script returns the widget script.
	Script func(ScriptOptions) string
	// HistFile is the default history file, used when the widget does not
	// set RX_HISTFILE.
	HistFile func() string
	// ReadHistory parses the shell's history file.
	ReadHistory history.Reader
	// Cursor is the unit of `rx edit --cursor` and the cursor in DONE.
	Cursor CursorUnit
}

func home(elem ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, elem...)...)
}

var shells = map[string]*Shell{
	"zsh": {
		Name:        "zsh",
		Script:      func(o ScriptOptions) string { return zsh.Script(zsh.Options(o)) },
		HistFile:    func() string { return home(".zsh_history") },
		ReadHistory: history.ReadZsh,
		Cursor:      Chars,
	},
	"bash": {
		Name:        "bash",
		Script:      func(o ScriptOptions) string { return bash.Script(bash.Options(o)) },
		HistFile:    func() string { return home(".bash_history") },
		ReadHistory: history.ReadBash,
		// The widget converts bash 4's byte offsets.
		Cursor: Chars,
	},
	"fish": {
		Name:        "fish",
		Script:      func(o ScriptOptions) string { return fish.Script(fish.Options(o)) },
		HistFile:    fishHistFile,
		ReadHistory: history.ReadFish,
		Cursor:      Chars,
	},
}

// fishHistFile is fish's default history file. The widget sets
// RX_HISTFILE from $fish_history, which names other sessions.
func fishHistFile() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "fish", "fish_history")
	}
	return home(".local", "share", "fish", "fish_history")
}

// Default is used when no shell is named and $SHELL is not supported.
const Default = "zsh"

// Names returns the supported shells, sorted.
func Names() []string {
	var out []string
	for n := range shells {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the named shell.
func Lookup(name string) (*Shell, bool) {
	s, ok := shells[name]
	return s, ok
}

// Resolve returns the named shell. An empty name picks the basename of
// $SHELL if it is supported, otherwise Default.
func Resolve(name string) (*Shell, error) {
	if name == "" {
		if s, ok := shells[filepath.Base(os.Getenv("SHELL"))]; ok {
			return s, nil
		}
		return shells[Default], nil
	}
	if s, ok := shells[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("unsupported shell %q (supported: %v)", name, Names())
}

// ToByte converts cursor position pos in buf from the shell's unit to a
// byte offset. Positions past the end clamp to len(buf).
func (s *Shell) ToByte(buf string, pos int) int {
	if s.Cursor == Bytes {
		return min(pos, len(buf))
	}
	b := 0
	for i := 0; i < pos && b < len(buf); i++ {
		_, n := utf8.DecodeRuneInString(buf[b:])
		b += n
	}
	return b
}

// FromByte converts byte offset b in buf to the shell's cursor unit.
func (s *Shell) FromByte(buf string, b int) int {
	if s.Cursor == Bytes {
		return b
	}
	return utf8.RuneCountInString(buf[:b])
}
