package rule

import (
	"strings"
	"testing"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/internal/transform"
	"github.com/amimof/rx/plugins"
)

// session is a command being edited, as the editor does: edits, each
// followed by Settle.
type session struct {
	t   *testing.T
	cmd *model.Command
	sp  *spec.Spec
	st  *State
	log []string
}

func open(t *testing.T, line string, extra ...string) *session {
	t.Helper()
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	for _, y := range extra {
		s, err := spec.Parse([]byte(y), "extra.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if errs := s.Validate(); len(errs) > 0 {
			t.Fatal(errs)
		}
		reg.Add(s)
	}
	c := spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
	cmd := c.Classify(parse.Parse(line), 0)
	sp := reg.Lookup(cmd.Name)
	cmd.AssignIDs()
	return &session{t: t, cmd: cmd, sp: sp, st: Init(cmd, sp)}
}

// edit runs f on the command and settles the rules.
func (s *session) edit(f func(c *model.Command)) Result {
	f(s.cmd)
	res := Settle(s.cmd, s.sp, s.st)
	s.log = res.Log
	return res
}

func (s *session) set(key, v string) {
	s.t.Helper()
	s.edit(func(c *model.Command) {
		if err := transform.Set(c, s.sp, key, v); err != nil {
			s.t.Fatal(err)
		}
	})
}

func (s *session) want(line string) {
	s.t.Helper()
	if got := s.cmd.Render(); got != line {
		s.t.Fatalf("command = %q, want %q (log %q)", got, line, s.log)
	}
}

func (s *session) wantLog(log string) {
	s.t.Helper()
	if got := strings.Join(s.log, "; "); got != log {
		s.t.Fatalf("log = %q, want %q", got, log)
	}
}

func (s *session) index(key string) int {
	s.t.Helper()
	i := s.cmd.Find(key)
	if i < 0 {
		s.t.Fatalf("no slot %q in %q", key, s.cmd.Render())
	}
	return s.cmd.Slots[i].Index
}

func TestLogsRoundTrip(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	s := open(t, line)
	s.set("subcommand", "logs")
	s.want("kubectl logs my-pod -n default")
	s.wantLog("logs-removes-resource: removed resource")
	if i := s.index("name"); i != 1 {
		t.Fatalf("name at position %d, want 1", i)
	}
	if got := s.st.Active(); len(got) != 1 {
		t.Fatalf("active = %v", got)
	}

	s.set("subcommand", "get")
	s.want(line)
	s.wantLog("reverted logs-removes-resource")
	if r, n := s.index("resource"), s.index("name"); r != 1 || n != 2 {
		t.Fatalf("resource at %d, name at %d", r, n)
	}
	if r := s.cmd.Slots[s.cmd.Find("resource")]; r.Dirty || r.Raw != "pods" {
		t.Fatalf("restored slot = %+v", r)
	}
	if len(s.st.Active()) != 0 {
		t.Fatalf("active = %v", s.st.Active())
	}
}

func TestLogsRemovesOutput(t *testing.T) {
	const line = "k get po my-pod -o wide --context=prod"
	s := open(t, line)
	s.set("subcommand", "logs")
	s.want("k logs my-pod --context=prod")
	s.wantLog("logs-removes-resource: removed resource, removed --output")
	// Any subcommand other than logs brings them back as written.
	s.set("subcommand", "describe")
	s.want("k describe po my-pod -o wide --context=prod")
}

func TestLogsKeepsRenamedPod(t *testing.T) {
	s := open(t, "kubectl get pods my-pod -n default")
	s.set("subcommand", "logs")
	s.set("name", "other-pod")
	s.wantLog("")
	s.set("subcommand", "get")
	s.want("kubectl get pods other-pod -n default")
}

func TestLogsKeepsResourceAddedBack(t *testing.T) {
	s := open(t, "kubectl get pods my-pod -n default")
	s.set("subcommand", "logs")
	// The user puts a resource back themselves; it's still a pod.
	s.edit(func(c *model.Command) {
		c.InsertPositional(model.Slot{Kind: model.Positional, Key: "resource", Label: "resource", Value: "po", Index: 1, Dirty: true})
	})
	s.want("kubectl logs po my-pod -n default")
	s.set("subcommand", "get")
	s.want("kubectl get po my-pod -n default")
	s.wantLog("reverted logs-removes-resource (kept resource)")
}

func TestLogsNotForOtherResources(t *testing.T) {
	for _, line := range []string{"k get svc web", "k get web"} {
		s := open(t, line)
		s.set("subcommand", "logs")
		s.wantLog("")
	}
}

func TestPostAddsBody(t *testing.T) {
	s := open(t, "curl https://x")
	s.set("request", "POST")
	s.want("curl https://x -X POST -d '{}'")
	s.wantLog("post-adds-body: added --data")
	// Still a method with a body: nothing changes.
	s.set("request", "PUT")
	s.want("curl https://x -X PUT -d '{}'")
	s.wantLog("")
	s.set("request", "DELETE")
	s.want("curl https://x -X DELETE")
	s.wantLog("reverted post-adds-body")
	// And again.
	s.set("request", "PATCH")
	s.want("curl https://x -X PATCH -d '{}'")
}

func TestPostKeepsEditedBody(t *testing.T) {
	s := open(t, "curl https://x")
	s.set("request", "POST")
	s.set("data", `{"a":1}`)
	s.set("request", "GET")
	s.want(`curl https://x -X GET -d '{"a":1}'`)
	s.wantLog("reverted post-adds-body (kept --data)")
}

func TestPostDoesntReAddDeletedBody(t *testing.T) {
	s := open(t, "curl https://x")
	s.set("request", "POST")
	s.edit(func(c *model.Command) { c.RemoveKey("data") })
	s.want("curl https://x -X POST")
	s.set("request", "PUT")
	s.want("curl https://x -X PUT")
	s.set("request", "GET")
	s.want("curl https://x -X GET")
}

func TestPostKeepsExistingBody(t *testing.T) {
	s := open(t, "curl https://x -d a=1")
	s.set("request", "POST")
	s.want("curl https://x -d a=1 -X POST")
	s.wantLog("post-adds-body: no change")
	// --json sends its own body.
	s = open(t, "curl https://x --json '{}'")
	s.set("request", "POST")
	s.wantLog("")
}

func TestHoldingAtOpenDoesntFire(t *testing.T) {
	s := open(t, "curl -X POST https://x")
	s.edit(func(*model.Command) {})
	s.want("curl -X POST https://x")
	if s.cmd.Changed {
		t.Fatal("command changed")
	}
	// It fires once it stops holding and holds again.
	s.set("request", "GET")
	s.set("request", "POST")
	s.want("curl -X POST https://x -d '{}'")
}

const toolSpec = `
match: { command: tool }
flags:
  - { name: a, takes: none }
  - { name: b, takes: none }
  - { name: env, short: e, takes: value }
  - { name: header, short: H, takes: value }
  - { name: force, short: f, takes: none }
positionals:
  - { index: 0, name: sub }
  - { index: 1, name: target }
  - { index: 2, name: extra }
values:
  sub: { remove: [rm, del] }
`

func TestRulesThatUndoEachOtherStop(t *testing.T) {
	s := open(t, "tool", toolSpec+`
rules:
  - { name: a-adds-b, when: { flag: { name: a, exists: true } }, then: { add: { flag: b } } }
  - { name: b-removes-a, when: { flag: { name: b, exists: true } }, then: { remove: { flag: a } } }
`)
	s.edit(func(c *model.Command) { c.Add(model.Slot{Kind: model.Bool, Key: "a", Name: "--a"}) })
	if last := s.log[len(s.log)-1]; last != "rules stopped after 10 rounds" {
		t.Fatalf("log = %q", s.log)
	}
}

func TestActions(t *testing.T) {
	for _, tt := range []struct {
		name, rules, line, edit, want string
		focus                         string
	}{
		{
			"add argument without value focuses it",
			`[{ name: r, when: { argument: { name: sub, equals: copy } }, then: { add: { argument: target } } }]`,
			"tool x", "copy", "tool copy", "target",
		},
		{
			"add argument shifts the later ones",
			`[{ name: r, when: { argument: { name: sub, equals: copy } }, then: { add: { argument: target, value: "{{.sub}}-dest" } } }]`,
			"tool x", "copy", "tool copy copy-dest", "",
		},
		{
			"add repeats a flag",
			`[{ name: r, when: { argument: { name: sub, equals: post } }, then: { add: { flag: header, value: "Accept: */*" } } }]`,
			"tool x -H a:b", "post", "tool post -H a:b -H 'Accept: */*'", "",
		},
		{
			"add a bool flag once",
			`[{ name: r, when: { argument: { name: sub, in: [rm, push] } }, then: [{ add: { flag: force } }, { add: { flag: a } }] }]`,
			"tool x -f", "del", "tool del -f --a", "",
		},
		{
			"set replaces a value",
			`[{ name: r, when: { argument: { name: sub, matches: "^prod-" } }, then: { set: { flag: env, value: prod } } }]`,
			"tool x -e dev", "prod-1", "tool prod-1 -e prod", "",
		},
		{
			"set adds a missing flag",
			`[{ name: r, when: { not: { argument: { name: sub, equals: x } } }, then: { set: { flag: env, value: "{{default \"dev\" .target}}" } } }]`,
			"tool x", "y", "tool y -e dev", "",
		},
		{
			"pos:N conditions",
			`[{ name: r, when: { argument: { name: "pos:3", exists: true } }, then: { remove: { argument: target } } }]`,
			"tool x a b c", "y", "tool y a b c", "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t, tt.line, toolSpec+"rules: "+tt.rules+"\n")
			res := s.edit(func(c *model.Command) { c.SetValue(c.Find("sub"), tt.edit) })
			s.want(tt.want)
			focus := ""
			if i := s.cmd.FindID(res.Focus); res.Focus != 0 && i >= 0 {
				focus = s.cmd.Slots[i].Key
			}
			if focus != tt.focus {
				t.Errorf("focus = %q, want %q", focus, tt.focus)
			}
			// Reverting gives the edited command back.
			s.edit(func(c *model.Command) { c.SetValue(c.Find("sub"), "x") })
			s.want(strings.Replace(tt.line, strings.Fields(tt.line)[1], "x", 1))
		})
	}
}

func TestHolds(t *testing.T) {
	s := open(t, "tool rm web-1 -f -e a -e b", toolSpec)
	for _, tt := range []struct {
		cond string
		ok   bool
	}{
		{"{ flag: { name: force, exists: true } }", true},
		{"{ flag: { name: a, exists: false } }", true},
		{"{ argument: { name: sub, equals: del } }", true},
		{"{ argument: { name: sub, in: [add, remove] } }", true},
		{"{ argument: { name: target, matches: '^web-' } }", true},
		{"{ argument: { name: target, matches: '^db-' } }", false},
		// Any value of a repeated flag.
		{"{ flag: { name: env, equals: b } }", true},
		{"{ not: { flag: { name: env, equals: b } } }", false},
		{"{ argument: { name: extra, exists: true } }", false},
		{"{ argument: { name: 'pos:1', exists: true } }", false},
	} {
		sp, err := spec.Parse([]byte(toolSpec+"rules: [{ name: r, when: "+tt.cond+", then: { remove: { flag: a } } }]\n"), "x.yaml")
		if err != nil {
			t.Fatalf("%s: %v", tt.cond, err)
		}
		if ok, why := Holds(s.cmd, sp, &sp.Rules[0]); ok != tt.ok {
			t.Errorf("%s: holds = %v (%s), want %v", tt.cond, ok, why, tt.ok)
		}
	}
}
