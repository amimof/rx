// Package tui implements the inline slot editor.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/internal/transform"
)

const listHeight = 8

// Options configures the editor.
type Options struct {
	Cmd     *model.Command
	Spec    *spec.Spec
	History *history.Index
	Engine  *engine.Engine
	Infer   spec.Inferrer
	TTY     *os.File
}

// Outcome is the result of an editing session.
type Outcome struct {
	Cancelled bool
	Cmd       *model.Command
}

// Run runs the editor on the terminal until the user accepts or cancels.
func Run(o Options) (Outcome, error) {
	return run(o, o.TTY, o.TTY)
}

func run(o Options, in io.Reader, out io.Writer) (Outcome, error) {
	r := lipgloss.NewRenderer(out)
	m := newModel(o, newStyles(r))
	p := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.ctx = ctx
	m.send = func(u engine.Update) { p.Send(updateMsg(u)) }
	final, err := p.Run()
	if err != nil {
		return Outcome{Cancelled: true}, err
	}
	fm := final.(*Model)
	return Outcome{Cancelled: fm.cancelled, Cmd: fm.cmd}, nil
}

type styles struct {
	focus, dim, sel, src, err, key lipgloss.Style
}

func newStyles(r *lipgloss.Renderer) styles {
	return styles{
		focus: r.NewStyle().Reverse(true),
		dim:   r.NewStyle().Faint(true),
		sel:   r.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		src:   r.NewStyle().Faint(true).Italic(true),
		err:   r.NewStyle().Foreground(lipgloss.Color("1")),
		key:   r.NewStyle().Bold(true),
	}
}

type updateMsg engine.Update

// row is a candidate shown in the list.
type row struct {
	value   string
	desc    string
	sources []string
	hist    float64 // frecency score, 0 if not in history
	order   int     // position in completion output
	literal bool    // "use typed text"
	matched []int
}

// Model is the bubbletea model.
type Model struct {
	o   Options
	st  styles
	cmd *model.Command
	sp  *spec.Spec

	focus int // index into cmd.Slots; len(cmd.Slots) is the "+ flag" slot
	query string
	rows  []row
	sel   int // -1 keeps the current value

	// Candidates for the focused slot, merged from all sources.
	cands   map[string]*row
	reqID   uint64
	loading bool
	stale   bool
	message string
	errText string

	// cycle is a cycle transform waiting for candidates to fill its values.
	cycle *pendingCycle

	ctx       context.Context
	send      func(engine.Update)
	width     int
	cancelled bool
	quitting  bool
}

type pendingCycle struct {
	id    uint64 // request whose completion fills the values
	key   string
	added bool // the slot was added to fetch values for it
}

func newModel(o Options, st styles) *Model {
	m := &Model{o: o, st: st, cmd: o.Cmd, sp: o.Spec, width: 80}
	m.addPlaceholders()
	return m
}

// addPlaceholders adds empty slots for spec positionals the command doesn't
// have yet, so they can be tabbed to. Empty slots render as nothing, and the
// command keeps rendering as its original source until something changes.
func (m *Model) addPlaceholders() {
	if m.sp == nil {
		return
	}
	changed := m.cmd.Changed
	for _, p := range m.sp.Positionals {
		if m.cmd.Find(p.Name) < 0 {
			m.cmd.Add(model.Slot{Kind: model.Positional, Key: p.Name, Label: p.Name, Index: p.Index})
		}
	}
	for i := range m.cmd.Slots {
		if m.cmd.Slots[i].Value == "" && m.cmd.Slots[i].Kind == model.Positional {
			m.cmd.Slots[i].Dirty = false
		}
	}
	m.cmd.Changed = changed
}

func (m *Model) Init() tea.Cmd {
	m.focus = m.initialFocus()
	m.refocus()
	return nil
}

// initialFocus returns the slot to start on: the first of the plugin's
// focus entries present in the command, else the first value-bearing flag,
// else the first slot. Starting on the slot under the cursor would be nicer.
func (m *Model) initialFocus() int {
	if m.sp != nil {
		for _, e := range m.sp.Focus {
			if i := m.cmd.Find(m.sp.FocusKey(e)); i >= 0 {
				return i
			}
		}
	}
	for i, s := range m.cmd.Slots {
		if s.Kind == model.Flag {
			return i
		}
	}
	return 0
}

func (m *Model) onAddSlot() bool { return m.focus >= len(m.cmd.Slots) }

// refocus resets the query and reloads candidates for the focused slot.
func (m *Model) refocus() {
	if m.focus > len(m.cmd.Slots) {
		m.focus = len(m.cmd.Slots)
	}
	if m.focus < 0 {
		m.focus = 0
	}
	m.query, m.sel = "", -1
	m.cands = map[string]*row{}
	m.message, m.errText, m.stale = "", "", false
	m.reqID++
	m.loading = true

	now := time.Now()
	if m.onAddSlot() {
		for _, c := range m.o.History.Candidates(m.cmd.Name, history.FlagsKey, now) {
			if m.cmd.Find(m.flagKey(c.Value)) < 0 {
				m.merge(c.Value, "", "history", c.Score, 0)
			}
		}
		if m.sp != nil {
			for _, f := range m.sp.Flags {
				if m.cmd.Find(f.Name) < 0 {
					m.merge("--"+f.Name, "", "spec", 0, 0)
				}
			}
		}
		m.o.Engine.Start(m.ctx, m.reqID, m.cmd, m.sp, -1, m.send)
	} else {
		s := m.cmd.Slots[m.focus]
		for _, c := range m.o.History.Candidates(m.cmd.Name, s.Key, now) {
			m.merge(c.Value, "", "history", c.Score, 0)
		}
		for i, v := range m.specValues(s) {
			m.merge(v, "", "spec", 0, i)
		}
		m.o.Engine.Start(m.ctx, m.reqID, m.cmd, m.sp, m.focus, m.send)
	}
	m.filter()
}

func (m *Model) specValues(s model.Slot) []string {
	if s.Kind == model.Positional {
		if p := m.sp.PositionalByName(s.Key); p != nil {
			return p.Values
		}
		return nil
	}
	if f := m.sp.Flag("--" + s.Key); f != nil {
		return f.Values
	}
	return nil
}

func (m *Model) flagKey(name string) string {
	if f := m.sp.Flag(name); f != nil {
		return f.Name
	}
	return strings.TrimLeft(name, "-")
}

func (m *Model) merge(value, desc, source string, hist float64, order int) {
	if value == "" {
		return
	}
	r, ok := m.cands[value]
	if !ok {
		r = &row{value: value, order: 1 << 30}
		m.cands[value] = r
	}
	if desc != "" && r.desc == "" {
		r.desc = desc
	}
	if hist > r.hist {
		r.hist = hist
	}
	if source != "history" && order < r.order {
		r.order = order
	}
	for _, s := range r.sources {
		if s == source {
			return
		}
	}
	r.sources = append(r.sources, source)
}

// filter recomputes the visible rows from the candidates and query.
// Without a query, rows are ordered by history frecency, then completion
// order. With a query, by fuzzy score boosted by frecency.
func (m *Model) filter() {
	all := m.ranked()
	if m.query == "" {
		m.rows = all
		if m.sel >= len(m.rows) {
			m.sel = len(m.rows) - 1
		}
		return
	}
	src := make([]string, len(all))
	for i, r := range all {
		src[i] = r.value
	}
	matches := fuzzy.Find(m.query, src)
	type scored struct {
		r     row
		score float64
	}
	ss := make([]scored, 0, len(matches))
	exact := false
	for _, mt := range matches {
		r := all[mt.Index]
		r.matched = mt.MatchedIndexes
		ss = append(ss, scored{r, float64(mt.Score) + 10*r.hist})
		exact = exact || r.value == m.query
	}
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].score > ss[j].score })
	m.rows = m.rows[:0]
	for _, s := range ss {
		m.rows = append(m.rows, s.r)
	}
	if !exact {
		m.rows = append(m.rows, row{value: m.query, literal: true})
	}
	m.sel = 0
}

// ranked returns the candidates ordered by history frecency, then
// completion order.
func (m *Model) ranked() []row {
	all := make([]row, 0, len(m.cands))
	for _, r := range m.cands {
		all = append(all, *r)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if (a.hist > 0) != (b.hist > 0) {
			return a.hist > 0
		}
		if a.hist != b.hist {
			return a.hist > b.hist
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.value < b.value
	})
	return all
}

// Update handles input and completion results.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Terminals that don't report a size send 0; keep the default.
		if msg.Width > 0 {
			m.width = msg.Width
		}
	case updateMsg:
		if msg.ID != m.reqID {
			return m, nil
		}
		if msg.Done {
			m.loading = false
		}
		m.stale = msg.Stale
		if msg.Err != nil {
			m.errText = msg.Err.Error()
		}
		if msg.Result.Message != "" {
			m.message = msg.Result.Message
		}
		for i, c := range msg.Result.Candidates {
			v := c.Value
			if m.onAddSlot() && !strings.HasPrefix(v, "-") {
				continue
			}
			m.merge(v, c.Desc, msg.Source, 0, i)
		}
		keepSel := m.sel
		m.filter()
		if m.query == "" {
			m.sel = min(keepSel, len(m.rows)-1)
		}
		if msg.Done && m.cycle != nil && m.cycle.id == msg.ID {
			m.finishCycle()
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Alt && k.Type == tea.KeyRunes && m.sp != nil {
		for _, t := range m.sp.Transforms {
			if t.Key == string(k.Runes) {
				err := transform.Apply(m.cmd, m.sp, t)
				if errors.Is(err, transform.ErrNoValues) {
					m.autoCycle(t.Target())
					return m, nil
				}
				if err != nil {
					m.errText = err.Error()
					return m, nil
				}
				m.refocus()
				return m, nil
			}
		}
	}
	switch k.Type {
	case tea.KeyCtrlC:
		m.cancelled, m.quitting = true, true
		return m, tea.Quit
	case tea.KeyEsc:
		if m.query != "" || m.sel >= 0 {
			m.query, m.sel = "", -1
			m.filter()
			return m, nil
		}
		m.cancelled, m.quitting = true, true
		return m, tea.Quit
	case tea.KeyEnter:
		m.commit()
		m.quitting = true
		return m, tea.Quit
	case tea.KeyTab:
		if m.commit() {
			// Added a flag that takes a value: stay on it.
			m.refocus()
			return m, nil
		}
		m.focus = (m.focus + 1) % (len(m.cmd.Slots) + 1)
		m.refocus()
	case tea.KeyShiftTab:
		m.commit()
		m.focus = (m.focus + len(m.cmd.Slots)) % (len(m.cmd.Slots) + 1)
		m.refocus()
	case tea.KeyUp, tea.KeyCtrlP:
		if m.sel > -1 {
			m.sel--
		}
	case tea.KeyDown, tea.KeyCtrlN:
		if m.sel < len(m.rows)-1 {
			m.sel++
		}
	case tea.KeyCtrlD:
		// Delete the focused slot.
		if !m.onAddSlot() {
			m.cmd.Remove(m.focus)
			m.refocus()
		}
	case tea.KeyBackspace:
		if m.query != "" {
			r := []rune(m.query)
			m.query = string(r[:len(r)-1])
			m.filter()
			if m.query == "" {
				m.sel = -1
			}
		}
	case tea.KeyCtrlU, tea.KeyCtrlW:
		m.query, m.sel = "", -1
		m.filter()
	case tea.KeySpace:
		m.query += " "
		m.filter()
	case tea.KeyRunes:
		if !k.Alt {
			m.query += string(k.Runes)
			m.filter()
		}
	}
	return m, nil
}

// autoCycle cycles the slot with key through the candidates the editor would
// list for it, for cycle transforms without values. The slot is focused so
// its candidates load; the cycle completes once they have.
func (m *Model) autoCycle(key string) {
	i := m.cmd.Find(key)
	added := false
	if i < 0 {
		s, err := transform.NewSlot(m.cmd, m.sp, key)
		if err != nil {
			m.errText = err.Error()
			return
		}
		i, added = m.cmd.Add(s), true
	}
	// An added slot may land on the focused index (the "+ flag" slot), whose
	// candidates are flag names, so it always needs a fresh load.
	if i == m.focus && !added && !m.loading {
		m.cycle = &pendingCycle{id: m.reqID, key: key}
		m.finishCycle()
		return
	}
	if i != m.focus || added {
		m.focus = i
		m.refocus()
	}
	m.cycle = &pendingCycle{id: m.reqID, key: key, added: added}
}

// finishCycle applies the pending cycle using the focused slot's
// candidates.
func (m *Model) finishCycle() {
	c := m.cycle
	m.cycle = nil
	var values []string
	for _, r := range m.ranked() {
		values = append(values, r.value)
	}
	if len(values) == 0 {
		if i := m.cmd.Find(c.key); c.added && i >= 0 && m.cmd.Slots[i].Value == "" {
			m.cmd.Remove(i)
			m.focus = min(m.focus, len(m.cmd.Slots))
		}
		m.errText = fmt.Sprintf("%v for %q", transform.ErrNoValues, c.key)
		return
	}
	if err := transform.Cycle(m.cmd, m.sp, c.key, values); err != nil {
		m.errText = err.Error()
		return
	}
	if i := m.cmd.Find(c.key); i >= 0 {
		m.focus = i
	}
	m.refocus()
}

// commit applies the selected row to the focused slot. It reports whether a
// flag that takes a value was added and focused.
func (m *Model) commit() bool {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return false
	}
	v := m.rows[m.sel].value
	if m.onAddSlot() {
		takes := m.flagTakesValue(v)
		m.focus = transform.AddFlag(m.cmd, m.sp, v, takes)
		return takes
	}
	m.focus = transform.SetAt(m.cmd, m.sp, m.focus, v)
	return false
}

func (m *Model) flagTakesValue(name string) bool {
	if f := m.sp.Flag(name); f != nil {
		switch f.Takes {
		case "value":
			return true
		case "none":
			return false
		}
		if len(f.Values) > 0 || f.Provider != "" {
			return true
		}
	}
	if m.o.Infer != nil {
		if t, ok := m.o.Infer.TakesValue(m.cmd.Name, name); ok {
			return t
		}
	}
	// Unknown: assume a value; an empty value renders as nothing and the
	// user can Ctrl-D the slot.
	return true
}

// View renders the editor.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.preview())
	b.WriteByte('\n')

	label := "+ flag"
	if !m.onAddSlot() {
		label = m.cmd.Slots[m.focus].Label
	}
	status := ""
	switch {
	case m.loading && m.stale:
		status = m.st.dim.Render(" refreshing…")
	case m.loading:
		status = m.st.dim.Render(" loading…")
	}
	fmt.Fprintf(&b, "%s %s▏%s\n", m.st.key.Render(label+":"), m.query, status)

	start := 0
	if m.sel >= listHeight {
		start = m.sel - listHeight + 1
	}
	for i := start; i < len(m.rows) && i < start+listHeight; i++ {
		b.WriteString(m.renderRow(i))
		b.WriteByte('\n')
	}
	if len(m.rows) == 0 && !m.loading {
		b.WriteString(m.st.dim.Render("  (no candidates, type a value)") + "\n")
	}
	if m.message != "" {
		b.WriteString(m.st.dim.Render("  "+firstLine(m.message)) + "\n")
	}
	if m.errText != "" {
		b.WriteString(m.st.err.Render("  "+firstLine(m.errText)) + "\n")
	}
	b.WriteString(m.help())
	return b.String()
}

func (m *Model) renderRow(i int) string {
	r := m.rows[i]
	cursor := "  "
	val := r.value
	if r.literal {
		val = fmt.Sprintf("use %q", r.value)
	}
	if i == m.sel {
		cursor = m.st.sel.Render("> ")
		val = m.st.sel.Render(val)
	}
	line := cursor + val
	extra := r.desc
	if len(r.sources) > 0 {
		extra = strings.TrimSpace(extra + "  [" + strings.Join(r.sources, ",") + "]")
	}
	if extra != "" {
		line += "  " + m.st.dim.Render(truncate(extra, max(m.width-lipgloss.Width(line)-4, 10)))
	}
	return line
}

// preview renders the command with the selected candidate applied and the
// focused slot highlighted.
func (m *Model) preview() string {
	parts := []string{m.st.dim.Render(m.cmd.Head.Raw)}
	for i, s := range m.cmd.Slots {
		if i == m.focus && m.sel >= 0 && m.sel < len(m.rows) {
			s.Value, s.Dirty = m.rows[m.sel].value, true
		}
		r := s.Render()
		if i == m.focus {
			if r == "" {
				r = "<" + s.Label + ">"
			}
			r = m.st.focus.Render(r)
		}
		if r != "" {
			parts = append(parts, r)
		}
	}
	if m.onAddSlot() {
		v := "+flag"
		if m.sel >= 0 && m.sel < len(m.rows) {
			v = m.rows[m.sel].value
		}
		parts = append(parts, m.st.focus.Render(v))
	}
	return strings.Join(parts, " ")
}

func (m *Model) help() string {
	h := "tab/s-tab slot · ↑↓ pick · enter accept · ^d drop · esc cancel"
	if m.sp != nil {
		var ts []string
		for _, t := range m.sp.Transforms {
			d := t.Desc
			if d == "" {
				d = t.Op + " " + t.Target()
			}
			ts = append(ts, "M-"+t.Key+" "+d)
		}
		if len(ts) > 0 {
			h += "\n" + strings.Join(ts, " · ")
		}
	}
	return m.st.dim.Render(h)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
