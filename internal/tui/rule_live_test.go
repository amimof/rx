package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

// focusKey focuses the slot with key and waits for its candidates.
func (h *harness) focusKey(key string) {
	h.t.Helper()
	h.m.focus = h.m.cmd.Find(key)
	h.m.refocus()
	if h.m.loading {
		h.pump()
	}
}

// wantPreview checks the preview and the rule notice shown with it.
func (h *harness) wantPreview(line, notice string) {
	h.t.Helper()
	if got := ansi.Strip(h.m.preview()); got != line {
		h.t.Fatalf("preview = %q, want %q", got, line)
	}
	if got := h.m.ruleNotice(); got != notice {
		h.t.Fatalf("notice = %q, want %q", got, notice)
	}
}

func TestLiveRulesTyping(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	h := newHarness(t, line, nil)
	h.focusKey("subcommand")
	h.keys(typ("logs"))
	h.wantPreview("kubectl logs my-pod -n default", "logs-removes-resource: removed resource")
	// Only the preview has changed.
	h.want(line)
	if len(h.m.rules.Active()) != 0 {
		t.Fatalf("active = %v", h.m.rules.Active())
	}
	h.keys(key(tea.KeyBackspace))
	h.wantPreview("kubectl log pods my-pod -n default", "")
	h.keys(typ("s"), key(tea.KeyTab))
	// Tab commits what the preview showed.
	h.want("kubectl logs my-pod -n default")
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "name" {
		t.Fatalf("focus = %+v", s)
	}
}

func TestLiveRulesEsc(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	h := newHarness(t, line, nil)
	h.focusKey("subcommand")
	h.keys(typ("logs"), key(tea.KeyEsc))
	h.wantPreview(line, "")
	h.want(line)
}

func TestLiveRulesSelection(t *testing.T) {
	h := newHarness(t, "kubectl get pods my-pod -n default", nil)
	h.focusKey("subcommand")
	for h.m.selected() != "logs" {
		if h.m.sel == len(h.m.rows)-1 {
			t.Fatalf("no logs row in %+v", h.m.rows)
		}
		h.keys(key(tea.KeyCtrlN))
	}
	h.wantPreview("kubectl logs my-pod -n default", "logs-removes-resource: removed resource")
	h.keys(key(tea.KeyCtrlP))
	h.wantPreview("kubectl describe pods my-pod -n default", "")
	h.keys(key(tea.KeyCtrlN), key(tea.KeyEnter))
	h.want("kubectl logs my-pod -n default")
}

func TestLiveRulesCurlMethod(t *testing.T) {
	h := newHarness(t, "curl -X GET https://example.com", nil)
	h.focusKey("request")
	h.keys(typ("POST"))
	h.wantPreview("curl -X POST https://example.com -d '{}'", "post-adds-body: added --data")
	h.keys(key(tea.KeyEnter))
	h.want("curl -X POST https://example.com -d '{}'")
}

func TestLiveRulesAddFlag(t *testing.T) {
	sp, err := spec.Parse([]byte(`
match: { command: curl }
flags:
  - { name: silent, short: s, takes: none }
  - { name: verbose, short: v, takes: none }
positionals:
  - { index: 0, name: url }
rules:
  - name: silent-drops-verbose
    when: { flag: { name: silent, exists: true } }
    then: { remove: { flag: verbose } }
`), "curl.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := spec.NewRegistry()
	reg.Add(sp)
	h := newRegHarness(t, reg, "curl https://x -v", nil)
	for !h.m.onAddSlot() {
		h.keys(key(tea.KeyTab))
	}
	// A partly typed name selects --silent, but doesn't fire the rule.
	h.keys(typ("--sil"))
	h.wantPreview("curl https://x -v --silent", "")
	h.keys(typ("ent"))
	h.wantPreview("curl https://x --silent", "silent-drops-verbose: removed --verbose")
	h.want("curl https://x -v")
	// The short name fires it too, and selects the flag Enter adds.
	h.keys(key(tea.KeyCtrlU), typ("-s"))
	h.wantPreview("curl https://x --silent", "silent-drops-verbose: removed --verbose")
	h.keys(key(tea.KeyEnter))
	h.want("curl https://x --silent")
}

func TestCommitRules(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	h := newHarness(t, line, nil)
	h.m.o.Rules = RulesCommit
	h.focusKey("subcommand")
	h.keys(typ("logs"))
	h.wantPreview("kubectl logs pods my-pod -n default", "")
	h.keys(key(tea.KeyTab))
	h.want("kubectl logs my-pod -n default")
	if h.m.ruleNotice() != "logs-removes-resource: removed resource" {
		t.Fatalf("notice = %q", h.m.ruleNotice())
	}
}

func TestRulesOff(t *testing.T) {
	h := newHarness(t, "kubectl get pods my-pod -n default", nil)
	h.m.o.Rules = RulesOff
	h.keys(key(tea.KeyCtrlV), key(tea.KeyCtrlV))
	h.want("kubectl logs pods my-pod -n default")
	h.focusKey("subcommand")
	h.keys(typ("get"))
	h.wantPreview("kubectl get pods my-pod -n default", "")

	reg := spec.NewRegistry()
	_ = reg.LoadFS(plugins.FS, "")
	h = newRegHarness(t, reg, "curl https://x", nil)
	h.m.o.Rules = RulesOff
	h.keys(key(tea.KeyCtrlX))
	h.want("curl https://x -X POST")
}
