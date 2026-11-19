package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/part"
)

// zoomState is the focused value split into parts, one of which is edited
// at a time. The query edits the part at; moving to another part writes it
// into split. The slot itself only changes when the zoom is applied.
type zoomState struct {
	split part.Split
	at    int // index into split.Parts, always an editable part
	// undo holds split before each part edit.
	undo []part.Split
}

// zoomIn splits the pending value, else the focused slot's value, into
// parts and starts editing the first one.
func (m *Model) zoomIn() {
	if m.onAddSlot() {
		return
	}
	v := m.value()
	if v == "" {
		v = m.cmd.Slots[m.focus].Value
	}
	s := part.Parse(v)
	ed := s.Editable()
	if len(ed) < 2 {
		m.message = "nothing to zoom into"
		return
	}
	m.zoom = &zoomState{split: s, at: ed[0]}
	m.query, m.sel, m.pin = "", -1, ""
	m.load()
}

// zoomKey handles the keys that mean something else while zoomed in. It
// reports whether it handled k; other keys work as usual, and any that
// commits applies the zoom first. The slot keys move between parts.
func (m *Model) zoomKey(k tea.KeyMsg) bool {
	z := m.zoom
	switch m.act[k.String()] {
	case ActNextSlot:
		m.zoomMove(1)
		return true
	case ActPrevSlot:
		m.zoomMove(-1)
		return true
	case ActAccept, ActZoom:
		m.pin = ""
		m.commit()
		m.refocus()
		return true
	case ActBack:
		if m.query != "" || m.sel >= 0 {
			return false
		}
		// Leave without applying anything.
		m.refocus()
		return true
	case ActUndo:
		n := len(z.undo)
		if n == 0 {
			m.errText = "nothing to undo in zoom"
			return true
		}
		z.split, z.undo = z.undo[n-1], z.undo[:n-1]
		m.query, m.sel, m.pin = "", -1, ""
		m.load()
		return true
	case ActSource:
		// Part candidates only come from splitting the slot's values.
		return true
	}
	return false
}

// zoomMove writes the pending part value and moves d editable parts along,
// wrapping around at the ends.
func (m *Model) zoomMove(d int) {
	z := m.zoom
	ed := z.split.Editable()
	i := (slices.Index(ed, z.at) + d + len(ed)) % len(ed)
	if v := m.partValue(); v != "" && v != z.split.Parts[z.at].Text {
		z.undo = append(z.undo, z.split)
		z.split = z.split.Set(z.at, v)
	}
	z.at = ed[i]
	m.query, m.sel, m.pin = "", -1, ""
	m.load()
}

// partValue is the pending value of the zoomed part: the selected row, else
// the typed query, else "" to keep it.
func (m *Model) partValue() string {
	if v := m.selected(); v != "" {
		return v
	}
	return m.query
}

// zoomSplit is the zoomed value with the pending part value written in.
func (m *Model) zoomSplit() part.Split {
	z := m.zoom
	if v := m.partValue(); v != "" {
		return z.split.Set(z.at, v)
	}
	return z.split
}

// zoomValue is what the focused slot is set to when the zoom is applied, or
// "" if that is its current value.
func (m *Model) zoomValue() string {
	v := m.zoomSplit().Value()
	if v == m.cmd.Slots[m.focus].Value {
		return ""
	}
	return v
}

// partName is the name of the zoomed part.
func (m *Model) partName() string {
	return m.zoom.split.Parts[m.zoom.at].Name
}

// loadZoom loads the zoomed part's candidates: the same part of the slot's
// history and spec values that split the same way, and its current text.
func (m *Model) loadZoom() {
	m.cands = map[string]*row{}
	m.message, m.errText = "", ""
	// Drops the results of a request still running for the whole value.
	m.reqID++
	m.loading, m.active = false, ""

	z := m.zoom
	name := m.partName()
	add := func(v, source string, hist float64, order int) {
		s := part.Parse(v)
		if i := s.Find(name); s.Kind == z.split.Kind && i >= 0 {
			m.merge(s.Parts[i].Text, "", source, hist, order)
		}
	}
	hist, vals := m.enabledCands()
	for _, c := range hist {
		add(c.Value, "history", c.Score, 0)
	}
	for i, v := range vals {
		add(v, "spec", 0, i)
	}
	m.merge(z.split.Parts[z.at].Text, "", "current", 0, len(vals))
	m.filter()
}

// renderZoomed renders the focused slot while zoomed in: the zoomed part,
// bytes lo to hi of val, reversed and the rest underlined.
func (m *Model) renderZoomed(k model.Kind, name, sep, val string, lo, hi int) string {
	ns, vs := m.st.flag, m.st.value
	if k == model.Positional {
		vs = m.st.arg
	}
	ul := vs.Underline(true)
	var out string
	if name != "" {
		out += ns.Underline(true).Render(name)
	}
	if sep != "" {
		out += lipgloss.NewStyle().Render(sep)
	}
	for _, p := range []struct {
		s  string
		st lipgloss.Style
	}{{val[:lo], ul}, {val[lo:hi], vs.Reverse(true)}, {val[hi:], ul}} {
		if p.s != "" {
			out += p.st.Render(p.s)
		}
	}
	return out
}

// zoomRange returns the bytes of the zoomed part in val, the focused slot's
// value as rendered: as is, or quoted. If it can't be found, all of val.
func (m *Model) zoomRange(val string) (int, int) {
	s := m.zoomSplit()
	lo, hi := s.Offset(m.zoom.at)
	full := s.Value()
	switch {
	case val == full:
		return lo, hi
	case len(val) == len(full)+2 && val[1:len(val)-1] == full:
		return lo + 1, hi + 1
	}
	return 0, len(val)
}
