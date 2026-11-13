package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func (h *harness) want(line string) {
	h.t.Helper()
	if got := h.m.cmd.Render(); got != line {
		h.t.Fatalf("command = %q, want %q (notice %q)", got, line, h.m.notice)
	}
}

func TestRuleCycleSubcommand(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	h := newHarness(t, line, nil)
	h.keys(key(tea.KeyCtrlV))
	h.want("kubectl describe pods my-pod -n default")
	h.keys(key(tea.KeyCtrlV))
	h.want("kubectl logs my-pod -n default")
	if h.m.notice != "logs-removes-resource: removed resource" {
		t.Fatalf("notice = %q", h.m.notice)
	}
	if v := ansi.Strip(h.m.View()); !strings.Contains(v, "rule logs-removes-resource: removed resource") {
		t.Errorf("view lacks the notice:\n%s", v)
	}
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "subcommand" {
		t.Errorf("focus = %+v", s)
	}
	// Moving off logs brings the resource back.
	h.keys(key(tea.KeyCtrlV))
	h.want("kubectl edit pods my-pod -n default")
	if h.m.notice != "reverted logs-removes-resource" {
		t.Fatalf("notice = %q", h.m.notice)
	}
	// The notice lasts until the next key.
	h.keys(typ("x"))
	if h.m.notice != "" {
		t.Errorf("notice = %q", h.m.notice)
	}
}

func TestRuleTypedRoundTrip(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	h := newHarness(t, line, nil)
	h.m.focus = h.m.cmd.Find("subcommand")
	h.m.refocus()
	h.keys(typ("logs"), key(tea.KeyTab))
	h.want("kubectl logs my-pod -n default")
	// Tab moved on from the subcommand to the pod name, now at position 1.
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "name" || s.Index != 1 {
		t.Fatalf("focus = %+v", s)
	}
	h.keys(typ("other-pod"), key(tea.KeyShiftTab))
	h.want("kubectl logs other-pod -n default")
	h.keys(typ("get"), key(tea.KeyEnter))
	h.want("kubectl get pods other-pod -n default")
	if !h.m.quitting {
		t.Fatal("enter should accept")
	}
}

func TestRuleRoundTripRendersAsTyped(t *testing.T) {
	const line = "kubectl get pods my-pod -n default"
	h := newHarness(t, line, nil)
	h.m.focus = h.m.cmd.Find("subcommand")
	h.m.refocus()
	h.keys(typ("logs"), key(tea.KeyTab), key(tea.KeyShiftTab), typ("get"), key(tea.KeyEnter))
	h.want(line)
}

func TestRulePostAddsBody(t *testing.T) {
	h := newHarness(t, "curl https://example.com", nil)
	h.keys(key(tea.KeyCtrlX))
	h.want("curl https://example.com -X POST -d '{}'")
	h.keys(key(tea.KeyCtrlX), key(tea.KeyCtrlX))
	h.want("curl https://example.com -X PATCH -d '{}'")
	h.keys(key(tea.KeyCtrlX))
	h.want("curl https://example.com -X DELETE")
	if h.m.notice != "reverted post-adds-body" {
		t.Fatalf("notice = %q", h.m.notice)
	}
}

func TestRuleUndo(t *testing.T) {
	h := newHarness(t, "kubectl get pods my-pod -n default", nil)
	h.keys(key(tea.KeyCtrlV), key(tea.KeyCtrlV))
	h.want("kubectl logs my-pod -n default")
	// One undo takes back the cycle and the rule it fired.
	h.keys(key(tea.KeyCtrlZ))
	h.want("kubectl describe pods my-pod -n default")
	if len(h.m.rules.Active()) != 0 {
		t.Fatalf("active = %v", h.m.rules.Active())
	}
	// The rule fires again, from the restored state.
	h.keys(key(tea.KeyCtrlV))
	h.want("kubectl logs my-pod -n default")
	h.keys(key(tea.KeyCtrlZ), key(tea.KeyCtrlZ))
	h.want("kubectl get pods my-pod -n default")
	if h.m.cmd.Changed {
		t.Error("undoing everything should restore the unchanged command")
	}
}

func TestUndoCommit(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(typ("prod"), key(tea.KeyTab))
	h.want("k get pods -n prod")
	h.keys(key(tea.KeyCtrlZ))
	h.want("k get pods -n monitoring")
	if s := h.m.cmd.Slots[h.m.focus]; s.Key != "namespace" {
		t.Fatalf("focus = %+v", s)
	}
}

func TestUndoTransform(t *testing.T) {
	h := newHarness(t, "k get pods -n monitoring", nil)
	h.keys(key(tea.KeyCtrlZ))
	if h.m.errText != "nothing to undo" {
		t.Fatalf("error = %q", h.m.errText)
	}
	h.keys(key(tea.KeyCtrlA), key(tea.KeyCtrlO))
	h.want("k get pods -A -o wide")
	h.keys(key(tea.KeyCtrlZ))
	h.want("k get pods -A")
	h.keys(key(tea.KeyCtrlZ))
	h.want("k get pods -n monitoring")
}
