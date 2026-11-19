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

	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

const zoomCurl = `curl -H 'Content-Type: application/json' https://localhost:7878/compute/v2/servers -k`

// zoomHarness focuses the URL of zoomCurl and zooms in.
func zoomHarness(t *testing.T, hist *history.Index) *harness {
	t.Helper()
	h := newHarness(t, zoomCurl, hist)
	h.focusKey("url")
	h.keys(key(tea.KeyCtrlF))
	if h.m.zoom == nil {
		t.Fatalf("not zoomed: %q", h.m.message)
	}
	return h
}

func TestZoomEditPart(t *testing.T) {
	h := zoomHarness(t, nil)
	if got := h.m.partName(); got != "scheme" {
		t.Fatalf("starts on %q, want scheme", got)
	}
	h.keys(key(tea.KeyTab), key(tea.KeyTab))
	if got := h.m.partName(); got != "port" {
		t.Fatalf("on %q, want port", got)
	}
	h.keys(typ("8080"))
	h.wantPreview(`curl -H 'Content-Type: application/json' https://localhost:8080/compute/v2/servers -k`, "")
	// Nothing is written until the zoom is applied.
	h.want(zoomCurl)
	h.keys(key(tea.KeyEnter))
	if h.m.zoom != nil || h.m.quitting {
		t.Fatalf("enter: zoomed %v, quitting %v", h.m.zoom != nil, h.m.quitting)
	}
	h.want(`curl -H 'Content-Type: application/json' https://localhost:8080/compute/v2/servers -k`)

	// One undo entry for the whole zoom.
	h.keys(key(tea.KeyCtrlZ))
	h.want(zoomCurl)
}

func TestZoomSeveralParts(t *testing.T) {
	h := zoomHarness(t, nil)
	h.keys(key(tea.KeyTab), typ("example.com"), key(tea.KeyTab), typ("80"), key(tea.KeyTab), key(tea.KeyTab), typ("v3"))
	// Shift-Tab on the first part wraps to the last, Tab on the last to the
	// first.
	for range 4 {
		h.keys(key(tea.KeyShiftTab))
	}
	if got := h.m.partName(); got != "scheme" {
		t.Fatalf("on %q, want scheme", got)
	}
	h.keys(key(tea.KeyShiftTab))
	if got := h.m.partName(); got != "path[2]" {
		t.Fatalf("shift+tab on scheme: on %q, want path[2]", got)
	}
	h.keys(key(tea.KeyTab))
	if got := h.m.partName(); got != "scheme" {
		t.Fatalf("tab on path[2]: on %q, want scheme", got)
	}
	h.keys(typ("http"), key(tea.KeyTab))
	// ctrl+z undoes part edits, newest first: the scheme.
	h.keys(key(tea.KeyCtrlZ))
	// ←/→ do nothing.
	h.keys(key(tea.KeyLeft), key(tea.KeyRight))
	if got := h.m.partName(); got != "host" {
		t.Fatalf("after arrows: on %q, want host", got)
	}
	h.keys(key(tea.KeyCtrlF))
	h.want(`curl -H 'Content-Type: application/json' https://example.com:80/compute/v3/servers -k`)
}

func TestZoomDiscard(t *testing.T) {
	h := zoomHarness(t, nil)
	h.keys(key(tea.KeyTab), typ("example.com"), key(tea.KeyTab), typ("80"))
	// The first esc clears the query, the second leaves without applying.
	h.keys(key(tea.KeyEsc))
	if h.m.zoom == nil || h.m.query != "" {
		t.Fatalf("first esc: zoomed %v, query %q", h.m.zoom != nil, h.m.query)
	}
	h.keys(key(tea.KeyEsc))
	if h.m.zoom != nil || h.m.cancelled {
		t.Fatalf("second esc: zoomed %v, cancelled %v", h.m.zoom != nil, h.m.cancelled)
	}
	h.want(zoomCurl)
	if len(h.m.undo) != 0 {
		t.Fatalf("undo has %d entries", len(h.m.undo))
	}
}

func TestZoomTabStays(t *testing.T) {
	h := zoomHarness(t, nil)
	h.keys(key(tea.KeyTab), typ("example.com"), key(tea.KeyTab))
	// Tab moves to the next part without writing the slot.
	if h.m.zoom == nil || h.m.partName() != "port" {
		t.Fatalf("tab: zoomed %v", h.m.zoom != nil)
	}
	h.want(zoomCurl)
	// ctrl+f zooms out, keeping the edits and the focus.
	h.keys(key(tea.KeyCtrlF))
	h.want(`curl -H 'Content-Type: application/json' https://example.com:7878/compute/v2/servers -k`)
	if h.m.zoom != nil || h.m.cmd.Slots[h.m.focus].Key != "url" {
		t.Fatalf("ctrl+f: zoomed %v, focus %s", h.m.zoom != nil, h.m.cmd.Slots[h.m.focus].Key)
	}
}

func TestZoomHistoryParts(t *testing.T) {
	es, _ := history.ReadZsh(strings.NewReader(
		": 1:0;curl https://example.com:9090/a\n" +
			": 2:0;curl https://other.org:8443/b\n" +
			": 3:0;curl https://other.org:8443/c\n" +
			": 4:0;curl other.org:1234\n"))
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	x := history.Build(es, spec.Classifier{Specs: reg})
	h := zoomHarness(t, x)
	h.keys(key(tea.KeyTab), key(tea.KeyTab))
	var got []string
	for _, r := range h.m.rows {
		got = append(got, r.value)
	}
	// Ports from URLs only, the most used first, then the current one.
	if want := []string{"8443", "9090", "7878"}; !slices.Equal(got, want) {
		t.Fatalf("port rows = %v, want %v", got, want)
	}
	h.keys(key(tea.KeyDown), key(tea.KeyEnter))
	h.want(`curl -H 'Content-Type: application/json' https://localhost:8443/compute/v2/servers -k`)
}

func TestZoomNothingToSplit(t *testing.T) {
	h := newHarness(t, "curl -X POST http://localhost:7878", nil)
	h.focusKey("request")
	h.keys(key(tea.KeyCtrlF))
	if h.m.zoom != nil || h.m.message != "nothing to zoom into" {
		t.Fatalf("zoomed %v, message %q", h.m.zoom != nil, h.m.message)
	}
}

func TestZoomQuoted(t *testing.T) {
	h := newHarness(t, zoomCurl, nil)
	h.focusKey("header")
	h.keys(key(tea.KeyCtrlF))
	if got := h.m.partName(); got != "part[0]" {
		t.Fatalf("on %q", got)
	}
	h.keys(key(tea.KeyTab), typ("text"), key(tea.KeyTab), typ("plain"), key(tea.KeyEnter))
	h.want(`curl -H 'Content-Type: text/plain' https://localhost:7878/compute/v2/servers -k`)
}

// TestZoomView checks that only the zoomed part is reversed, inside the
// quotes, and that the search line and legend show zoom.
func TestZoomView(t *testing.T) {
	h := newHarness(t, zoomCurl, nil)
	h.focusKey("header")
	h.keys(key(tea.KeyCtrlF), key(tea.KeyTab))
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	h.m.st = newStyles(r, nil)
	h.m.width = 120
	view := h.m.View()
	buf := cells(view, 120)
	var rev strings.Builder
	for y := range buf.Height() {
		for x := range 120 {
			if c := buf.Cell(x, y); c != nil && c.Style.Attrs.Contains(cellbuf.ReverseAttr) {
				rev.WriteRune(c.Rune)
			}
		}
	}
	if rev.String() != "application" {
		t.Errorf("reversed = %q", rev.String())
	}
	plain := ansi.Strip(view)
	for _, want := range []string{"--header › part[1]:", "tab/s-tab part", "^f zoom out", "esc discard"} {
		if !strings.Contains(plain, want) {
			t.Errorf("no %q in view:\n%s", want, plain)
		}
	}
}
