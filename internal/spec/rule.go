package spec

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"

	"gopkg.in/yaml.v3"
)

// Rule is a conditional transformation: when every condition in When
// starts to hold because of an edit, the actions in Then run. When the
// conditions stop holding, the actions are reverted.
type Rule struct {
	Name string  `yaml:"name"`
	Desc string  `yaml:"desc"`
	When Conds   `yaml:"when"`
	Then Actions `yaml:"then"`
}

// Cond is a condition: a check on a flag or a positional argument, or the
// negation of another condition.
type Cond struct {
	Flag     *Check `yaml:"flag"`
	Argument *Check `yaml:"argument"`
	Not      *Cond  `yaml:"not"`
}

// Check tests one slot, by the positional's name or the flag's long name,
// with exactly one of Exists, Equals, In or Matches.
type Check struct {
	Name    string   `yaml:"name"`
	Exists  *bool    `yaml:"exists"`
	Equals  string   `yaml:"equals"`
	In      []string `yaml:"in"`
	Matches string   `yaml:"matches"`
}

// Conds is a list of conditions that unmarshals from either a single
// condition or a list.
type Conds []Cond

func (c *Conds) UnmarshalYAML(n *yaml.Node) error { return oneOrMany(n, (*[]Cond)(c)) }

// Actions is a list of actions that unmarshals from either a single action
// or a list.
type Actions []Action

func (a *Actions) UnmarshalYAML(n *yaml.Node) error { return oneOrMany(n, (*[]Action)(a)) }

// oneOrMany decodes n, a single item or a list of them, into out. Unknown
// fields are errors, as in the rest of the plugin.
func oneOrMany[T any](n *yaml.Node, out *[]T) error {
	if n.Kind != yaml.SequenceNode {
		var one T
		if err := strictDecode(n, &one); err != nil {
			return err
		}
		*out = []T{one}
		return nil
	}
	return strictDecode(n, out)
}

// strictDecode decodes n into out, rejecting unknown fields, which
// yaml.Node.Decode doesn't.
func strictDecode(n *yaml.Node, out any) error {
	data, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	return nil
}

// Action changes one slot with exactly one of Add, Remove or Set.
type Action struct {
	Add    *Change `yaml:"add"`
	Remove *Change `yaml:"remove"`
	Set    *Change `yaml:"set"`
}

// Op returns the action's operation ("add", "remove" or "set") and its
// change.
func (a Action) Op() (string, *Change) {
	switch {
	case a.Add != nil:
		return "add", a.Add
	case a.Remove != nil:
		return "remove", a.Remove
	case a.Set != nil:
		return "set", a.Set
	}
	return "", nil
}

// Change names the slot an action changes, a flag by long name or a
// positional by name, and the value it gets. Value is a Go template over
// the command's slot values by key, e.g. {{.name}}, as in providers.
type Change struct {
	Flag     string `yaml:"flag"`
	Argument string `yaml:"argument"`
	Value    string `yaml:"value"`
	// UnlessPresent makes add leave a slot that already has a value alone.
	UnlessPresent bool `yaml:"unless_present"`
}

// Key returns the slot key the change acts on and whether it is a flag.
func (c Change) Key() (string, bool) {
	if c.Flag != "" {
		return c.Flag, true
	}
	return c.Argument, false
}

// Label returns the slot as shown to the user: "--data" or "resource".
func (c Change) Label() string {
	if c.Flag != "" {
		return "--" + c.Flag
	}
	return c.Argument
}

// Check returns the condition's check and whether it tests a flag. It is
// nil for a Not condition.
func (c Cond) Check() (*Check, bool) {
	if c.Flag != nil {
		return c.Flag, true
	}
	return c.Argument, false
}

// String describes what the condition requires, e.g. "subcommand=logs",
// "--request=POST|PUT" or "not --watch".
func (c Cond) String() string {
	if c.Not != nil {
		return "not " + c.Not.String()
	}
	ch, flag := c.Check()
	if ch == nil {
		return ""
	}
	k := ch.Name
	if flag {
		k = "--" + k
	}
	switch {
	case ch.Exists != nil && !*ch.Exists:
		return "no " + k
	case ch.Equals != "":
		return k + "=" + ch.Equals
	case len(ch.In) > 0:
		return k + "=" + strings.Join(ch.In, "|")
	case ch.Matches != "":
		return k + "~" + ch.Matches
	}
	return k
}

// ValueAliases maps a slot key to its canonical values and the spellings of
// each, e.g. resource: {pod: [po, pods]}.
type ValueAliases map[string]map[string][]string

// Canonical returns the canonical spelling of value v for slot key.
func (s *Spec) Canonical(key, v string) string {
	if s == nil {
		return v
	}
	for c, alts := range s.Values[key] {
		if v == c || slices.Contains(alts, v) {
			return c
		}
	}
	return v
}

// Rule returns the rule with the given name, or nil.
func (s *Spec) Rule(name string) *Rule {
	if s == nil {
		return nil
	}
	for i := range s.Rules {
		if s.Rules[i].Name == name {
			return &s.Rules[i]
		}
	}
	return nil
}

// TemplateFuncs are the functions available in rule value templates.
var TemplateFuncs = template.FuncMap{
	"default": func(def string, v any) string {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
		return def
	},
}

// ParseTemplate parses a rule value template.
func ParseTemplate(text string) (*template.Template, error) {
	return template.New("value").Option("missingkey=zero").Funcs(TemplateFuncs).Parse(text)
}

// slotKeys returns the slot keys the spec declares: positional and flag
// names.
func (s *Spec) slotKeys() map[string]bool {
	keys := map[string]bool{}
	for _, p := range s.Positionals {
		keys[p.Name] = true
	}
	for _, f := range s.Flags {
		keys[f.Name] = true
	}
	return keys
}

// validateValues reports problems with the value aliases.
func (s *Spec) validateValues(add func(string, ...any)) {
	keys := s.slotKeys()
	for k, canon := range s.Values {
		if !keys[k] {
			add("values: unknown slot %q", k)
		}
		seen := map[string]string{}
		for c, alts := range canon {
			for _, v := range append([]string{c}, alts...) {
				if o, ok := seen[v]; ok && o != c {
					add("values.%s: %q is a spelling of both %q and %q", k, v, o, c)
				}
				seen[v] = c
			}
		}
	}
}

// validateRules reports problems with the rules.
func (s *Spec) validateRules(add func(string, ...any)) {
	names := map[string]bool{}
	for i, r := range s.Rules {
		id := r.Name
		switch {
		case r.Name == "":
			id = fmt.Sprintf("#%d", i+1)
			add("rule %s has no name", id)
		case names[r.Name]:
			add("rule %q defined twice", r.Name)
		}
		names[r.Name] = true
		radd := func(f string, a ...any) { add("rule %q: "+f, append([]any{id}, a...)...) }
		if len(r.When) == 0 {
			radd("when is required")
		}
		for _, c := range r.When {
			s.validateCond(c, radd)
		}
		if len(r.Then) == 0 {
			radd("then is required")
		}
		for _, a := range r.Then {
			s.validateAction(a, radd)
		}
	}
}

// knownSlot reports whether the spec declares the flag or positional.
func (s *Spec) knownSlot(key string, flag bool) bool {
	if flag {
		return s.Flag("--"+key) != nil
	}
	if s.PositionalByName(key) != nil {
		return true
	}
	var idx int
	_, err := fmt.Sscanf(key, "pos:%d", &idx)
	return err == nil
}

func (s *Spec) validateCond(c Cond, add func(string, ...any)) {
	n := 0
	for _, set := range []bool{c.Flag != nil, c.Argument != nil, c.Not != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		add("a condition needs exactly one of flag, argument or not")
		return
	}
	if c.Not != nil {
		s.validateCond(*c.Not, add)
		return
	}
	ch, flag := c.Check()
	switch {
	case ch.Name == "":
		add("condition without name")
		return
	case !s.knownSlot(ch.Name, flag) && flag:
		add("unknown flag %q", ch.Name)
	case !s.knownSlot(ch.Name, flag):
		add("unknown argument %q", ch.Name)
	}
	n = 0
	for _, set := range []bool{ch.Exists != nil, ch.Equals != "", ch.In != nil, ch.Matches != ""} {
		if set {
			n++
		}
	}
	if n != 1 {
		add("%q needs exactly one of exists, equals, in or matches", ch.Name)
	}
	if ch.Matches != "" {
		if _, err := regexp.Compile(ch.Matches); err != nil {
			add("%q: %v", ch.Name, err)
		}
	}
}

func (s *Spec) validateAction(a Action, add func(string, ...any)) {
	n := 0
	for _, set := range []bool{a.Add != nil, a.Remove != nil, a.Set != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		add("an action needs exactly one of add, remove or set (use a list for more)")
		return
	}
	op, c := a.Op()
	key, flag := c.Key()
	switch {
	case c.Flag != "" && c.Argument != "":
		add("%s: flag and argument can't be combined", op)
		return
	case key == "":
		add("%s: flag or argument is required", op)
		return
	case !s.knownSlot(key, flag) && flag:
		add("%s: unknown flag %q", op, key)
	case !s.knownSlot(key, flag):
		add("%s: unknown argument %q", op, key)
	}
	switch op {
	case "remove":
		if c.Value != "" || c.UnlessPresent {
			add("remove %s: takes no value or unless_present", c.Label())
		}
	case "set":
		if c.Value == "" {
			add("set %s: value is required", c.Label())
		}
		if c.UnlessPresent {
			add("set %s: unless_present is only for add", c.Label())
		}
	case "add":
		if f := s.Flag("--" + key); flag && f != nil && f.Takes == "none" && c.Value != "" {
			add("add %s: the flag takes no value", c.Label())
		}
	}
	if c.Value == "" {
		return
	}
	t, err := ParseTemplate(c.Value)
	if err != nil {
		add("%s %s: %v", op, c.Label(), err)
		return
	}
	keys := s.slotKeys()
	for _, f := range templateFields(t.Tree) {
		if !keys[f] {
			add("%s %s: value %q refers to unknown slot %q", op, c.Label(), c.Value, f)
		}
	}
}

// templateFields returns the top-level field names a template refers to,
// e.g. "name" for {{.name}} and {{default "x" .name}}.
func templateFields(t *parse.Tree) []string {
	var out []string
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walk(n.Pipe)
		case *parse.PipeNode:
			if n == nil {
				return
			}
			for _, c := range n.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			for _, a := range n.Args {
				walk(a)
			}
		case *parse.FieldNode:
			out = append(out, n.Ident[0])
		case *parse.ChainNode:
			walk(n.Node)
		case *parse.IfNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			// Fields inside the body refer to the new dot.
			walk(n.Pipe)
			walk(n.ElseList)
		case *parse.RangeNode:
			walk(n.Pipe)
			walk(n.ElseList)
		}
	}
	if t != nil {
		walk(t.Root)
	}
	return out
}
