package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Parts of the editor that a theme styles.
const (
	// RoleHead is the command word in the preview.
	RoleHead = "head"
	// RoleFlag is a flag name in the preview and the "+ flag" slot.
	RoleFlag = "flag"
	// RoleValue is a flag's value in the preview.
	RoleValue = "value"
	// RoleArg is a positional argument in the preview.
	RoleArg = "arg"
	// RoleSel is the selected candidate in the list.
	RoleSel = "sel"
	// RoleSpinner is the spinner shown while candidates load.
	RoleSpinner = "spinner"
	// RoleDim is secondary text: descriptions, status and the help line.
	RoleDim = "dim"
	// RoleErr is an error message.
	RoleErr = "err"
	// RoleBorder is the box around the editor.
	RoleBorder = "border"
	// RolePrompt is the symbol before the command.
	RolePrompt = "prompt"
	// RoleFocus is the separator between the focused flag and its value.
	RoleFocus = "focus"
	// RoleLink is a slot linked to the focused one in the preview.
	RoleLink = "link"
)

// Style is how a part of the editor is drawn. Colors are an ANSI color
// number, "0" to "255", or a hex color, "#rgb" or "#rrggbb"; empty leaves
// the terminal's color.
type Style struct {
	Fg, Bg                                  string
	Bold, Italic, Faint, Underline, Reverse bool
}

// Theme maps roles to styles.
type Theme map[string]Style

// DefaultTheme returns the default theme.
func DefaultTheme() Theme {
	return Theme{
		RoleHead:    {Bold: true},
		RoleFlag:    {Fg: "4"},
		RoleValue:   {Fg: "2"},
		RoleArg:     {Fg: "3"},
		RoleSel:     {Fg: "6", Bold: true},
		RoleSpinner: {Fg: "2"},
		RoleDim:     {Faint: true},
		RoleErr:     {Fg: "1"},
		RoleBorder:  {Fg: "240", Faint: true},
		RolePrompt:  {Bold: true},
		RoleFocus:   {Reverse: true},
		RoleLink:    {Underline: true},
	}
}

// Validate reports unknown roles and invalid colors.
func (t Theme) Validate() error {
	known := DefaultTheme()
	roles := make([]string, 0, len(t))
	for r := range t {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		if _, ok := known[r]; !ok {
			return fmt.Errorf("theme: unknown role %q", r)
		}
		s := t[r]
		for _, c := range []struct{ attr, v string }{{"fg", s.Fg}, {"bg", s.Bg}} {
			if c.v != "" && !validColor(c.v) {
				return fmt.Errorf("theme.%s.%s: invalid color %q (want 0-255 or #rrggbb)", r, c.attr, c.v)
			}
		}
	}
	return nil
}

// validColor reports whether c is an ANSI color number or a hex color.
func validColor(c string) bool {
	if hex, ok := strings.CutPrefix(c, "#"); ok {
		if len(hex) != 3 && len(hex) != 6 {
			return false
		}
		_, err := strconv.ParseUint(hex, 16, 32)
		return err == nil
	}
	n, err := strconv.Atoi(c)
	return err == nil && n >= 0 && n <= 255
}

// style returns s as a lipgloss style drawn by r.
func (s Style) style(r *lipgloss.Renderer) lipgloss.Style {
	st := r.NewStyle().
		Bold(s.Bold).Italic(s.Italic).Faint(s.Faint).
		Underline(s.Underline).Reverse(s.Reverse)
	if s.Fg != "" {
		st = st.Foreground(lipgloss.Color(s.Fg))
	}
	if s.Bg != "" {
		st = st.Background(lipgloss.Color(s.Bg))
	}
	return st
}
