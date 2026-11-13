package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/internal/tui"
)

// home points the config directory at a fresh directory and clears RX_*
// variables that would override the file.
func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	for _, e := range os.Environ() {
		if k, _, _ := strings.Cut(e, "="); strings.HasPrefix(k, "RX_") {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	return dir
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	home(t)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != DefaultKey || c.Log != "" || c.File != "" || c.Prompt != tui.DefaultPrompt {
		t.Fatalf("config = %+v", c)
	}
	if !slices.Equal(c.Keys[tui.ActAccept], []string{"enter"}) || !slices.Equal(c.Keys[tui.ActRun], []string{"ctrl+e"}) {
		t.Fatalf("accept = %v, run = %v", c.Keys[tui.ActAccept], c.Keys[tui.ActRun])
	}
}

func TestFileAndEnv(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, "rx", "config.yaml"), `
key: "^[e"
log: ~/rx.log
keys:
  accept: [ctrl+j]
  drop: []
`)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != "^[e" || c.Log != filepath.Join(dir, "rx.log") {
		t.Fatalf("key %q, log %q", c.Key, c.Log)
	}
	if !slices.Equal(c.Keys[tui.ActAccept], []string{"ctrl+j"}) || len(c.Keys[tui.ActDrop]) != 0 {
		t.Fatalf("keys = %v", c.Keys)
	}
	// Actions the file doesn't mention keep their defaults.
	if !slices.Equal(c.Keys[tui.ActCancel], []string{"ctrl+c"}) {
		t.Fatalf("cancel = %v", c.Keys[tui.ActCancel])
	}

	t.Setenv("RX_KEY", "^Xr")
	t.Setenv("RX_KEYS_NEXT_SLOT", "tab,ctrl+f")
	t.Setenv("RX_KEYS_RUN", "ctrl+r")
	if c, err = Load(""); err != nil {
		t.Fatal(err)
	}
	if c.Key != "^Xr" || !slices.Equal(c.Keys[tui.ActNextSlot], []string{"tab", "ctrl+f"}) {
		t.Fatalf("env: key %q, next-slot %v", c.Key, c.Keys[tui.ActNextSlot])
	}
	if !slices.Equal(c.Keys[tui.ActRun], []string{"ctrl+r"}) {
		t.Fatalf("env: run %v", c.Keys[tui.ActRun])
	}
}

func TestPrompt(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, "rx", "config.yaml"), "prompt: '$'\n")
	c, err := Load("")
	if err != nil || c.Prompt != "$" {
		t.Fatalf("prompt %q, err %v", c.Prompt, err)
	}
	// An empty prompt turns the symbol off rather than restoring the default.
	write(t, filepath.Join(dir, "rx", "config.yaml"), "prompt: ''\n")
	if c, err = Load(""); err != nil || c.Prompt != "" {
		t.Fatalf("empty prompt %q, err %v", c.Prompt, err)
	}
	t.Setenv("RX_PROMPT", ">")
	if c, err = Load(""); err != nil || c.Prompt != ">" {
		t.Fatalf("env prompt %q, err %v", c.Prompt, err)
	}
}

func TestRules(t *testing.T) {
	dir := home(t)
	c, err := Load("")
	if err != nil || c.Rules != tui.RulesLive {
		t.Fatalf("default rules %q, err %v", c.Rules, err)
	}
	for _, mode := range tui.RulesModes {
		write(t, filepath.Join(dir, "rx", "config.yaml"), "rules: "+mode+"\n")
		if c, err = Load(""); err != nil || c.Rules != mode {
			t.Fatalf("%s: rules %q, err %v", mode, c.Rules, err)
		}
	}
	t.Setenv("RX_RULES", "commit")
	if c, err = Load(""); err != nil || c.Rules != tui.RulesCommit {
		t.Fatalf("env rules %q, err %v", c.Rules, err)
	}
}

func TestComplete(t *testing.T) {
	dir := home(t)
	c, err := Load("")
	if err != nil || c.Complete != nil {
		t.Fatalf("default complete %q, err %v", c.Complete, err)
	}
	for _, tt := range []struct {
		yaml string
		want spec.Sources
	}{
		{"complete: shell\n", spec.Sources{"shell"}},
		{"complete: [history, cli]\n", spec.Sources{"history", "cli"}},
		{"complete: none\n", spec.Sources{"none"}},
	} {
		write(t, filepath.Join(dir, "rx", "config.yaml"), tt.yaml)
		if c, err = Load(""); err != nil || !slices.Equal(c.Complete, tt.want) {
			t.Fatalf("%q: complete %q, err %v", tt.yaml, c.Complete, err)
		}
	}
	t.Setenv("RX_COMPLETE", "history, spec")
	if c, err = Load(""); err != nil || !slices.Equal(c.Complete, spec.Sources{"history", "spec"}) {
		t.Fatalf("env complete %q, err %v", c.Complete, err)
	}
}

func TestTheme(t *testing.T) {
	dir := home(t)
	c, err := Load("")
	if err != nil || !reflect.DeepEqual(c.Theme, tui.DefaultTheme()) {
		t.Fatalf("default theme %v, err %v", c.Theme, err)
	}
	write(t, filepath.Join(dir, "rx", "config.yaml"), `
theme:
  flag: "#5f87ff"
  arg: 13
  sel: { fg: "#fa0", bg: 236, bold: false, underline: true }
  border: {}
`)
	if c, err = Load(""); err != nil {
		t.Fatal(err)
	}
	want := tui.DefaultTheme()
	want[tui.RoleFlag] = tui.Style{Fg: "#5f87ff"}
	want[tui.RoleArg] = tui.Style{Fg: "13"}
	want[tui.RoleSel] = tui.Style{Fg: "#fa0", Bg: "236", Underline: true}
	want[tui.RoleBorder] = tui.Style{}
	if !reflect.DeepEqual(c.Theme, want) {
		t.Fatalf("theme = %v\nwant    %v", c.Theme, want)
	}
}

func TestExplicitFile(t *testing.T) {
	dir := home(t)
	f := filepath.Join(dir, "other.yaml")
	write(t, f, "key: '^Xo'\n")
	c, err := Load(f)
	if err != nil || c.Key != "^Xo" || c.File != f {
		t.Fatalf("config %+v, err %v", c, err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("a missing explicit file should be an error")
	}
}

func TestInvalid(t *testing.T) {
	for _, tt := range []struct{ yaml, want string }{
		{"nope: 1\n", `unknown setting "nope"`},
		{"keys: { jump: [ctrl+j] }\n", `unknown action "jump"`},
		{"keys: { accept: [x] }\n", "used for typing"},
		{"keys: { drop: [ctrl+e] }\n", "bound to both"},
		{"keys: { yank: [ctrl+y] }\n", `unknown action "yank"`},
		{"key: [\n", "config"},
		{"theme: { nope: '1' }\n", `theme: unknown role "nope"`},
		{"theme: { flag: { colour: '1' } }\n", `theme.flag: unknown attribute "colour"`},
		{"theme: { flag: { bold: yes-please } }\n", "theme.flag: bold: want true or false"},
		{"theme: { flag: red }\n", `theme.flag.fg: invalid color "red"`},
		{"theme: { arg: { bg: 256 } }\n", `theme.arg.bg: invalid color "256"`},
		{"theme: { arg: [1] }\n", "theme.arg: want a color or a map"},
		{"theme: dark\n", "theme: want a map of roles"},
		{"complete: zsh\n", `complete: unknown source "zsh" (use "shell")`},
		{"complete: [auto, shell]\n", `complete: "auto" can't be combined`},
		{"complete: []\n", "complete: empty list of sources"},
		{"complete: { cobra: true }\n", "complete: want a source or a list"},
		{"rules: always\n", `rules: want one of live, commit, off, got "always"`},
	} {
		dir := home(t)
		write(t, filepath.Join(dir, "rx", "config.yaml"), tt.yaml)
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want %q", tt.yaml, err, tt.want)
		}
	}
}
