// Package focus remembers the slot the user last edited per command, so the
// editor can start on it next time (the "last" focus entry).
package focus

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Store maps a resolved command name to a slot key.
type Store struct {
	path  string
	slots map[string]string
}

// Load reads the store at path. A missing or unreadable file gives an empty
// store: losing the remembered slots only means starting on the default.
func Load(path string) *Store {
	s := &Store{path: path, slots: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var m map[string]string
	if json.Unmarshal(data, &m) == nil && m != nil {
		s.slots = m
	}
	return s
}

// Get returns the slot key remembered for cmd, or "".
func (s *Store) Get(cmd string) string { return s.slots[cmd] }

// Set remembers key for cmd.
func (s *Store) Set(cmd, key string) { s.slots[cmd] = key }

// Save writes the store through a temporary file, so shells saving at the
// same time can't leave a torn file. The last one wins.
func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s.slots)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "focus-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
