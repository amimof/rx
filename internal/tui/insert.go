package tui

import (
	"slices"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amimof/rx/internal/model"
)

// insertState is set while the query is edited in place (ActInsert). The
// query holds the text, so the value, the preview and live rules follow it
// as they do when typing.
type insertState struct {
	cur int // cursor, in runes into the query
}

// insertIn loads the pending value, else the focused slot's value, or the
// zoomed part, into the query with the cursor at the end. Flag names can't
// be edited, so "+ flag" and bool flags have nothing to insert into.
func (m *Model) insertIn() {
	if m.onAddSlot() || m.cmd.Slots[m.focus].Kind == model.Bool {
		m.message = "no value to edit"
		return
	}
	var v string
	if m.zoom != nil {
		if v = m.partValue(); v == "" {
			v = m.zoom.split.Parts[m.zoom.at].Text
		}
	} else if v = m.value(); v == "" {
		v = m.cmd.Slots[m.focus].Value
	}
	m.query, m.pin = v, ""
	m.filter()
	m.insert = &insertState{cur: len([]rune(v))}
}

// readline maps the keys that edit the query in insert mode to what they
// do with its runes and cursor. They take precedence over the editor's
// actions and the plugin's transforms.
var readline = map[string]func(r []rune, c int) ([]rune, int){
	"left":       func(r []rune, c int) ([]rune, int) { return r, max(c-1, 0) },
	"ctrl+b":     func(r []rune, c int) ([]rune, int) { return r, max(c-1, 0) },
	"right":      func(r []rune, c int) ([]rune, int) { return r, min(c+1, len(r)) },
	"ctrl+f":     func(r []rune, c int) ([]rune, int) { return r, min(c+1, len(r)) },
	"home":       func(r []rune, _ int) ([]rune, int) { return r, 0 },
	"ctrl+a":     func(r []rune, _ int) ([]rune, int) { return r, 0 },
	"end":        func(r []rune, _ int) ([]rune, int) { return r, len(r) },
	"ctrl+e":     func(r []rune, _ int) ([]rune, int) { return r, len(r) },
	"alt+b":      func(r []rune, c int) ([]rune, int) { return r, wordBack(r, c) },
	"alt+left":   func(r []rune, c int) ([]rune, int) { return r, wordBack(r, c) },
	"ctrl+left":  func(r []rune, c int) ([]rune, int) { return r, wordBack(r, c) },
	"alt+f":      func(r []rune, c int) ([]rune, int) { return r, wordFwd(r, c) },
	"alt+right":  func(r []rune, c int) ([]rune, int) { return r, wordFwd(r, c) },
	"ctrl+right": func(r []rune, c int) ([]rune, int) { return r, wordFwd(r, c) },
	"backspace":  func(r []rune, c int) ([]rune, int) { return cut(r, max(c-1, 0), c) },
	"ctrl+h":     func(r []rune, c int) ([]rune, int) { return cut(r, max(c-1, 0), c) },
	"delete":     func(r []rune, c int) ([]rune, int) { return cut(r, c, min(c+1, len(r))) },
	"ctrl+d":     func(r []rune, c int) ([]rune, int) { return cut(r, c, min(c+1, len(r))) },
	"ctrl+w":     func(r []rune, c int) ([]rune, int) { return cut(r, wordBack(r, c), c) },
	"alt+backspace": func(r []rune, c int) ([]rune, int) {
		return cut(r, wordBack(r, c), c)
	},
	"alt+d":  func(r []rune, c int) ([]rune, int) { return cut(r, c, wordFwd(r, c)) },
	"ctrl+u": func(r []rune, c int) ([]rune, int) { return cut(r, 0, c) },
	"ctrl+k": func(r []rune, c int) ([]rune, int) { return cut(r, c, len(r)) },
}

// cut removes runes lo to hi and leaves the cursor at lo.
func cut(r []rune, lo, hi int) ([]rune, int) {
	return append(r[:lo:lo], r[hi:]...), lo
}

// isWord reports whether r is part of a word for the word keys. Anything
// else, e.g. - / . _ :, separates words, so they stop inside values like
// "spoke-01".
func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordBack returns the start of the word before cursor c.
func wordBack(r []rune, c int) int {
	for c > 0 && !isWord(r[c-1]) {
		c--
	}
	for c > 0 && isWord(r[c-1]) {
		c--
	}
	return c
}

// wordFwd returns the end of the word after cursor c.
func wordFwd(r []rune, c int) int {
	for c < len(r) && !isWord(r[c]) {
		c++
	}
	for c < len(r) && isWord(r[c]) {
		c++
	}
	return c
}

// insertKey handles k in insert mode and reports whether it did. The slot
// keys leave insert mode and are then handled as usual, so they commit and
// move on; picking, linking and cancelling work as usual. Other keys do
// nothing, so a transform or an action can't fire in the middle of an edit.
func (m *Model) insertKey(k tea.KeyMsg) bool {
	switch m.act[k.String()] {
	case ActInsert, ActAccept:
		m.insertApply()
		return true
	case ActBack:
		m.insertDiscard()
		return true
	}
	if f, ok := readline[k.String()]; ok {
		m.adoptSel()
		r, c := f([]rune(m.query), m.insert.cur)
		m.setQuery(string(r), c)
		return true
	}
	switch k.Type {
	case tea.KeyRunes, tea.KeySpace:
		if k.Alt {
			return true
		}
		m.adoptSel()
		text := []rune(" ")
		if k.Type == tea.KeyRunes {
			text = k.Runes
		}
		c := m.insert.cur
		m.setQuery(string(slices.Insert([]rune(m.query), c, text...)), c+len(text))
		return true
	}
	switch m.act[k.String()] {
	case ActNextSlot, ActPrevSlot:
		m.insert = nil
		return false
	case ActUp, ActDown, ActLink, ActCancel:
		return false
	}
	return true
}

// adoptSel makes a row picked in insert mode the text to edit, with the
// cursor at its end.
func (m *Model) adoptSel() {
	if v := m.selected(); v != "" && v != m.query {
		m.query = v
		m.insert.cur = len([]rune(v))
		m.filter()
	}
}

// setQuery sets the edited text and cursor, refiltering if the text
// changed.
func (m *Model) setQuery(q string, cur int) {
	m.insert.cur = cur
	if q == m.query {
		return
	}
	m.query = q
	m.filter()
	if q == "" {
		m.sel = -1
	}
}

// insertApply leaves insert mode with the edit applied: written into the
// zoomed part, else committed to the slot. The focus stays where it is.
func (m *Model) insertApply() {
	m.insert = nil
	if m.zoom != nil {
		m.zoomWrite()
		m.query, m.sel, m.pin = "", -1, ""
		m.load()
		return
	}
	m.commit()
	m.refocus()
}

// insertDiscard leaves insert mode without applying the edit.
func (m *Model) insertDiscard() {
	m.insert = nil
	m.query, m.sel, m.pin = "", -1, ""
	m.filter()
}

// insertOffset returns the byte offset of the cursor in val, the focused
// slot's value as rendered: as is, or quoted. It is -1 outside insert mode,
// while a picked row is the value, or if the text can't be found in val.
func (m *Model) insertOffset(val string) int {
	if m.insert == nil || m.selected() != "" && m.selected() != m.query {
		return -1
	}
	off := len(string([]rune(m.query)[:m.insert.cur]))
	full := m.query
	if m.zoom != nil {
		s := m.zoomSplit()
		lo, _ := s.Offset(m.zoom.at)
		off, full = lo+off, s.Value()
	}
	switch {
	case val == full:
		return off
	case len(val) == len(full)+2 && val[1:len(val)-1] == full:
		return off + 1
	}
	return -1
}

// withCursor renders s in st with the rune at byte off in cur, or a space
// in cur after s if off is its end.
func withCursor(s string, off int, st, cur lipgloss.Style) string {
	if off < 0 || off > len(s) {
		return st.Render(s)
	}
	var out string
	if off > 0 {
		out += st.Render(s[:off])
	}
	if off == len(s) {
		return out + cur.Render(" ")
	}
	n := len(string([]rune(s[off:])[0]))
	out += cur.Render(s[off : off+n])
	if rest := s[off+n:]; rest != "" {
		out += st.Render(rest)
	}
	return out
}
