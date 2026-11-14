package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/amimof/rx/internal/spec"
)

const gitSpec = `
match: { command: git }
positionals:
  - { index: 0, name: subcommand }
  - { index: 1, name: tagname }
flags:
  - { name: annotate, short: a, takes: none }
  - { name: message, short: m, takes: value }
`

// gitHarness opens the editor on line with a git spec, focused on the
// slot with key.
func gitHarness(t *testing.T, line, key string) *harness {
	t.Helper()
	sp, err := spec.Parse([]byte(gitSpec), "git.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := spec.NewRegistry()
	reg.Add(sp)
	h := newRegHarness(t, reg, line, nil)
	h.focusKey(key)
	return h
}

func ctrlQ() tea.KeyMsg { return key(tea.KeyCtrlQ) }

func TestLinkTypeAndCommit(t *testing.T) {
	h := gitHarness(t, `git tag -a v0.1.0 -m v0.1.0`, "tagname")
	h.keys(ctrlQ())
	if len(h.m.linked) != 1 || h.m.cmd.Slots[h.m.cmd.FindID(h.m.linked[0])].Key != "message" {
		t.Fatalf("linked = %v", h.m.linked)
	}
	if !strings.Contains(ansi.Strip(h.m.View()), "(+1 linked)") {
		t.Fatal("search line should show the link")
	}
	h.keys(typ("v0.2.0"))
	h.wantPreview("git tag -a v0.2.0 -m v0.2.0", "")
	// Picking rows and clearing keep the link.
	h.keys(key(tea.KeyDown), key(tea.KeyUp), key(tea.KeyCtrlU), typ("v0.2.0"))
	if len(h.m.linked) != 1 {
		t.Fatal("link should survive up, down and clear")
	}
	h.want(`git tag -a v0.1.0 -m v0.1.0`)
	h.keys(key(tea.KeyTab))
	h.want(`git tag -a v0.2.0 -m v0.2.0`)
	if h.m.linked != nil {
		t.Fatalf("tab should end the link: %v", h.m.linked)
	}
	// One undo restores every linked slot.
	h.keys(key(tea.KeyCtrlZ))
	h.want(`git tag -a v0.1.0 -m v0.1.0`)
}

func TestLinkAccept(t *testing.T) {
	h := gitHarness(t, `git tag -a v1 -m v1`, "message")
	h.keys(ctrlQ(), typ("v2"), key(tea.KeyEnter))
	h.want(`git tag -a v2 -m v2`)
}

func TestLinkDropped(t *testing.T) {
	for _, k := range []tea.KeyMsg{key(tea.KeyEsc), ctrlQ()} {
		h := gitHarness(t, `git tag -a v1 -m v1`, "tagname")
		h.keys(ctrlQ(), typ("v2"), k)
		if h.m.linked != nil {
			t.Fatalf("%s should end the link", k)
		}
		h.keys(key(tea.KeyCtrlU), typ("v3"), key(tea.KeyTab))
		h.want(`git tag -a v3 -m v1`)
	}
	h := gitHarness(t, `git tag -a v1 -m v1`, "tagname")
	h.keys(ctrlQ(), typ("v2"), key(tea.KeyCtrlD))
	h.want(`git tag -a -m v1`)
}

func TestLinkNoMatch(t *testing.T) {
	h := gitHarness(t, `git tag -a v1 -m msg`, "tagname")
	h.keys(ctrlQ())
	if h.m.linked != nil {
		t.Fatalf("linked = %v", h.m.linked)
	}
	// Bool flags have no value to match.
	h.focusKey("annotate")
	h.keys(ctrlQ())
	if h.m.linked != nil {
		t.Fatalf("linked = %v", h.m.linked)
	}
}

func TestLinkLiveRules(t *testing.T) {
	h := newHarness(t, "kubectl get pods get", nil)
	h.focusKey("subcommand")
	h.keys(ctrlQ(), typ("logs"))
	h.wantPreview("kubectl logs logs", "logs-removes-resource: removed resource")
	h.keys(key(tea.KeyTab))
	h.want("kubectl logs logs")
}
