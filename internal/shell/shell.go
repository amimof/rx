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
	"github.com/amimof/rx/internal/shell/zsh"
)

// CursorUnit is how a shell counts its cursor position.
type CursorUnit int

const (
	// Chars counts characters (zsh CURSOR, fish commandline -C).
	Chars CursorUnit = iota
	// Bytes counts bytes (bash READLINE_POINT).
	Bytes
)

// Shell describes one supported shell.
type Shell struct {
	Name string
	// Script returns the widget script calling the rx binary at bin.
	Script func(bin string) string
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
		Script:      zsh.Script,
		HistFile:    func() string { return home(".zsh_history") },
		ReadHistory: history.ReadZsh,
		Cursor:      Chars,
	},
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
