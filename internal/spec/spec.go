// Package spec loads declarative YAML plugins describing a command's flags,
// positionals, value providers and transforms.
package spec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Backend selects where completions for a command come from.
const (
	CompleteAuto  = "auto"
	CompleteCobra = "cobra"
	CompleteZsh   = "zsh"
	CompleteNone  = "none"
)

// Spec is a plugin for one command.
type Spec struct {
	Name  string `yaml:"name"`
	Match struct {
		Command string   `yaml:"command"`
		Aliases []string `yaml:"aliases"`
	} `yaml:"match"`
	Complete    string              `yaml:"complete"`
	Flags       []FlagSpec          `yaml:"flags"`
	Positionals []PosSpec           `yaml:"positionals"`
	Providers   map[string]Provider `yaml:"providers"`
	Transforms  []Transform         `yaml:"transforms"`

	// Source is the file the spec was loaded from.
	Source string `yaml:"-"`
}

// FlagSpec describes a flag.
type FlagSpec struct {
	Name     string   `yaml:"name"`
	Short    string   `yaml:"short"`
	Takes    string   `yaml:"takes"` // value | none | "" (infer)
	Values   []string `yaml:"values"`
	Provider string   `yaml:"provider"`
	Mutex    []string `yaml:"mutex"`
}

// PosSpec describes a positional argument.
type PosSpec struct {
	Index    int      `yaml:"index"`
	Name     string   `yaml:"name"`
	Values   []string `yaml:"values"`
	Provider string   `yaml:"provider"`
}

// Provider produces candidate values by running a command. Exec is a Go
// template that can reference other slots by key, e.g. {{.namespace}}.
type Provider struct {
	Exec  string   `yaml:"exec"`
	Strip string   `yaml:"strip"`
	TTL   Duration `yaml:"ttl"`
}

// Transform is a one-key action in the editor.
type Transform struct {
	Key    string   `yaml:"key"`
	Op     string   `yaml:"op"` // toggle | cycle | set | unset
	Flag   string   `yaml:"flag"`
	Slot   string   `yaml:"slot"`
	Value  string   `yaml:"value"`
	Values []string `yaml:"values"`
	Desc   string   `yaml:"desc"`
}

// Target returns the slot key the transform acts on.
func (t Transform) Target() string {
	if t.Flag != "" {
		return t.Flag
	}
	return t.Slot
}

// Duration is a time.Duration that unmarshals from strings like "30s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

// Flag finds a flag by long name, short name, or as written ("-n",
// "--namespace").
func (s *Spec) Flag(name string) *FlagSpec {
	if s == nil {
		return nil
	}
	long := strings.HasPrefix(name, "--")
	short := !long && strings.HasPrefix(name, "-")
	n := strings.TrimLeft(name, "-")
	for i := range s.Flags {
		f := &s.Flags[i]
		if (!short && f.Name == n) || (!long && f.Short != "" && f.Short == n) {
			return f
		}
	}
	return nil
}

// Positional returns the spec for positional index i.
func (s *Spec) Positional(i int) *PosSpec {
	if s == nil {
		return nil
	}
	for j := range s.Positionals {
		if s.Positionals[j].Index == i {
			return &s.Positionals[j]
		}
	}
	return nil
}

// PositionalByName returns the positional spec with the given name.
func (s *Spec) PositionalByName(name string) *PosSpec {
	if s == nil {
		return nil
	}
	for j := range s.Positionals {
		if s.Positionals[j].Name == name {
			return &s.Positionals[j]
		}
	}
	return nil
}

// Backend returns the configured completion backend, defaulting to auto.
func (s *Spec) Backend() string {
	if s == nil || s.Complete == "" {
		return CompleteAuto
	}
	return s.Complete
}

// Validate reports problems with the spec.
func (s *Spec) Validate() []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if s.Match.Command == "" {
		add("match.command is required")
	}
	switch s.Complete {
	case "", CompleteAuto, CompleteCobra, CompleteZsh, CompleteNone:
	default:
		add("complete: unknown backend %q", s.Complete)
	}
	seen := map[string]bool{}
	for _, f := range s.Flags {
		if f.Name == "" {
			add("flag with short %q has no name", f.Short)
			continue
		}
		if seen[f.Name] {
			add("flag %q defined twice", f.Name)
		}
		seen[f.Name] = true
		switch f.Takes {
		case "", "value", "none":
		default:
			add("flag %q: takes must be value or none, got %q", f.Name, f.Takes)
		}
		if f.Provider != "" {
			if _, ok := s.Providers[f.Provider]; !ok {
				add("flag %q: unknown provider %q", f.Name, f.Provider)
			}
		}
	}
	for _, f := range s.Flags {
		for _, m := range f.Mutex {
			if !seen[m] {
				add("flag %q: mutex refers to unknown flag %q", f.Name, m)
			}
		}
	}
	for _, p := range s.Positionals {
		if p.Provider != "" {
			if _, ok := s.Providers[p.Provider]; !ok {
				add("positional %q: unknown provider %q", p.Name, p.Provider)
			}
		}
	}
	for name, p := range s.Providers {
		if p.Exec == "" {
			add("provider %q: exec is required", name)
		}
	}
	keys := map[string]bool{}
	for _, t := range s.Transforms {
		if t.Key == "" {
			add("transform without key")
		} else if keys[t.Key] {
			add("transform key %q bound twice", t.Key)
		}
		keys[t.Key] = true
		switch t.Op {
		case "toggle", "cycle", "set", "unset":
		default:
			add("transform %q: unknown op %q", t.Key, t.Op)
		}
		if t.Target() == "" {
			add("transform %q: flag or slot is required", t.Key)
		}
		if t.Flag != "" && s.Flag("--"+t.Flag) == nil {
			add("transform %q: unknown flag %q", t.Key, t.Flag)
		}
		if t.Slot != "" && s.PositionalByName(t.Slot) == nil {
			add("transform %q: unknown slot %q", t.Key, t.Slot)
		}
	}
	return errs
}

// Parse decodes a single spec.
func Parse(data []byte, source string) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	s.Source = source
	return &s, nil
}

// Registry maps command names to specs.
type Registry struct {
	byCmd map[string]*Spec
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byCmd: map[string]*Spec{}} }

// Add registers s, replacing any spec for the same command. Later sources
// therefore override earlier ones.
func (r *Registry) Add(s *Spec) {
	r.byCmd[s.Match.Command] = s
	for _, a := range s.Match.Aliases {
		r.byCmd[a] = s
	}
}

// Lookup returns the spec for a resolved command name, or nil.
func (r *Registry) Lookup(cmd string) *Spec {
	if r == nil {
		return nil
	}
	return r.byCmd[cmd]
}

// Specs returns all distinct specs sorted by name.
func (r *Registry) Specs() []*Spec {
	seen := map[*Spec]bool{}
	var out []*Spec
	for _, s := range r.byCmd {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Match.Command < out[j].Match.Command })
	return out
}

// LoadFS loads every *.yaml file in fsys into r.
func (r *Registry) LoadFS(fsys fs.FS, prefix string) error {
	matches, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return err
	}
	for _, m := range matches {
		data, err := fs.ReadFile(fsys, m)
		if err != nil {
			return err
		}
		s, err := Parse(data, prefix+m)
		if err != nil {
			return err
		}
		r.Add(s)
	}
	return nil
}

// LoadDir loads every *.yaml file in dir. A missing directory is not an
// error.
func (r *Registry) LoadDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return r.LoadFS(os.DirFS(dir), filepath.Clean(dir)+string(filepath.Separator))
}
