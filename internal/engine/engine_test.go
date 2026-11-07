package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/complete/provider"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/shell/proto"
	"github.com/amimof/rx/internal/spec"
)

const toolSpec = `
name: tool
match: { command: tool }
complete: none
flags:
  - { name: env, short: e, takes: value, provider: envs }
  - { name: region, takes: value, provider: regions }
providers:
  envs:    { exec: "printf 'env/dev\tDevelopment\nenv/prod\n'", strip: "env/", ttl: 1h }
  regions: { exec: "echo {{.env | default \"none\"}}-{{.missing}}" }
`

func setup(t *testing.T, line string) (*model.Command, *spec.Spec) {
	t.Helper()
	sp, err := spec.Parse([]byte(toolSpec), "tool.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if errs := sp.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	reg := spec.NewRegistry()
	reg.Add(sp)
	c := spec.Classifier{Specs: reg}
	return c.Classify(parse.Parse(line), 0), sp
}

func collect(t *testing.T, e *Engine, cmd *model.Command, sp *spec.Spec, focus int) []Update {
	t.Helper()
	ch := make(chan Update, 10)
	e.Start(context.Background(), 7, cmd, sp, focus, func(u Update) { ch <- u })
	var out []Update
	for {
		select {
		case u := <-ch:
			if u.ID != 7 {
				t.Fatalf("id = %d", u.ID)
			}
			out = append(out, u)
			if u.Done {
				return out
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out")
		}
	}
}

func TestProviderRender(t *testing.T) {
	cmd, sp := setup(t, "tool --env prod --region x")
	s, err := provider.Render(sp.Providers["regions"], cmd)
	if err != nil || s != "echo prod-" {
		t.Fatalf("render = %q, %v", s, err)
	}
}

func TestEngineProviderAndCache(t *testing.T) {
	cmd, sp := setup(t, "tool -e dev --region x")
	dir := t.TempDir()
	e := New(nil, &complete.Cache{Dir: dir})

	ups := collect(t, e, cmd, sp, cmd.Find("env"))
	last := ups[len(ups)-1]
	if last.Err != nil {
		t.Fatal(last.Err)
	}
	c := last.Result.Candidates
	if len(c) != 2 || c[0].Value != "dev" || c[0].Desc != "Development" || c[1].Value != "prod" {
		t.Fatalf("candidates = %+v", c)
	}

	// Second run is served from cache (fresh, ttl 1h) in a single update.
	ups = collect(t, e, cmd, sp, cmd.Find("env"))
	if len(ups) != 1 || len(ups[0].Result.Candidates) != 2 || ups[0].Stale {
		t.Fatalf("cached updates = %+v", ups)
	}

	// Providers without a ttl use the engine's. Once expired, the cached
	// result is served as stale first and then refreshed.
	e.TTL = time.Nanosecond
	collect(t, e, cmd, sp, cmd.Find("region"))
	ups = collect(t, e, cmd, sp, cmd.Find("region"))
	if len(ups) != 2 || !ups[0].Stale || ups[0].Done || ups[1].Stale || !ups[1].Done {
		t.Fatalf("stale-while-revalidate updates = %+v", ups)
	}
	if v := ups[1].Result.Candidates[0].Value; v != "dev-" {
		t.Fatalf("region = %q", v)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(files) != 2 {
		t.Fatalf("cache files = %v", files)
	}
}

// Start must never call send before returning. The editor calls Start from
// its event loop and send hands the update to that same loop, so a
// synchronous send deadlocks it.
func TestStartNeverSendsSynchronously(t *testing.T) {
	cmd, sp := setup(t, "tool --verbose arg -e dev")
	// A template that fails to execute, for the provider error path.
	sp.Providers["envs"] = spec.Provider{Exec: "{{template \"missing\"}}"}
	e := New(nil, nil)
	for i := range cmd.Slots {
		ch := make(chan Update) // unbuffered, like tea.Program.Send
		returned := make(chan struct{})
		go func() {
			e.Start(context.Background(), 1, cmd, sp, i, func(u Update) { ch <- u })
			close(returned)
		}()
		select {
		case <-returned:
		case <-time.After(2 * time.Second):
			t.Fatalf("slot %d (%s): Start blocked on send", i, cmd.Slots[i].Key)
		}
		select {
		case u := <-ch:
			if !u.Done {
				// Drain until done so the goroutine exits.
				for !u.Done {
					u = <-ch
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("slot %d: no update", i)
		}
	}
}

// fakeShell answers completions with the request line, and INFO with a
// non-cobra completion function.
type fakeShell struct{ infos int }

func (f *fakeShell) Name() string { return "fakesh" }

func (f *fakeShell) Complete(_ context.Context, req complete.Request) (complete.Result, error) {
	return complete.Result{Candidates: []complete.Candidate{{Value: req.Line, Source: "fakesh"}}}, nil
}

func (f *fakeShell) Info(context.Context, string) (proto.Info, error) {
	f.infos++
	return proto.Info{CompFunc: "_other"}, nil
}

func TestEngineShell(t *testing.T) {
	for _, backend := range []string{"shell", "auto"} {
		sp, err := spec.Parse([]byte("match: { command: other }\ncomplete: "+backend+"\n"), "other.yaml")
		if err != nil {
			t.Fatal(err)
		}
		reg := spec.NewRegistry()
		reg.Add(sp)
		cmd := (&spec.Classifier{Specs: reg}).Classify(parse.Parse("other a b"), 0)
		sh := &fakeShell{}
		e := New(sh, nil)
		ups := collect(t, e, cmd, sp, cmd.Find("pos:1"))
		last := ups[len(ups)-1]
		if last.Source != "fakesh" || len(last.Result.Candidates) != 1 || last.Result.Candidates[0].Value != "other a " {
			t.Errorf("%s: updates = %+v", backend, ups)
		}
		if want := map[string]int{"shell": 0, "auto": 1}[backend]; sh.infos != want {
			t.Errorf("%s: %d INFO requests, want %d", backend, sh.infos, want)
		}
	}
}

func TestSources(t *testing.T) {
	cmd, sp := setup(t, "tool --verbose -e dev arg")
	sh := &fakeShell{}
	e := New(sh, nil)
	for _, tt := range []struct {
		key  string
		want string
	}{
		{"env", "provider:envs"}, // complete: none hides cobra and the shell
		{"verbose", ""},
		{"pos:0", ""},
	} {
		if got := strings.Join(e.Sources(cmd, sp, cmd.Find(tt.key)), ","); got != tt.want {
			t.Errorf("%s: sources = %q, want %q", tt.key, got, tt.want)
		}
	}

	for _, tt := range []struct {
		backend string
		sh      Shell
		want    string
	}{
		{"cobra", sh, "cobra,fakesh"},
		{"cobra", nil, "cobra"},
		// Auto with no cached INFO saying cobra: the shell only.
		{"auto", sh, "fakesh"},
		{"shell", sh, "fakesh"},
	} {
		osp, err := spec.Parse([]byte("match: { command: other }\ncomplete: "+tt.backend+"\n"), "other.yaml")
		if err != nil {
			t.Fatal(err)
		}
		reg := spec.NewRegistry()
		reg.Add(osp)
		ocmd := (&spec.Classifier{Specs: reg}).Classify(parse.Parse("other a"), 0)
		if got := strings.Join(New(tt.sh, nil).Sources(ocmd, osp, 0), ","); got != tt.want {
			t.Errorf("%s: sources = %q, want %q", tt.backend, got, tt.want)
		}
	}
}

// A forced source overrides the spec's backend.
func TestStartFrom(t *testing.T) {
	sp, err := spec.Parse([]byte("match: { command: other }\ncomplete: cobra\n"), "other.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := spec.NewRegistry()
	reg.Add(sp)
	cmd := (&spec.Classifier{Specs: reg}).Classify(parse.Parse("other a b"), 0)
	e := New(&fakeShell{}, nil)
	ch := make(chan Update, 10)
	e.StartFrom(context.Background(), 1, cmd, sp, cmd.Find("pos:1"), "fakesh", func(u Update) { ch <- u })
	select {
	case u := <-ch:
		if u.Source != "fakesh" || len(u.Result.Candidates) != 1 {
			t.Fatalf("update = %+v", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestEngineNoneAndBool(t *testing.T) {
	cmd, sp := setup(t, "tool --verbose arg")
	e := New(nil, nil)
	// Unknown command without zsh: nothing to run, but Done is delivered.
	for i := range cmd.Slots {
		ups := collect(t, e, cmd, sp, i)
		if len(ups) != 1 || !ups[0].Done || len(ups[0].Result.Candidates) != 0 {
			t.Fatalf("slot %d updates = %+v", i, ups)
		}
	}
}
