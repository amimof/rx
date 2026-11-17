package focus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "focus.json")
	s := Load(path)
	if got := s.Get("curl"); got != "" {
		t.Fatalf("empty store: Get = %q", got)
	}
	s.Set("curl", "request")
	s.Set("kubectl", "namespace")
	s.Set("curl", "url")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s = Load(path)
	if s.Get("curl") != "url" || s.Get("kubectl") != "namespace" {
		t.Fatalf("reloaded = %v", s.slots)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Fatalf("temporary files left behind: %v", ents)
	}
}

func TestCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Load(path)
	if s.Get("curl") != "" {
		t.Fatal("corrupt file should load empty")
	}
	s.Set("curl", "request")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if Load(path).Get("curl") != "request" {
		t.Fatal("save should replace a corrupt file")
	}
}
