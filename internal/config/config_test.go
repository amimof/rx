package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	} {
		dir := home(t)
		write(t, filepath.Join(dir, "rx", "config.yaml"), tt.yaml)
		if _, err := Load(""); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want %q", tt.yaml, err, tt.want)
		}
	}
}
