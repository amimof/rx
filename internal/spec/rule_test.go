package spec

import (
	"strings"
	"testing"
)

const ruleBase = `
match: { command: curl }
flags:
  - { name: request, short: X, takes: value }
  - { name: data, short: d, takes: value }
  - { name: silent, short: s, takes: none }
positionals:
  - { index: 0, name: url }
`

func TestRuleParse(t *testing.T) {
	s, err := Parse([]byte(ruleBase+`
values:
  request: { POST: [post] }
rules:
  - name: post-adds-body
    when:
      flag: { name: request, equals: POST }
    then:
      add: { flag: data, value: "{}", unless_present: true }
  - name: lists
    when:
      - argument: { name: url, matches: "^https://" }
      - not: { flag: { name: silent, exists: true } }
    then:
      - remove: { flag: data }
      - set: { flag: request, value: "{{.url}}" }
`), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if errs := s.Validate(); len(errs) > 0 {
		t.Fatalf("unexpected %v", errs)
	}
	r := s.Rule("post-adds-body")
	if r == nil || len(r.When) != 1 || len(r.Then) != 1 {
		t.Fatalf("rule = %+v", r)
	}
	if op, c := r.Then[0].Op(); op != "add" || c.Flag != "data" || c.Value != "{}" || !c.UnlessPresent {
		t.Fatalf("action = %s %+v", op, c)
	}
	l := s.Rule("lists")
	if len(l.When) != 2 || len(l.Then) != 2 {
		t.Fatalf("lists = %+v", l)
	}
	var conds []string
	for _, c := range append(r.When, l.When...) {
		conds = append(conds, c.String())
	}
	if got := strings.Join(conds, ", "); got != "--request=POST, url~^https://, not --silent" {
		t.Fatalf("conditions = %s", got)
	}
	if s.Canonical("request", "post") != "POST" {
		t.Error("post should be a spelling of POST")
	}
}

func TestRuleUnknownField(t *testing.T) {
	for _, y := range []string{
		"rules: [{ name: r, when: { flag: { name: data, exist: true } }, then: { remove: { flag: data } } }]",
		"rules: [{ name: r, when: { flag: { name: data, exists: true } }, then: { remove: { flag: data, unless: true } } }]",
		"rules: [{ name: r, when: [{ flag: { name: data, exists: true }, typo: 1 }], then: { remove: { flag: data } } }]",
	} {
		if _, err := Parse([]byte(ruleBase+y+"\n"), "x.yaml"); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s: err = %v", y, err)
		}
	}
}

func TestRuleValidate(t *testing.T) {
	const when = "when: { flag: { name: request, exists: true } }"
	const then = "then: { remove: { flag: data } }"
	for _, tt := range []struct{ rules, err string }{
		{"[{ name: a, " + when + ", " + then + " }]", ""},
		{"[{ " + when + ", " + then + " }]", "rule #1 has no name"},
		{"[{ name: a, " + when + ", " + then + " }, { name: a, " + when + ", " + then + " }]", `rule "a" defined twice`},
		{"[{ name: a, " + then + " }]", "when is required"},
		{"[{ name: a, " + when + " }]", "then is required"},
		{"[{ name: a, when: {}, " + then + " }]", "exactly one of flag, argument or not"},
		{"[{ name: a, when: { flag: { name: request, exists: true }, argument: { name: url, exists: true } }, " + then + " }]", "exactly one of flag, argument or not"},
		{"[{ name: a, when: { flag: { name: nope, exists: true } }, " + then + " }]", `unknown flag "nope"`},
		{"[{ name: a, when: { argument: { name: nope, exists: true } }, " + then + " }]", `unknown argument "nope"`},
		{"[{ name: a, when: { argument: { name: 'pos:3', exists: true } }, " + then + " }]", ""},
		{"[{ name: a, when: { flag: { exists: true } }, " + then + " }]", "condition without name"},
		{"[{ name: a, when: { flag: { name: request } }, " + then + " }]", "exactly one of exists, equals, in or matches"},
		{"[{ name: a, when: { flag: { name: request, equals: x, in: [y] } }, " + then + " }]", "exactly one of exists, equals, in or matches"},
		{"[{ name: a, when: { flag: { name: request, matches: '(' } }, " + then + " }]", "missing closing )"},
		{"[{ name: a, when: { not: { flag: { name: nope, exists: true } } }, " + then + " }]", `unknown flag "nope"`},
		{"[{ name: a, " + when + ", then: {} }]", "exactly one of add, remove or set"},
		{"[{ name: a, " + when + ", then: { add: { flag: data }, remove: { flag: data } } }]", "use a list for more"},
		{"[{ name: a, " + when + ", then: { add: {} } }]", "flag or argument is required"},
		{"[{ name: a, " + when + ", then: { add: { flag: data, argument: url } } }]", "can't be combined"},
		{"[{ name: a, " + when + ", then: { add: { flag: nope } } }]", `add: unknown flag "nope"`},
		{"[{ name: a, " + when + ", then: { remove: { argument: nope } } }]", `remove: unknown argument "nope"`},
		{"[{ name: a, " + when + ", then: { remove: { flag: data, value: x } } }]", "takes no value or unless_present"},
		{"[{ name: a, " + when + ", then: { set: { flag: data } } }]", "value is required"},
		{"[{ name: a, " + when + ", then: { set: { flag: data, value: x, unless_present: true } } }]", "only for add"},
		{"[{ name: a, " + when + ", then: { add: { flag: silent, value: x } } }]", "takes no value"},
		{"[{ name: a, " + when + ", then: { add: { flag: data, value: '{{.nope}}' } } }]", `unknown slot "nope"`},
		{"[{ name: a, " + when + ", then: { add: { flag: data, value: '{{default .request .url}}' } } }]", ""},
		{"[{ name: a, " + when + ", then: { add: { flag: data, value: '{{.url' } } }]", "unclosed action"},
	} {
		s, err := Parse([]byte(ruleBase+"rules: "+tt.rules+"\n"), "x.yaml")
		if err != nil {
			t.Fatalf("%s: %v", tt.rules, err)
		}
		errs := s.Validate()
		switch {
		case tt.err == "" && len(errs) > 0:
			t.Errorf("%s: unexpected %v", tt.rules, errs)
		case tt.err != "" && (len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.err)):
			t.Errorf("%s: errors %v, want %q", tt.rules, errs, tt.err)
		}
	}
}

func TestValuesValidate(t *testing.T) {
	for _, tt := range []struct{ values, err string }{
		{`{ request: { POST: [post] } }`, ""},
		{`{ nope: { pod: [po] } }`, `values: unknown slot "nope"`},
		{`{ request: { POST: [p], PUT: [p] } }`, `"p" is a spelling of both`},
	} {
		s, err := Parse([]byte(ruleBase+"values: "+tt.values+"\n"), "x.yaml")
		if err != nil {
			t.Fatal(err)
		}
		errs := s.Validate()
		switch {
		case tt.err == "" && len(errs) > 0:
			t.Errorf("%s: unexpected %v", tt.values, errs)
		case tt.err != "" && (len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.err)):
			t.Errorf("%s: errors %v, want %q", tt.values, errs, tt.err)
		}
	}
}
