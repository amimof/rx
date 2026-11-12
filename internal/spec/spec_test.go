package spec

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestFocus(t *testing.T) {
	const flags = "match: { command: x }\nflags:\n  - { name: namespace, short: n, takes: value }\n"
	s, err := Parse([]byte(flags+"focus: kind\n"), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Focus) != 1 || s.Focus[0] != "kind" {
		t.Fatalf("scalar focus = %q", s.Focus)
	}
	s, err = Parse([]byte(flags+"focus: [kind, -n]\n"), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Focus) != 2 || s.Focus[1] != "-n" {
		t.Fatalf("list focus = %q", s.Focus)
	}
	if errs := s.Validate(); len(errs) > 0 {
		t.Fatalf("unexpected %v", errs)
	}
	for entry, want := range map[string]string{
		"kind":        "kind",
		"pos:1":       "pos:1",
		"namespace":   "namespace",
		"--namespace": "namespace",
		"-n":          "namespace",
		"-name":       "name",
		"--other":     "other",
	} {
		if got := s.FocusKey(entry); got != want {
			t.Errorf("FocusKey(%q) = %q, want %q", entry, got, want)
		}
	}
	s, err = Parse([]byte(flags+"focus: [kind, '', '--']\n"), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if errs := s.Validate(); len(errs) != 2 {
		t.Fatalf("errors = %v, want 2", errs)
	}
}

func TestAliasChain(t *testing.T) {
	a := Aliases{
		"k":       "kubectl",
		"kubectl": "kubecolor",
		"kc":      "FOO=1 noglob /usr/bin/kubecolor",
		"loop":    "other",
		"other":   "loop -x",
	}
	for word, want := range map[string]string{
		"k":    "[k kubectl kubecolor]",
		"kc":   "[kc kubecolor]",
		"git":  "[git]",
		"loop": "[loop other loop]",
	} {
		if got := fmt.Sprint(a.Chain(word)); got != want {
			t.Errorf("Chain(%q) = %s, want %s", word, got, want)
		}
	}
	if got := a.Resolve("k"); got != "kubecolor" {
		t.Errorf("Resolve(k) = %s", got)
	}
}

func TestClassifierResolve(t *testing.T) {
	kubectl, err := Parse([]byte("match: { command: kubectl, aliases: [k] }\n"), "kubectl.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(kubectl)
	for _, tt := range []struct {
		aliases    Aliases
		word, want string
	}{
		// k is matched by the spec's aliases before the wrapper.
		{Aliases{"k": "kubecolor"}, "k", "kubectl"},
		{Aliases{"k": "kubectl", "kubectl": "kubecolor"}, "k", "kubectl"},
		{Aliases{"kubectl": "kubecolor"}, "kubectl", "kubectl"},
		{Aliases{"k": "kubectl"}, "k", "kubectl"},
		{nil, "k", "kubectl"},
		{nil, "/usr/bin/kubectl", "kubectl"},
		{Aliases{"g": "git"}, "g", "git"},
		{nil, "mytool", "mytool"},
	} {
		c := Classifier{Specs: reg, Aliases: tt.aliases}
		if got := c.Resolve(tt.word); got != tt.want {
			t.Errorf("Resolve(%q) with %v = %s, want %s", tt.word, tt.aliases, got, tt.want)
		}
	}
}

func TestRegistryOverrideDropsAliases(t *testing.T) {
	bundled, _ := Parse([]byte("match: { command: kubectl, aliases: [k] }\n"), "bundled.yaml")
	user, _ := Parse([]byte("match: { command: kubectl }\n"), "user.yaml")
	reg := NewRegistry()
	reg.Add(bundled)
	reg.Add(user)
	if reg.Lookup("kubectl") != user {
		t.Error("kubectl should use the user spec")
	}
	if reg.Lookup("k") != nil {
		t.Error("k should no longer use the bundled spec")
	}
}

func TestTransformKeys(t *testing.T) {
	for _, tt := range []struct{ keys, binding, err string }{
		{"[A]", "alt+A", ""},
		{"[alt+A]", "alt+A", ""},
		{"[ctrl+a]", "ctrl+a", ""},
		{"[A, alt+A]", "alt+A", "bound twice"},
		{"[ctrl+A]", "ctrl+A", "lowercase letter"},
		{"[ctrl+i]", "ctrl+i", "lowercase letter"},
		{"[ctrl+ab]", "ctrl+ab", "lowercase letter"},
		{"[ab]", "alt+ab", "single character"},
	} {
		var y strings.Builder
		y.WriteString("match: { command: x }\nflags:\n  - { name: all, takes: none }\ntransforms:\n")
		for k := range strings.SplitSeq(strings.Trim(tt.keys, "[]"), ", ") {
			fmt.Fprintf(&y, "  - { key: %q, op: toggle, flag: all }\n", k)
		}
		s, err := Parse([]byte(y.String()), "x.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Transforms[0].Binding(); got != tt.binding {
			t.Errorf("%s: binding %q, want %q", tt.keys, got, tt.binding)
		}
		errs := s.Validate()
		switch {
		case tt.err == "" && len(errs) > 0:
			t.Errorf("%s: unexpected %v", tt.keys, errs)
		case tt.err != "" && (len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.err)):
			t.Errorf("%s: errors %v, want %q", tt.keys, errs, tt.err)
		}
	}
}

func TestValidateFocusOp(t *testing.T) {
	s, err := Parse([]byte(`
match: { command: x }
positionals:
  - { index: 0, name: name }
transforms:
  - { key: ctrl+g, op: focus, slot: name }
  - { key: ctrl+o, op: focus, slot: nope }
`), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	errs := s.Validate()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `unknown slot "nope"`) {
		t.Fatalf("errors = %v, want only the unknown slot", errs)
	}
}

func TestValidateComplete(t *testing.T) {
	for _, tt := range []struct{ backend, err string }{
		{`""`, ""},
		{"auto", ""},
		{"cobra", ""},
		{"shell", ""},
		{"none", ""},
		{"[history, spec, provider, cobra, shell]", ""},
		{"[none]", ""},
		{"zsh", `unknown source "zsh" (use "shell")`},
		{"bogus", `unknown source "bogus"`},
		{"[cobra, bogus]", `unknown source "bogus"`},
		{"[auto, cobra]", `"auto" can't be combined`},
		{"[cobra, cobra]", `source "cobra" listed twice`},
		{"[]", `empty list of sources (use "none")`},
	} {
		s, err := Parse(fmt.Appendf(nil, "match: { command: x }\ncomplete: %s\n", tt.backend), "x.yaml")
		if err != nil {
			t.Fatal(err)
		}
		errs := s.Validate()
		switch {
		case tt.err == "" && len(errs) > 0:
			t.Errorf("complete %q: unexpected %v", tt.backend, errs)
		case tt.err != "" && (len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.err)):
			t.Errorf("complete %q: errors %v, want %q", tt.backend, errs, tt.err)
		}
	}
}

func TestEnabled(t *testing.T) {
	all := "cobra,history,provider,shell,spec"
	for _, tt := range []struct {
		spec, global string
		want         string
	}{
		{"", "", all},
		{"", "auto", all},
		{"", "shell", "shell"},
		{"", "none", ""},
		{"[cobra, history]", "shell", "cobra,history"},
		{"none", "auto", ""},
		{"auto", "shell", all},
	} {
		y := "match: { command: x }\n"
		if tt.spec != "" {
			y += "complete: " + tt.spec + "\n"
		}
		s, err := Parse([]byte(y), "x.yaml")
		if err != nil {
			t.Fatal(err)
		}
		var global Sources
		if tt.global != "" {
			if global, err = ParseSources(tt.global); err != nil {
				t.Fatal(err)
			}
		}
		var got []string
		for n := range s.Enabled(global) {
			got = append(got, n)
		}
		sort.Strings(got)
		if g := strings.Join(got, ","); g != tt.want {
			t.Errorf("spec %q, global %q: enabled = %q, want %q", tt.spec, tt.global, g, tt.want)
		}
	}
	// A nil spec uses the global sources.
	if got := (*Spec)(nil).Enabled(Sources{"cobra"}); len(got) != 1 || !got["cobra"] {
		t.Errorf("nil spec: enabled = %v", got)
	}
}

func TestParseSources(t *testing.T) {
	for _, tt := range []struct {
		in   any
		want string
		err  bool
	}{
		{"cobra", "cobra", false},
		{"history, shell", "history,shell", false},
		{"history shell", "history,shell", false},
		{[]any{"spec", "cobra"}, "spec,cobra", false},
		{[]string{"shell"}, "shell", false},
		{nil, "auto", false},
		{[]any{1}, "", true},
		{42, "", true},
	} {
		got, err := ParseSources(tt.in)
		if (err != nil) != tt.err || (!tt.err && got.String() != tt.want) {
			t.Errorf("ParseSources(%v) = %q, %v", tt.in, got, err)
		}
	}
}
