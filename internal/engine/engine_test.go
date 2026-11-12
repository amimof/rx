package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/complete/cli"
	"github.com/amimof/rx/internal/complete/provider"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/shell/proto"
	"github.com/amimof/rx/internal/spec"
)

const toolSpec = `
name: tool
match: { command: tool }
complete: [history, spec, provider]
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
// completion function containing marker, if it is one of those requested.
type fakeShell struct {
	infos  int
	marker string
}

func (f *fakeShell) Name() string { return "fakesh" }

func (f *fakeShell) Complete(_ context.Context, req complete.Request) (complete.Result, error) {
	return complete.Result{Candidates: []complete.Candidate{{Value: req.Line, Source: "fakesh"}}}, nil
}

func (f *fakeShell) Info(_ context.Context, _ string, markers ...string) (proto.Info, error) {
	f.infos++
	info := proto.Info{CompFunc: "_other"}
	if slices.Contains(markers, f.marker) {
		info.Marker = f.marker
	}
	return info, nil
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
		{"env", "provider:envs"}, // cli and the shell aren't enabled
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
		// A pinned protocol is offered with or without a shell.
		{"cli\ncli: cobra", sh, "cli"},
		{"cli\ncli: clap", nil, "cli"},
		{"auto\ncli: clap", sh, "cli,fakesh"},
		// No pin and no cached INFO naming a protocol: no cli.
		{"cli", sh, ""},
		{"[cli, shell]", sh, "fakesh"},
		{"auto", sh, "fakesh"},
		{"shell", sh, "fakesh"},
		{"[shell]", nil, ""},
		{"none", sh, ""},
		{"[history, spec]\ncli: cobra", sh, ""},
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

// A forced source overrides the backend the engine would pick, but only if
// it is enabled.
func TestStartFrom(t *testing.T) {
	for _, tt := range []struct {
		complete string
		want     int
	}{
		{"[cli, shell]", 1},
		{"cli", 0},
	} {
		sp, err := spec.Parse([]byte("match: { command: other }\ncomplete: "+tt.complete+"\n"), "other.yaml")
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
			if !u.Done || len(u.Result.Candidates) != tt.want || (tt.want > 0 && u.Source != "fakesh") {
				t.Fatalf("%s: update = %+v", tt.complete, u)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out")
		}
	}
}

// The engine's default applies to commands whose spec doesn't set sources.
func TestEngineDefaultSources(t *testing.T) {
	cmd := (&spec.Classifier{Specs: spec.NewRegistry()}).Classify(parse.Parse("other a b"), 0)
	sh := &fakeShell{}
	e := New(sh, nil)
	e.Complete = spec.Sources{spec.SourceHistory}
	if got := e.Sources(cmd, nil, cmd.Find("pos:1")); len(got) != 0 {
		t.Errorf("sources = %q, want none", got)
	}
	ups := collect(t, e, cmd, nil, cmd.Find("pos:1"))
	if len(ups) != 1 || len(ups[0].Result.Candidates) != 0 || sh.infos != 0 {
		t.Errorf("updates = %+v, %d INFO requests", ups, sh.infos)
	}

	// A spec's own sources replace the default.
	sp, err := spec.Parse([]byte("match: { command: other }\ncomplete: shell\n"), "other.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.Sources(cmd, sp, cmd.Find("pos:1")), ","); got != "fakesh" {
		t.Errorf("spec sources = %q, want fakesh", got)
	}
}

// A disabled provider is neither offered nor run.
func TestProviderDisabled(t *testing.T) {
	cmd, sp := setup(t, "tool -e dev")
	sp.Complete = spec.Sources{spec.SourceShell}
	e := New(&fakeShell{}, nil)
	if got := strings.Join(e.Sources(cmd, sp, cmd.Find("env")), ","); got != "fakesh" {
		t.Errorf("sources = %q, want fakesh", got)
	}
	ups := collect(t, e, cmd, sp, cmd.Find("env"))
	if last := ups[len(ups)-1]; last.Source != "fakesh" {
		t.Errorf("updates = %+v", ups)
	}
}

// fakeCLI puts an "other" command on PATH that speaks both the cobra and
// the clap protocol, answering cobra-val or clap-val, and appends each run
// to a log so tests can check it wasn't run. It must be called before New,
// which snapshots the environment.
func fakeCLI(t *testing.T, script string) (logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "runs")
	if script == "" {
		script = `
echo "$*" >> "$RX_TEST_LOG"
if [ "$COMPLETE" = fish ]; then printf 'clap-val\tfrom clap\n'; exit 0; fi
if [ "$1" = __complete ]; then printf 'cobra-val\n:4\n'; exit 0; fi
exit 1
`
	}
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RX_TEST_LOG", logFile)
	return logFile
}

func otherCmd(t *testing.T, yaml string) (*model.Command, *spec.Spec) {
	t.Helper()
	reg := spec.NewRegistry()
	var sp *spec.Spec
	if yaml != "" {
		var err error
		if sp, err = spec.Parse([]byte("match: { command: other }\n"+yaml), "other.yaml"); err != nil {
			t.Fatal(err)
		}
		if errs := sp.Validate(); len(errs) > 0 {
			t.Fatal(errs)
		}
		reg.Add(sp)
	}
	return (&spec.Classifier{Specs: reg}).Classify(parse.Parse("other a b"), 0), sp
}

// The shell's completion function picks the protocol.
func TestCLIDetect(t *testing.T) {
	fakeCLI(t, "")
	for _, tt := range []struct{ marker, want string }{
		{"__complete", "cobra-val"},
		{"_CLAP_COMPLETE_INDEX", "clap-val"},
	} {
		cmd, sp := otherCmd(t, "")
		sh := &fakeShell{marker: tt.marker}
		e := New(sh, nil)
		ups := collect(t, e, cmd, sp, cmd.Find("pos:1"))
		last := ups[len(ups)-1]
		if last.Err != nil || last.Source != "cli" || len(last.Result.Candidates) != 1 || last.Result.Candidates[0].Value != tt.want {
			t.Fatalf("%s: updates = %+v", tt.marker, ups)
		}
		// Once INFO is cached, cli is offered without asking again.
		if got := strings.Join(e.Sources(cmd, sp, cmd.Find("pos:1")), ","); got != "cli,fakesh" || sh.infos != 1 {
			t.Errorf("%s: sources = %q after %d INFO requests", tt.marker, got, sh.infos)
		}
	}
}

// Commands whose completion function names no protocol are never run.
func TestCLIUnknownNotRun(t *testing.T) {
	runs := fakeCLI(t, "")
	cmd, sp := otherCmd(t, "")
	e := New(&fakeShell{}, nil)
	ups := collect(t, e, cmd, sp, cmd.Find("pos:1"))
	if last := ups[len(ups)-1]; last.Source != "fakesh" {
		t.Errorf("updates = %+v", ups)
	}
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Errorf("command was run: %v", err)
	}
}

// A pinned protocol works without a shell, and isn't detected.
func TestCLIPinned(t *testing.T) {
	fakeCLI(t, "")
	cmd, sp := otherCmd(t, "complete: [cli]\ncli: clap\n")
	e := New(nil, nil)
	if got := strings.Join(e.Sources(cmd, sp, cmd.Find("pos:1")), ","); got != "cli" {
		t.Errorf("sources = %q", got)
	}
	ups := collect(t, e, cmd, sp, cmd.Find("pos:1"))
	if last := ups[len(ups)-1]; last.Err != nil || len(last.Result.Candidates) != 1 || last.Result.Candidates[0].Desc != "from clap" {
		t.Fatalf("updates = %+v", ups)
	}

	sh := &fakeShell{marker: "__complete"}
	e = New(sh, nil)
	ups = collect(t, e, cmd, sp, cmd.Find("pos:1"))
	if last := ups[len(ups)-1]; len(last.Result.Candidates) != 1 || last.Result.Candidates[0].Value != "clap-val" || sh.infos != 0 {
		t.Fatalf("pinned with shell: updates = %+v, %d INFO requests", ups, sh.infos)
	}
}

// A command that turns out not to speak its protocol falls back to the
// shell for the rest of the session.
func TestCLIUnsupported(t *testing.T) {
	fakeCLI(t, "echo usage: other\n")
	cmd, sp := otherCmd(t, "cli: cobra\n")
	e := New(&fakeShell{}, nil)
	ups := collect(t, e, cmd, sp, cmd.Find("pos:1"))
	if last := ups[len(ups)-1]; !errors.Is(last.Err, cli.ErrUnsupported) {
		t.Fatalf("updates = %+v", ups)
	}
	if got := strings.Join(e.Sources(cmd, sp, cmd.Find("pos:1")), ","); got != "fakesh" {
		t.Errorf("sources = %q", got)
	}
	ups = collect(t, e, cmd, sp, cmd.Find("pos:1"))
	if last := ups[len(ups)-1]; last.Source != "fakesh" {
		t.Errorf("fallback updates = %+v", ups)
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
