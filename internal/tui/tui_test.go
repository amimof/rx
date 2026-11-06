package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/history"
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
	fakeKubectl(t)
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	c := spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
	cmd := c.Classify(parse.Parse(line), 0)
	h := &harness{t: t, ups: make(chan engine.Update, 16)}
	h.m = newModel(Options{
		Cmd: cmd, Spec: reg.Lookup(cmd.Name), History: hist,
		Engine: engine.New(nil, nil),
	}, newStyles(lipgloss.NewRenderer(os.Stderr)))
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
		if h.m.reqID != before && !h.m.quitting {
			h.pump()
		}
	}
}

func typ(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func alt(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: true} }
func key(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

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

func TestLiteralValue(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(typ("brand-new"), key(tea.KeyEnter))
	if got := h.m.cmd.Render(); got != "k get pods -n brand-new" {
		t.Fatalf("result = %q", got)
	}
}

func TestTransformAndAddFlag(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(alt("A"))
	if got := h.m.cmd.Render(); got != "k get pods -A" {
		t.Fatalf("after M-A = %q", got)
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
