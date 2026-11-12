// Package spec loads declarative YAML plugins describing a command's flags,
// positionals, value providers and transforms.
package spec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Completion sources a command's candidates can come from.
const (
	SourceHistory  = "history"
	SourceSpec     = "spec"
	SourceProvider = "provider"
	SourceCobra    = "cobra"
	// SourceShell asks the shell rx runs under, as <Tab> would.
	SourceShell = "shell"

	// SourceAuto enables every source, SourceNone none of them. Either must
	// be used on its own.
	SourceAuto = "auto"
	SourceNone = "none"
)

// AllSources lists the sources in the order Ctrl-S cycles through them.
var AllSources = []string{SourceHistory, SourceSpec, SourceProvider, SourceCobra, SourceShell}

// Sources is the list of enabled completion sources. It unmarshals from
// either a single string or a list. A nil Sources is unset.
type Sources []string

func (s *Sources) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag == "!!null" || (n.Kind == yaml.ScalarNode && n.Value == "") {
		*s = nil
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		*s = Sources{n.Value}
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	if l == nil {
		l = []string{}
	}
	*s = l
	return nil
}

// ParseSources converts a value from a config file (a string or a list), or
// a comma or space separated string from the environment, to Sources.
func ParseSources(v any) (Sources, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		// An empty string is unset, as in a plugin.
		return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }), nil
	case []string:
		return append(Sources{}, v...), nil
	case []any:
		out := Sources{}
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("want a source name, got %v", x)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("want a source or a list of sources, got %v", v)
}

// Validate reports unknown, repeated or conflicting sources.
func (s Sources) Validate() []error {
	if s == nil {
		return nil
	}
	if len(s) == 0 {
		return []error{fmt.Errorf("empty list of sources (use %q)", SourceNone)}
	}
	var errs []error
	seen := map[string]bool{}
	for _, n := range s {
		switch {
		case n == SourceAuto || n == SourceNone:
			if len(s) > 1 {
				errs = append(errs, fmt.Errorf("%q can't be combined with other sources", n))
			}
		case n == "zsh":
			errs = append(errs, fmt.Errorf("unknown source %q (use %q)", n, SourceShell))
		case !slices.Contains(AllSources, n):
			errs = append(errs, fmt.Errorf("unknown source %q", n))
		}
		if seen[n] {
			errs = append(errs, fmt.Errorf("source %q listed twice", n))
		}
		seen[n] = true
	}
	return errs
}

// Set returns the enabled sources. Unset means auto.
func (s Sources) Set() map[string]bool {
	if s == nil {
		s = Sources{SourceAuto}
	}
	out := map[string]bool{}
	for _, n := range s {
		switch n {
		case SourceNone:
			return map[string]bool{}
		case SourceAuto:
			for _, a := range AllSources {
				out[a] = true
			}
		default:
			out[n] = true
		}
	}
	return out
}

// String returns the sources as written, or "auto" if unset.
func (s Sources) String() string {
	if s == nil {
		return SourceAuto
	}
	if len(s) == 0 {
		return SourceNone
	}
	return strings.Join(s, ",")
}

// Spec is a plugin for one command.
type Spec struct {
	Name  string `yaml:"name"`
	Match struct {
		Command string   `yaml:"command"`
		Aliases []string `yaml:"aliases"`
	} `yaml:"match"`
	// Complete lists the enabled completion sources, replacing the global
	// setting. Unset uses the global setting.
	Complete    Sources             `yaml:"complete"`
	Flags       []FlagSpec          `yaml:"flags"`
	Positionals []PosSpec           `yaml:"positionals"`
	Providers   map[string]Provider `yaml:"providers"`
	Transforms  []Transform         `yaml:"transforms"`
	// Focus lists the slots the editor starts on, in order of preference.
	// The first one present in the command wins.
	Focus FocusList `yaml:"focus"`

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
	// Key is "ctrl+<letter>", "alt+<char>", or a bare character, which
	// means alt+<char>.
	Key    string   `yaml:"key"`
	Op     string   `yaml:"op"` // toggle | cycle | set | unset | focus
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

// Binding returns the key as bubbletea names it, e.g. "ctrl+a" or "alt+A".
func (t Transform) Binding() string {
	if strings.HasPrefix(t.Key, "ctrl+") || strings.HasPrefix(t.Key, "alt+") {
		return t.Key
	}
	return "alt+" + t.Key
}

// checkBinding reports why a transform binding can't be typed, or "".
func checkBinding(b string) string {
	if c, ok := strings.CutPrefix(b, "ctrl+"); ok {
		// Terminals send ctrl+i and ctrl+m as tab and enter, and can't tell
		// ctrl+A from ctrl+a.
		if len(c) != 1 || c[0] < 'a' || c[0] > 'z' || c == "i" || c == "m" {
			return "ctrl+ takes a lowercase letter other than i and m"
		}
		return ""
	}
	c, _ := strings.CutPrefix(b, "alt+")
	if utf8.RuneCountInString(c) != 1 || c == " " {
		return "key must be a single character"
	}
	return ""
}

// FocusList is a list of slot references that unmarshals from either a
// single string or a list. An entry is a positional name, "pos:N", a long
// flag name, or a flag as written ("-n", "--namespace", "-name").
type FocusList []string

func (f *FocusList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*f = FocusList{n.Value}
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	*f = l
	return nil
}

// FocusKey returns the slot key a focus entry refers to. Flags written with
// dashes resolve through the spec, falling back to the name without dashes
// like the classifier does for flags the spec doesn't know.
func (s *Spec) FocusKey(entry string) string {
	if !strings.HasPrefix(entry, "-") {
		return entry
	}
	if f := s.Flag(entry); f != nil {
		return f.Name
	}
	return strings.TrimLeft(entry, "-")
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

// Enabled returns the completion sources enabled for the command: the
// spec's, else global, else all of them.
func (s *Spec) Enabled(global Sources) map[string]bool {
	if s != nil && s.Complete != nil {
		return s.Complete.Set()
	}
	return global.Set()
}

// Validate reports problems with the spec.
func (s *Spec) Validate() []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if s.Match.Command == "" {
		add("match.command is required")
	}
	for _, err := range s.Complete.Validate() {
		add("complete: %v", err)
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
	for _, e := range s.Focus {
		if strings.Trim(e, "-") == "" {
			add("focus: empty slot reference %q", e)
		}
	}
	keys := map[string]bool{}
	for _, t := range s.Transforms {
		b := t.Binding()
		switch {
		case t.Key == "":
			add("transform without key")
		case keys[b]:
			add("transform key %q bound twice", t.Key)
		default:
			if why := checkBinding(b); why != "" {
				add("transform %q: %s", t.Key, why)
			}
		}
		keys[b] = true
		switch t.Op {
		case "toggle", "cycle", "set", "unset", "focus":
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

// Add registers s, replacing any spec for the same command along with its
// aliases. Later sources therefore override earlier ones.
func (r *Registry) Add(s *Spec) {
	if old := r.byCmd[s.Match.Command]; old != nil {
		for k, v := range r.byCmd {
			if v == old {
				delete(r.byCmd, k)
			}
		}
	}
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
