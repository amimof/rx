package tui

import (
	"io"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
)

const (
	insertCurl = `curl -ik -X POST -H "Authorization: Bearer $JWT" https://openstack.example.lab:5000/v3/OS-FEDERATION/identity_providers/spoke-01/protocols/openid/auth`
	insertWant = `curl -ik -X POST -H "Authorization: Bearer $JWT" https://openstack.example.lab:5000/v3/OS-FEDERATION/identity_providers/spoke-cluster-01/protocols/openid/auth`
)

// insertHarness focuses the URL of insertCurl and enters insert mode.
func insertHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, insertCurl, nil)
	h.focusKey("url")
	h.keys(alt("i"))
	if h.m.insert == nil {
		t.Fatalf("not in insert mode: %q", h.m.message)
	}
	return h
}

// wantText checks the edited text and the cursor, shown as |.
func (h *harness) wantText(want string) {
	h.t.Helper()
	r := []rune(h.m.query)
	got := string(r[:h.m.insert.cur]) + "|" + string(r[h.m.insert.cur:])
	if got != want {
		h.t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestInsertWholeValue(t *testing.T) {
	h := insertHarness(t)
	const url = "https://openstack.example.lab:5000/v3/OS-FEDERATION/identity_providers/spoke-01/protocols/openid/auth"
	h.wantText(url + "|")
	// Back over auth, openid, protocols, 01 and spoke, then after spoke.
	for range 5 {
		h.keys(alt("b"))
	}
	h.keys(alt("f"), typ("-cluster"))
	h.wantPreview(insertWant, "")
	// Nothing is written until the edit is applied.
	h.want(insertCurl)
	h.keys(key(tea.KeyEnter))
	if h.m.insert != nil || h.m.quitting || h.m.cmd.Slots[h.m.focus].Key != "url" {
		t.Fatalf("enter: insert %v, quitting %v", h.m.insert != nil, h.m.quitting)
	}
	h.want(insertWant)
	// One undo entry for the edit.
	h.keys(key(tea.KeyCtrlZ))
	h.want(insertCurl)
}

func TestInsertZoomedPart(t *testing.T) {
	h := newHarness(t, insertCurl, nil)
	h.focusKey("url")
	h.keys(key(tea.KeyCtrlF))
	for h.m.partName() != "path[3]" {
		h.keys(key(tea.KeyTab))
	}
	h.keys(alt("i"))
	h.wantText("spoke-01|")
	h.keys(key(tea.KeyHome), alt("f"), typ("-cluster"))
	h.wantText("spoke-cluster|-01")
	// The insert key applies the part and stays zoomed on it.
	h.keys(alt("i"))
	if h.m.insert != nil || h.m.zoom == nil || h.m.partName() != "path[3]" {
		t.Fatalf("alt+i: insert %v, zoomed %v", h.m.insert != nil, h.m.zoom != nil)
	}
	h.keys(key(tea.KeyEnter))
	h.want(insertWant)
}

func TestInsertZoomedTab(t *testing.T) {
	h := newHarness(t, insertCurl, nil)
	h.focusKey("url")
	h.keys(key(tea.KeyCtrlF), key(tea.KeyTab), key(tea.KeyTab), alt("i"))
	h.wantText("5000|")
	// Tab writes the part and moves to the next one, out of insert mode.
	h.keys(key(tea.KeyBackspace), key(tea.KeyBackspace), typ("13"), key(tea.KeyTab))
	if h.m.insert != nil || h.m.partName() != "path[0]" {
		t.Fatalf("tab: insert %v, on %q", h.m.insert != nil, h.m.partName())
	}
	h.keys(key(tea.KeyCtrlF))
	h.want(strings.Replace(insertCurl, ":5000", ":5013", 1))
}

func TestInsertKeys(t *testing.T) {
	h := newHarness(t, "curl -X POST http://localhost:7878", nil)
	h.focusKey("url")
	h.keys(alt("i"))
	for _, c := range []struct {
		k    tea.KeyMsg
		want string
	}{
		{key(tea.KeyCtrlA), "|http://localhost:7878"},
		{key(tea.KeyLeft), "|http://localhost:7878"},
		{key(tea.KeyRight), "h|ttp://localhost:7878"},
		{key(tea.KeyCtrlE), "http://localhost:7878|"},
		{key(tea.KeyRight), "http://localhost:7878|"},
		{alt("b"), "http://localhost:|7878"},
		{key(tea.KeyCtrlB), "http://localhost|:7878"},
		{key(tea.KeyCtrlW), "http://|:7878"},
		{typ("exämple.com"), "http://exämple.com|:7878"},
		{key(tea.KeyBackspace), "http://exämple.co|:7878"},
		{key(tea.KeyDelete), "http://exämple.co|7878"},
		{key(tea.KeyCtrlD), "http://exämple.co|878"},
		{key(tea.KeySpace), "http://exämple.co |878"},
		{key(tea.KeyCtrlH), "http://exämple.co|878"},
		{key(tea.KeyHome), "|http://exämple.co878"},
		{alt("f"), "http|://exämple.co878"},
		{key(tea.KeyCtrlF), "http:|//exämple.co878"},
		{key(tea.KeyCtrlK), "http:|"},
		{key(tea.KeyEnd), "http:|"},
		{key(tea.KeyCtrlU), "|"},
	} {
		h.keys(c.k)
		h.wantText(c.want)
	}
	if h.m.value() != "" {
		t.Fatalf("value = %q after clearing", h.m.value())
	}
}

// TestInsertShadowsKeys checks that readline keys win over the editor's
// actions and transforms, and that other keys do nothing.
func TestInsertShadowsKeys(t *testing.T) {
	h := insertHarness(t)
	// ctrl+e is run, ctrl+d drop, ctrl+x curl's method cycle, ctrl+z undo.
	h.keys(key(tea.KeyCtrlE), key(tea.KeyCtrlD), key(tea.KeyCtrlX), key(tea.KeyCtrlZ), key(tea.KeyCtrlY), key(tea.KeyCtrlS), alt("x"))
	if h.m.quitting || h.m.insert == nil {
		t.Fatalf("quitting %v, insert %v", h.m.quitting, h.m.insert != nil)
	}
	h.want(insertCurl)
	// ctrl+d at the end deletes nothing.
	h.wantText("https://openstack.example.lab:5000/v3/OS-FEDERATION/identity_providers/spoke-01/protocols/openid/auth|")
}

func TestInsertDiscard(t *testing.T) {
	h := insertHarness(t)
	h.keys(key(tea.KeyCtrlU), typ("x"))
	// The first esc discards the edit, the second cancels as usual.
	h.keys(key(tea.KeyEsc))
	if h.m.insert != nil || h.m.query != "" || h.m.cancelled {
		t.Fatalf("esc: insert %v, query %q, cancelled %v", h.m.insert != nil, h.m.query, h.m.cancelled)
	}
	h.want(insertCurl)
	if len(h.m.undo) != 0 {
		t.Fatalf("undo has %d entries", len(h.m.undo))
	}
}

// TestInsertPickedRow checks that a row picked in insert mode becomes the
// text to edit.
func TestInsertPickedRow(t *testing.T) {
	h := newHarness(t, "curl -X POST http://localhost:7878", nil)
	h.focusKey("request")
	h.keys(alt("i"), key(tea.KeyCtrlU))
	var rows []string
	for _, r := range h.m.rows {
		rows = append(rows, r.value)
	}
	i := slices.Index(rows, "PATCH")
	if i < 0 {
		t.Fatalf("no PATCH in %v", rows)
	}
	for range i + 1 {
		h.keys(key(tea.KeyDown))
	}
	h.keys(key(tea.KeyLeft), typ("_"))
	h.wantText("PATC_|H")
	h.keys(key(tea.KeyEnter))
	h.want("curl -X PATC_H http://localhost:7878")
}

func TestInsertNoValue(t *testing.T) {
	h := newHarness(t, "curl -k http://localhost:7878", nil)
	h.focusKey("insecure")
	h.keys(alt("i"))
	if h.m.insert != nil || h.m.message != "no value to edit" {
		t.Fatalf("insert %v, message %q", h.m.insert != nil, h.m.message)
	}
}

// TestInsertView checks the cursor in the preview and the search line, and
// that the legend shows only insert mode's keys.
func TestInsertView(t *testing.T) {
	h := newHarness(t, "curl -X POST http://localhost:7878", nil)
	h.focusKey("url")
	h.keys(alt("i"), alt("b"))
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	h.m.st = newStyles(r, nil)
	h.m.width = 120
	view := h.m.View()
	buf := cells(view, 120)
	var rev []string
	for y := range buf.Height() {
		var b strings.Builder
		for x := range 120 {
			if c := buf.Cell(x, y); c != nil && c.Style.Attrs.Contains(cellbuf.ReverseAttr) {
				b.WriteRune(c.Rune)
			}
		}
		if b.Len() > 0 {
			rev = append(rev, b.String())
		}
	}
	// The value is reversed but for the cursor on 7; in the search line,
	// only the cursor is.
	if want := []string{"http://localhost:878", "7"}; !slices.Equal(rev, want) {
		t.Errorf("reversed = %q, want %q", rev, want)
	}
	plain := ansi.Strip(view)
	for _, want := range []string{"url [insert]: http://localhost:7878", "M-b/M-f word", "M-i/enter apply", "esc discard"} {
		if !strings.Contains(plain, want) {
			t.Errorf("no %q in view:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "cycle method") {
		t.Errorf("transforms shown in insert mode:\n%s", plain)
	}
}
