package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

// fakeKubectl puts a cobra-style kubectl on PATH that completes namespaces
// and flags.
func fakeKubectl(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
for a in "$@"; do pp=$prev; prev=$a; done
case "$prev" in
  -*) printf -- '--selector\tlabel query\n--watch\twatch\n:4\n'; exit 0 ;;
esac
if [ "$pp" = -n ]; then printf 'default\nkube-system\tsystem\nmonitoring\n:4\n'; exit 0; fi
printf ':4\n'
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type harness struct {
	t   *testing.T
	m   *Model
	ups chan engine.Update
}

func newHarness(t *testing.T, line string, hist *history.Index) *harness {
	t.Helper()
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	return newRegHarness(t, reg, line, hist)
}

func newRegHarness(t *testing.T, reg *spec.Registry, line string, hist *history.Index) *harness {
	t.Helper()
	fakeKubectl(t)
	c := spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
	cmd := c.Classify(parse.Parse(line), 0)
	h := &harness{t: t, ups: make(chan engine.Update, 16)}
	h.m = newModel(Options{
		Cmd: cmd, Spec: reg.Lookup(cmd.Name), History: hist,
		Engine: engine.New(nil, nil),
	}, newStyles(lipgloss.NewRenderer(os.Stderr), nil))
	h.m.ctx = context.Background()
	h.m.send = func(u engine.Update) { h.ups <- u }
	h.m.Init()
	h.pump()
	return h
}

// pump feeds completion updates into the model until the current request
// is done.
func (h *harness) pump() {
	h.t.Helper()
	for {
		select {
		case u := <-h.ups:
			h.m.Update(updateMsg(u))
			if u.Done && u.ID == h.m.reqID {
				return
			}
		case <-time.After(5 * time.Second):
			h.t.Fatal("timed out waiting for completion")
		}
	}
}

func (h *harness) keys(ks ...tea.KeyMsg) {
	for _, k := range ks {
		before := h.m.reqID
		h.m.Update(k)
		// History and spec sources load without an engine request.
		if h.m.reqID != before && h.m.loading && !h.m.quitting {
			h.pump()
		}
	}
}

func typ(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func alt(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: true} }
func key(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

func TestInitialFocus(t *testing.T) {
	const flux = `
match: { command: flux }
positionals:
  - { index: 0, name: verb }
  - { index: 1, name: kind }
  - { index: 2, name: name }
focus: kind
`
	const find = `
match: { command: find }
flags:
  - { name: type, short: type, takes: value }
focus: [-name, -type]
`
	const kubectl = `
match: { command: kubectl }
flags:
  - { name: namespace, short: n, takes: value }
  - { name: selector, short: l, takes: value }
focus: [selector, --namespace]
`
	const kubectlResource = `
match: { command: kubectl, aliases: [k] }
positionals:
  - { index: 0, name: verb }
  - { index: 1, name: resource }
focus: resource
`
	for _, tt := range []struct{ spec, line, want string }{
		{flux, "flux get kustomization", "kind"},
		{flux, "flux get", "kind"},
		{flux, "flux", "kind"},
		// -name isn't declared; takesAll infers that it takes a value.
		{find, `find . -name "*.txt" -type d`, "name"},
		{find, "find . -type d", "type"},
		{kubectl, "kubectl get pods -o wide -n default", "namespace"},
		{kubectl, "kubectl get pods -n default -l app=web", "selector"},
		// No match falls back to the first value-bearing flag.
		{kubectl, "kubectl get pods --output wide", "output"},
		// k is an alias for the kubecolor wrapper.
		{kubectlResource, "k get secrets", "resource"},
		{kubectlResource, "kubectl get secrets", "resource"},
	} {
		sp, err := spec.Parse([]byte(tt.spec), "test.yaml")
		if err != nil {
			t.Fatal(err)
		}
		reg := spec.NewRegistry()
		reg.Add(sp)
		c := spec.Classifier{Specs: reg, Infer: takesAll{}, Aliases: spec.Aliases{"k": "kubecolor"}}
		cmd := c.Classify(parse.Parse(tt.line), 0)
		m := newModel(Options{Cmd: cmd, Spec: reg.Lookup(cmd.Name)}, newStyles(lipgloss.NewRenderer(os.Stderr), nil))
		if got := m.cmd.Slots[m.initialFocus()].Key; got != tt.want {
			t.Errorf("%q: focus = %s, want %s", tt.line, got, tt.want)
		}
		if got := m.cmd.Render(); got != tt.line {
			t.Errorf("%q: placeholders changed render to %q", tt.line, got)
		}
	}
}

// takesAll infers that every unknown flag takes a value.
type takesAll struct{}

func (takesAll) TakesValue(string, string) (bool, bool) { return true, true }

func TestPickFromCompletion(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring -o wide", nil)
	if got := h.m.cmd.Slots[h.m.focus].Key; got != "namespace" {
		t.Fatalf("initial focus = %s", got)
	}
	if len(h.m.rows) != 3 {
		t.Fatalf("rows = %+v", h.m.rows)
	}
	h.keys(typ("ksys"))
	if h.m.rows[h.m.sel].value != "kube-system" || h.m.rows[h.m.sel].desc != "system" {
		t.Fatalf("selected = %+v", h.m.rows[h.m.sel])
	}
	if !strings.Contains(h.m.View(), "kube-system") {
		t.Fatal("view should preview the selection")
	}
	h.keys(key(tea.KeyEnter))
	if got := h.m.cmd.Render(); got != "k get pods -n kube-system -o wide" {
		t.Fatalf("result = %q", got)
	}
}

func TestSpinner(t *testing.T) {
	h := newHarness(t, "k get pods -n default -o wide", nil)
	frame := spinner.MiniDot.Frames[0]
	if strings.Contains(h.m.View(), frame) {
		t.Fatalf("spinner shown after loading:\n%s", h.m.View())
	}
	if _, cmd := h.m.Update(spinner.TickMsg{}); cmd != nil {
		t.Fatal("a tick after loading should stop the spinner")
	}

	// Moving to another slot starts a load, which starts the spinner.
	if _, cmd := h.m.Update(key(tea.KeyTab)); !h.m.loading || cmd == nil {
		t.Fatalf("loading %v, cmd %v", h.m.loading, cmd)
	}
	lines := strings.Split(h.m.View(), "\n")
	query, spin := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "▏") && query < 0 {
			query = i
		}
		if strings.Contains(l, frame) {
			spin = i
		}
	}
	if query < 0 || spin <= query || strings.Contains(lines[query], "loading") {
		t.Fatalf("spinner should be below the list:\n%s", h.m.View())
	}
	if _, cmd := h.m.Update(spinner.TickMsg{}); cmd == nil {
		t.Fatal("the spinner should keep ticking while loading")
	}
	h.pump()
	if strings.Contains(h.m.View(), frame) || strings.Contains(h.m.View(), spinner.MiniDot.Frames[1]) {
		t.Fatalf("spinner shown after loading:\n%s", h.m.View())
	}
}

func TestLiteralValue(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(typ("brand-new"), key(tea.KeyEnter))
	if got := h.m.cmd.Render(); got != "k get pods -n brand-new" {
		t.Fatalf("result = %q", got)
	}
}

func TestTransformAndAddFlag(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(key(tea.KeyCtrlA))
	if got := h.m.cmd.Render(); got != "k get pods -A" {
		t.Fatalf("after ^a = %q", got)
	}
	// Tab past the remaining slots to "+ flag" and add --selector.
	for !h.m.onAddSlot() {
		h.keys(key(tea.KeyTab))
	}
	h.keys(typ("sel"))
	if h.m.rows[h.m.sel].value != "--selector" {
		t.Fatalf("flag rows = %+v", h.m.rows)
	}
	h.keys(key(tea.KeyTab))
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "selector" {
		t.Fatalf("focus after adding flag = %+v", s)
	}
	h.keys(typ("app=web"), key(tea.KeyEnter))
	if got := h.m.cmd.Render(); got != "k get pods -A --selector app=web" {
		t.Fatalf("result = %q", got)
	}
}

func TestCancelKeepsSource(t *testing.T) {
	h := newHarness(t, "k  get pods   -n monitoring", nil)
	h.keys(typ("x"), key(tea.KeyEsc), key(tea.KeyEsc))
	if !h.m.cancelled {
		t.Fatal("second esc should cancel")
	}
	// Placeholders for missing positionals must not change the output.
	if got := h.m.cmd.Render(); got != "k  get pods   -n monitoring" {
		t.Fatalf("render = %q", got)
	}
}

// TestProgramTabThroughSlots runs a real bubbletea program, where engine
// updates go through the program's unbuffered message channel. Tabbing onto
// value-less flags used to deadlock the event loop.
func TestProgramTabThroughSlots(t *testing.T) {
	line := `tool tag -a "0.0.0" -m "0.0.0"`
	c := spec.Classifier{Specs: spec.NewRegistry()}
	cmd := c.Classify(parse.Parse(line), 0)
	in, w := io.Pipe()
	type result struct {
		out Outcome
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := run(Options{Cmd: cmd, Engine: engine.New(nil, nil)}, in, io.Discard)
		done <- result{out, err}
	}()
	go func() {
		// Twice around all slots, including "+ flag", then accept.
		for range 2 * (len(cmd.Slots) + 1) {
			w.Write([]byte("\t"))
			time.Sleep(20 * time.Millisecond)
		}
		w.Write([]byte("\r"))
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.out.Cancelled || r.out.Cmd.Render() != line {
			t.Fatalf("outcome = %+v, render %q", r.out, r.out.Cmd.Render())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("editor froze while tabbing through slots")
	}
}

const cycleSpec = `
match: { command: kubectl, aliases: [k] }
complete: [history, spec, cobra]
flags:
  - { name: namespace, short: n, takes: value }
positionals:
  - { index: 0, name: verb }
  - { index: 1, name: resource }
  - { index: 2, name: name }
transforms:
  - { key: N, op: cycle, flag: namespace }
  - { key: x, op: cycle, slot: name }
`

func cycleRegistry(t *testing.T) *spec.Registry {
	t.Helper()
	sp, err := spec.Parse([]byte(cycleSpec), "cycle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := spec.NewRegistry()
	reg.Add(sp)
	return reg
}

func TestCycleFillsValuesFromCompletion(t *testing.T) {
	h := newRegHarness(t, cycleRegistry(t), "k get pods", nil)
	for _, want := range []string{
		"k get pods -n default",
		"k get pods -n kube-system",
		"k get pods -n monitoring",
		// Flags cycle through absent after the last value.
		"k get pods",
		"k get pods -n default",
	} {
		h.keys(alt("N"))
		if h.m.errText != "" {
			t.Fatalf("error: %s", h.m.errText)
		}
		if got := h.m.cmd.Render(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if ns, ok := strings.CutPrefix(want, "k get pods -n "); ok {
			if got := h.m.selected(); got != ns {
				t.Fatalf("%q: selected %q, want %q", want, got, ns)
			}
		}
	}
}

// selected returns the value of the selected row, or "" if none is.
func (m *Model) selected() string {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return ""
	}
	return m.rows[m.sel].value
}

func TestCycleSelectsActiveValue(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	for _, want := range []string{"wide", "yaml"} {
		h.keys(key(tea.KeyCtrlO))
		if s := h.m.cmd.Slots[h.m.focus]; s.Key != "output" || s.Value != want {
			t.Fatalf("focused slot = %+v, want output %s", s, want)
		}
		if got := h.m.selected(); got != want {
			t.Fatalf("selected %q, want %q", got, want)
		}
	}
	// Moving the selection releases it from the active value, so later
	// completion updates leave it alone.
	h.keys(key(tea.KeyDown))
	moved := h.m.selected()
	if moved == "yaml" {
		t.Fatal("down should move off the active value")
	}
	h.m.Update(updateMsg{ID: h.m.reqID, Done: true})
	if got := h.m.selected(); got != moved {
		t.Fatalf("selection snapped back to %q, want %q", got, moved)
	}
	h.keys(key(tea.KeyEnter))
	if got := h.m.cmd.Render(); got != "k get pods -n monitoring -o "+moved {
		t.Fatalf("result = %q", got)
	}
}

func TestCycleFillsValuesFromHistory(t *testing.T) {
	es, _ := history.ReadZsh(strings.NewReader(": 1:0;k get pods -n monitoring\n"))
	reg := cycleRegistry(t)
	x := history.Build(es, spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}})
	h := newRegHarness(t, reg, "k get pods", x)
	h.keys(alt("N"))
	if got := h.m.cmd.Render(); got != "k get pods -n monitoring" {
		t.Fatalf("got %q", got)
	}
}

// Bare transform keys mean alt+<key>; the plain key types into the search.
func TestBareTransformKeyIsAlt(t *testing.T) {
	h := newRegHarness(t, cycleRegistry(t), "k get pods", nil)
	h.keys(typ("N"))
	if got := h.m.cmd.Render(); got != "k get pods" || h.m.query != "N" {
		t.Fatalf("plain N: command %q, query %q", got, h.m.query)
	}
	if !strings.Contains(h.m.View(), "M-N cycle namespace") {
		t.Fatal("help should label the transform M-N")
	}
}

func TestCtrlTransformHelp(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	if !strings.Contains(h.m.View(), "^a all namespaces") {
		t.Fatal("help should label the kubectl toggle ^a")
	}
}

func TestFocusTransform(t *testing.T) {
	for _, tt := range []struct{ line, want string }{
		{"k get pods -n monitoring", "k get pods my-pod -n monitoring"},
		{"k get pods old-pod -n monitoring", "k get pods my-pod -n monitoring"},
		{"k get", "k get my-pod"},
	} {
		h := newHarness(t, tt.line, nil)
		h.keys(key(tea.KeyCtrlG))
		if s := h.m.cmd.Slots[h.m.focus]; s.Key != "name" || h.m.query != "" {
			t.Fatalf("%q: focused %s, query %q", tt.line, s.Key, h.m.query)
		}
		if got := h.m.cmd.Render(); got != tt.line {
			t.Fatalf("%q: jumping changed the command to %q", tt.line, got)
		}
		h.keys(typ("my-pod"), key(tea.KeyEnter))
		if got := h.m.cmd.Render(); got != tt.want {
			t.Errorf("%q: result %q, want %q", tt.line, got, tt.want)
		}
	}
	h := newHarness(t, "k get pods", nil)
	if !strings.Contains(h.m.View(), "^g jump to name") {
		t.Fatal("help should label the jump ^g")
	}
}

// A value picked on the focused slot is kept when a transform runs, as it
// is when tabbing away.
func TestTransformKeepsPickedValue(t *testing.T) {
	const line = "kubectl get pods control-plane-9nj4j -n capi-system -w -o wide"
	h := newHarness(t, line, nil)
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "namespace" {
		t.Fatalf("initial focus = %s", s.Key)
	}
	h.keys(typ("ksys"), key(tea.KeyCtrlG))
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "name" {
		t.Fatalf("focused %s, want name", s.Key)
	}
	h.keys(typ("my-pod"), key(tea.KeyEnter))
	if got, want := h.m.cmd.Render(), "kubectl get pods my-pod -n kube-system -w -o wide"; got != want {
		t.Fatalf("ctrl+g: result %q, want %q", got, want)
	}

	h = newHarness(t, line, nil)
	h.keys(typ("ksys"), key(tea.KeyCtrlO))
	if got, want := h.m.cmd.Render(), "kubectl get pods control-plane-9nj4j -n kube-system -w -o yaml"; got != want {
		t.Fatalf("ctrl+o: result %q, want %q", got, want)
	}
}

// A focus transform adds the slot if the command doesn't have it.
func TestFocusTransformAddsSlot(t *testing.T) {
	sp, err := spec.Parse([]byte(`
match: { command: kubectl, aliases: [k] }
flags:
  - { name: selector, short: l, takes: value }
transforms:
  - { key: ctrl+g, op: focus, flag: selector }
`), "focus.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := spec.NewRegistry()
	reg.Add(sp)
	h := newRegHarness(t, reg, "k get pods", nil)
	h.keys(key(tea.KeyCtrlG))
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "selector" {
		t.Fatalf("focused %s", s.Key)
	}
	h.keys(typ("app=web"), key(tea.KeyEnter))
	if got := h.m.cmd.Render(); got != "k get pods -l app=web" {
		t.Fatalf("result %q", got)
	}
}

func TestCycleWithoutCandidates(t *testing.T) {
	h := newRegHarness(t, cycleRegistry(t), "k get pods", nil)
	h.keys(alt("x"))
	if !strings.Contains(h.m.errText, "no values to cycle") {
		t.Fatalf("errText = %q", h.m.errText)
	}
	if got := h.m.cmd.Render(); got != "k get pods" {
		t.Fatalf("command changed to %q", got)
	}
}

func TestCycleSource(t *testing.T) {
	es, _ := history.ReadZsh(strings.NewReader(": 1:0;k get pods -n monitoring\n: 2:0;k get pods -n old\n"))
	reg := spec.NewRegistry()
	_ = reg.LoadFS(plugins.FS, "")
	x := history.Build(es, spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}})
	h := newRegHarness(t, reg, "k get pods -n default", x)
	values := func() string {
		var vs []string
		for _, r := range h.m.rows {
			vs = append(vs, r.value)
		}
		sort.Strings(vs)
		return strings.Join(vs, ",")
	}
	for _, step := range []struct{ active, values string }{
		{"history", "monitoring,old"},
		{"cobra", "default,kube-system,monitoring"},
		{"", "default,kube-system,monitoring,old"},
	} {
		h.keys(key(tea.KeyCtrlS))
		if h.m.active != step.active || values() != step.values {
			t.Fatalf("source %q: rows %s, want %q: %s", h.m.active, values(), step.active, step.values)
		}
	}

	// The choice sticks across slots: one without history shows all, and
	// history is back on returning.
	h.keys(key(tea.KeyCtrlS))
	h.keys(key(tea.KeyShiftTab))
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "name" || h.m.active != "" {
		t.Fatalf("on %s: active %q, want all", s.Key, h.m.active)
	}
	h.keys(key(tea.KeyTab))
	if h.m.active != "history" || values() != "monitoring,old" {
		t.Fatalf("back on namespace: active %q, rows %s", h.m.active, values())
	}
	if !strings.Contains(h.m.View(), "namespace [history]:") {
		t.Fatal("view should show the active source")
	}

	// A typed query survives switching source.
	h.keys(typ("mon"), key(tea.KeyCtrlS))
	if h.m.query != "mon" || h.m.active != "cobra" || h.m.selected() != "monitoring" {
		t.Fatalf("query %q, active %q, selected %q", h.m.query, h.m.active, h.m.selected())
	}
}

// Cycles fill values from the active source, which for history needs no
// engine request.
func TestCycleFromHistorySource(t *testing.T) {
	es, _ := history.ReadZsh(strings.NewReader(": 1:0;k get pods -n monitoring\n"))
	reg := cycleRegistry(t)
	x := history.Build(es, spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}})
	h := newRegHarness(t, reg, "k get pods -n default", x)
	h.keys(key(tea.KeyCtrlS))
	if h.m.active != "history" {
		t.Fatalf("active = %q", h.m.active)
	}
	h.keys(alt("N"))
	if got := h.m.cmd.Render(); got != "k get pods -n monitoring" {
		t.Fatalf("got %q", got)
	}
}

// Sources the command doesn't enable are neither listed nor offered by
// Ctrl-S.
func TestDisabledSources(t *testing.T) {
	es, _ := history.ReadZsh(strings.NewReader(": 1:0;k get pods -n monitoring -o yaml\n"))
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	sp := reg.Lookup("kubectl")
	x := history.Build(es, spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}})

	h := newRegHarness(t, reg, "k get pods -o json", x)
	h.m.focusSlot("output")
	h.pump()
	if got := h.m.sources(h.m.enabledCands()); !slices.Contains(got, "history") || !slices.Contains(got, "spec") {
		t.Fatalf("sources = %q, want history and spec", got)
	}

	sp.Complete = spec.Sources{spec.SourceCobra}
	h.m.refocus()
	h.pump()
	if got := h.m.sources(h.m.enabledCands()); !slices.Equal(got, []string{"", "cobra"}) {
		t.Fatalf("sources = %q, want all and cobra", got)
	}
	for _, r := range h.m.rows {
		for _, s := range r.sources {
			if s == "history" || s == "spec" {
				t.Fatalf("row %q from disabled source %s", r.value, s)
			}
		}
	}
	h.keys(key(tea.KeyCtrlS))
	if h.m.active != "cobra" {
		t.Fatalf("ctrl+s: active = %q, want cobra", h.m.active)
	}
}

func TestRunCtrlE(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(typ("ksys"), key(tea.KeyCtrlE))
	if !h.m.quitting || h.m.cancelled || !h.m.run {
		t.Fatalf("ctrl+e should accept and run: quitting %v, cancelled %v, run %v", h.m.quitting, h.m.cancelled, h.m.run)
	}
	if got := h.m.cmd.Render(); got != "k get pods -n kube-system" {
		t.Fatalf("result = %q", got)
	}

	// Enter only puts the command back.
	h = newHarness(t, "k get pods -n monitoring", nil)
	h.keys(key(tea.KeyEnter))
	if !h.m.quitting || h.m.run {
		t.Fatalf("enter: quitting %v, run %v", h.m.quitting, h.m.run)
	}
}

func TestCustomKeymap(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	km := DefaultKeys()
	km[ActAccept] = []string{"ctrl+j"}
	km[ActDown] = []string{"ctrl+e"}
	km[ActRun] = nil
	h.m.keys, h.m.act = km, km.actions()

	h.keys(key(tea.KeyEnter))
	if h.m.quitting {
		t.Fatal("enter is no longer bound to accept")
	}
	h.keys(key(tea.KeyCtrlE))
	if h.m.sel != 0 {
		t.Fatalf("ctrl+e should move down, sel = %d", h.m.sel)
	}
	if help := h.m.help(); !strings.Contains(help, "^j accept") || strings.Contains(help, "enter") {
		t.Fatalf("help = %q", help)
	}
	h.keys(key(tea.KeyCtrlJ))
	if !h.m.quitting || h.m.cancelled {
		t.Fatal("ctrl+j should accept")
	}
}

func TestKeymapValidate(t *testing.T) {
	for _, tt := range []struct {
		km   Keymap
		want string
	}{
		{DefaultKeys(), ""},
		{Keymap{"nope": {"ctrl+x"}}, `unknown action "nope"`},
		{Keymap{ActAccept: {""}}, "empty key"},
		{Keymap{ActAccept: {"x"}}, "used for typing"},
		{Keymap{ActAccept: {"ctrl+x"}, ActDrop: {"ctrl+x"}}, "bound to both accept and drop"},
		{Keymap{ActDrop: nil}, ""},
		{func() Keymap { k := DefaultKeys(); k[ActAccept] = []string{"enter", "ctrl+e"}; return k }(), "bound to both accept and run"},
		{Keymap{"yank": {"ctrl+y"}}, `unknown action "yank"`},
	} {
		err := tt.km.Validate()
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%v: err = %v, want %q", tt.km, err, tt.want)
		}
	}
}

func TestHistoryRanksFirst(t *testing.T) {
	es, _ := history.ReadZsh(strings.NewReader(": 1:0;k get pods -n monitoring\n: 2:0;k get pods -n monitoring\n"))
	reg := spec.NewRegistry()
	_ = reg.LoadFS(plugins.FS, "")
	x := history.Build(es, spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}})
	h := newHarness(t, "k get pods -n default", x)
	r := h.m.rows[0]
	if r.value != "monitoring" || strings.Join(r.sources, ",") != "history,cobra" {
		t.Fatalf("first row = %+v", r)
	}
}

func TestViewBox(t *testing.T) {
	for _, width := range []int{80, 40} {
		h := newHarness(t, "k get pods -n monitoring -o wide --selector app=a-very-long-label-value", nil)
		h.m.width = width
		lines := strings.Split(h.m.View(), "\n")
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != width {
				t.Errorf("width %d: line %d is %d wide: %q", width, i, w, l)
			}
		}
		last := len(lines) - 1
		if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(lines[last], "╰") {
			t.Fatalf("width %d: no border:\n%s", width, h.m.View())
		}
		sep := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "├") })
		if sep < 0 || !strings.Contains(lines[sep+1], "slot") {
			t.Fatalf("width %d: no separator above the legend:\n%s", width, h.m.View())
		}
	}
}

func TestPreviewPrompt(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	if p := h.m.preview(); !strings.HasPrefix(p, "k get") {
		t.Fatalf("no prompt: preview = %q", p)
	}
	h.m.o.Prompt = DefaultPrompt
	if p := h.m.preview(); !strings.HasPrefix(p, DefaultPrompt+" k get") {
		t.Fatalf("preview = %q", p)
	}
}

func TestSlotParts(t *testing.T) {
	for _, tt := range []struct {
		s                model.Slot
		name, sep, value string
	}{
		{model.Slot{Kind: model.Flag, Name: "-n", Value: "a b", Dirty: true}, "-n", " ", "'a b'"},
		{model.Slot{Kind: model.Flag, Name: "--ns", Value: "x", Style: model.Equals, Dirty: true}, "--ns", "=", "x"},
		{model.Slot{Kind: model.Flag, Name: "-n", Value: "x", Style: model.Attached, Dirty: true}, "-n", "", "x"},
		{model.Slot{Kind: model.Flag, Name: "-n", Value: "a b", Raw: `-n  "a b"`}, "-n", "  ", `"a b"`},
		{model.Slot{Kind: model.Flag, Name: "-n", Dirty: true}, "", "", ""},
		{model.Slot{Kind: model.Bool, Name: "-A", Dirty: true}, "-A", "", ""},
		{model.Slot{Kind: model.Positional, Value: "pods", Raw: "pods"}, "", "", "pods"},
		{model.Slot{Kind: model.Positional, Dirty: true}, "", "", ""},
	} {
		n, s, v := slotParts(tt.s)
		if n != tt.name || s != tt.sep || v != tt.value {
			t.Errorf("%+v: got %q %q %q, want %q %q %q", tt.s, n, s, v, tt.name, tt.sep, tt.value)
		}
	}
}
