package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestValidColor(t *testing.T) {
	for c, want := range map[string]bool{
		"0": true, "255": true, "#fff": true, "#5f87FF": true,
		"256": false, "-1": false, "red": false, "#ffff": false, "#ggg": false, "": false,
	} {
		if got := validColor(c); got != want {
			t.Errorf("validColor(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestThemeValidate(t *testing.T) {
	if err := DefaultTheme().Validate(); err != nil {
		t.Fatalf("default theme: %v", err)
	}
	for _, tt := range []struct {
		theme Theme
		want  string
	}{
		{Theme{"nope": {}}, `unknown role "nope"`},
		{Theme{RoleFlag: {Fg: "blue"}}, `theme.flag.fg: invalid color "blue"`},
		{Theme{RoleArg: {Bg: "#12"}}, `theme.arg.bg: invalid color "#12"`},
	} {
		if err := tt.theme.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v: err = %v, want %q", tt.theme, err, tt.want)
		}
	}
}

func TestNewStylesTheme(t *testing.T) {
	st := newStyles(lipgloss.NewRenderer(nil), Theme{RoleFlag: {Fg: "9", Bg: "#000", Underline: true}})
	if st.flag.GetForeground() != lipgloss.Color("9") || st.flag.GetBackground() != lipgloss.Color("#000") ||
		!st.flag.GetUnderline() || st.flag.GetBold() {
		t.Errorf("flag = %v", st.flag)
	}
	// Roles the theme doesn't mention keep their defaults.
	if st.value.GetForeground() != lipgloss.Color("2") || st.spinner.GetForeground() != lipgloss.Color("2") || !st.border.GetFaint() || !st.focus.GetReverse() {
		t.Errorf("value = %v, border = %v, focus = %v", st.value, st.border, st.focus)
	}
}
