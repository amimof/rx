package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func ctrlY() tea.KeyMsg { return key(tea.KeyCtrlY) }

func TestQuoteCycle(t *testing.T) {
	h := gitHarness(t, `git tag -a v1 -m hello`, "message")
	for _, want := range []string{
		`git tag -a v1 -m 'hello'`,
		`git tag -a v1 -m "hello"`,
		`git tag -a v1 -m hello`,
		`git tag -a v1 -m 'hello'`,
	} {
		h.keys(ctrlY())
		h.want(want)
	}
	// Each step is one undo entry.
	h.keys(key(tea.KeyCtrlZ))
	h.want(`git tag -a v1 -m hello`)
	h.keys(key(tea.KeyCtrlZ))
	h.want(`git tag -a v1 -m "hello"`)
}

// Styles that write the value the same way are skipped: "a b" needs quotes
// anyway, and double quotes can't hold "!".
func TestQuoteSkipsSame(t *testing.T) {
	h := gitHarness(t, `git tag -a v1 -m 'a b'`, "message")
	for _, want := range []string{
		`git tag -a v1 -m "a b"`,
		`git tag -a v1 -m 'a b'`,
		`git tag -a v1 -m "a b"`,
	} {
		h.keys(ctrlY())
		h.want(want)
	}

	h = gitHarness(t, `git tag -a v1 -m 'hi!'`, "message")
	h.keys(ctrlY())
	h.want(`git tag -a v1 -m 'hi!'`)
	if h.m.notice == "" || len(h.m.undo) != 0 {
		t.Fatalf("want a notice and no undo entry, got %q, %d", h.m.notice, len(h.m.undo))
	}
}

func TestQuoteKeptOnEdit(t *testing.T) {
	h := gitHarness(t, `git tag -a v1 -m "msg"`, "message")
	h.keys(typ("new"), key(tea.KeyTab))
	h.want(`git tag -a v1 -m "new"`)

	// A chosen style is kept too.
	h = gitHarness(t, `git tag -a v1 -m msg`, "message")
	h.keys(ctrlY(), ctrlY(), typ("new"), key(tea.KeyTab))
	h.want(`git tag -a v1 -m "new"`)
}

func TestQuoteCommitsTyped(t *testing.T) {
	h := gitHarness(t, `git tag v1`, "tagname")
	h.keys(typ("v2"), ctrlY())
	h.want(`git tag 'v2'`)
	if h.m.query != "" {
		t.Fatalf("query = %q, want it cleared", h.m.query)
	}
	h.keys(key(tea.KeyCtrlZ))
	h.want(`git tag v2`)
}

func TestQuoteDynamic(t *testing.T) {
	h := gitHarness(t, `git tag $V`, "tagname")
	h.keys(ctrlY())
	h.want(`git tag '$V'`)
	h.keys(ctrlY())
	h.want(`git tag "\$V"`)
}

func TestQuoteNothingToQuote(t *testing.T) {
	h := gitHarness(t, `git tag -a v1`, "annotate")
	h.keys(ctrlY())
	h.want(`git tag -a v1`)
	if len(h.m.undo) != 0 {
		t.Fatal("a bool flag has no value to quote")
	}
}

func TestQuoteZoomed(t *testing.T) {
	h := gitHarness(t, `git tag -a v1 -m k=v`, "message")
	h.keys(key(tea.KeyCtrlF), ctrlY())
	if h.m.zoom != nil {
		t.Fatal("quote should leave zoom")
	}
	h.want(`git tag -a v1 -m 'k=v'`)
}

func TestQuoteLegend(t *testing.T) {
	h := gitHarness(t, `git tag v1`, "tagname")
	if v := ansi.Strip(h.m.View()); !strings.Contains(v, "^y quote") {
		t.Fatalf("legend should show the quote key:\n%s", v)
	}
}
