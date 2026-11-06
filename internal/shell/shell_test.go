package shell

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	for _, tt := range []struct{ name, env, want, err string }{
		{"zsh", "/bin/bash", "zsh", ""},
		{"", "/usr/bin/zsh", "zsh", ""},
		{"", "/bin/unknown", Default, ""},
		{"", "", Default, ""},
		{"nosuch", "/usr/bin/zsh", "", `unsupported shell "nosuch"`},
	} {
		t.Setenv("SHELL", tt.env)
		s, err := Resolve(tt.name)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("Resolve(%q) err = %v, want %q", tt.name, err, tt.err)
			}
			continue
		}
		if err != nil || s.Name != tt.want {
			t.Errorf("Resolve(%q) with SHELL=%q = %v, %v; want %s", tt.name, tt.env, s, err, tt.want)
		}
	}
}

func TestRegistry(t *testing.T) {
	for _, n := range Names() {
		s, ok := Lookup(n)
		if !ok || s.Name != n || s.Script == nil || s.HistFile == nil || s.ReadHistory == nil {
			t.Errorf("shell %q incomplete: %+v", n, s)
		}
		if !strings.Contains(s.Script("/bin/rx"), "/bin/rx") {
			t.Errorf("shell %q: script does not use the binary path", n)
		}
	}
}

func TestCursor(t *testing.T) {
	buf := "echo héllo wörld"
	chars := &Shell{Cursor: Chars}
	bytes := &Shell{Cursor: Bytes}
	// The cursor after "wö": 13 characters, 15 bytes.
	at := strings.Index(buf, "rld")
	if got := chars.ToByte(buf, 13); got != at {
		t.Errorf("chars ToByte = %d, want %d", got, at)
	}
	if got := chars.FromByte(buf, at); got != 13 {
		t.Errorf("chars FromByte = %d", got)
	}
	if got := bytes.ToByte(buf, at); got != at {
		t.Errorf("bytes ToByte = %d, want %d", got, at)
	}
	if got := bytes.FromByte(buf, at); got != at {
		t.Errorf("bytes FromByte = %d", got)
	}
	for _, s := range []*Shell{chars, bytes} {
		if got := s.ToByte(buf, 100); got != len(buf) {
			t.Errorf("unit %d: past end = %d", s.Cursor, got)
		}
	}
}
