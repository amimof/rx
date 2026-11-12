// Package tui implements the inline slot editor.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	// Keys binds the editor's actions; nil uses DefaultKeys.
	Keys Keymap
	// Prompt is shown before the command; empty shows none.
	Prompt string
	// Theme styles the editor; roles it doesn't mention, or all of them
	// if nil, use DefaultTheme.
	Theme Theme
}

// Outcome is the result of an editing session.
type Outcome struct {
	Cancelled bool
	// Run is set when the user asked the shell to run the command.
	Run bool
	Cmd *model.Command
}

// Run runs the editor on the terminal until the user accepts or cancels.
func Run(o Options) (Outcome, error) {
	return run(o, o.TTY, o.TTY)
}

func run(o Options, in io.Reader, out io.Writer) (Outcome, error) {
	r := lipgloss.NewRenderer(out)
	m := newModel(o, newStyles(r, o.Theme))
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
	return Outcome{Cancelled: fm.cancelled, Run: fm.run, Cmd: fm.cmd}, nil
}

type styles struct {
	focus, dim, sel, err, spinner lipgloss.Style
	// Slot parts in the preview: the command word, flag names, flag values
	// and positional arguments.
	head, flag, value, arg lipgloss.Style
	border, prompt         lipgloss.Style
}

// newStyles draws t with r. Roles t doesn't mention use DefaultTheme.
func newStyles(r *lipgloss.Renderer, t Theme) styles {
	th := DefaultTheme()
	for role, s := range t {
		th[role] = s
	}
	return styles{
		focus:   th[RoleFocus].style(r),
		dim:     th[RoleDim].style(r),
		sel:     th[RoleSel].style(r),
		spinner: th[RoleSpinner].style(r),
		err:     th[RoleErr].style(r),
		head:    th[RoleHead].style(r),
		flag:    th[RoleFlag].style(r),
		value:   th[RoleValue].style(r),
		arg:     th[RoleArg].style(r),
		border:  th[RoleBorder].style(r),
		prompt:  th[RolePrompt].style(r),
	}
}

// DefaultPrompt is the symbol shown before the command.
const DefaultPrompt = "❯"

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
	// pin is a value the selection follows as candidates arrive, until the
	// user moves it. Set after a cycle so the active value is selected.
	pin string

	// Candidates for the focused slot, merged from all sources.
	cands   map[string]*row
	reqID   uint64
	loading bool
	spin    spinner.Model // shown below the candidates while loading
	message string
	errText string

	// source is the source chosen with Ctrl-S ("" for all), kept across
	// slots. active is the one in use for the focused slot: source if the
	// slot has it, else "".
	source, active string

	// cycle is a cycle transform waiting for candidates to fill its values.
	cycle *pendingCycle

	keys Keymap
	act  map[string]string // key -> action

	ctx       context.Context
	send      func(engine.Update)
	width     int
	cancelled bool
	run       bool
	quitting  bool
}

type pendingCycle struct {
	id    uint64 // request whose completion fills the values
	key   string
	added bool // the slot was added to fetch values for it
}

func newModel(o Options, st styles) *Model {
	m := &Model{o: o, st: st, cmd: o.Cmd, sp: o.Spec, width: 80, keys: o.Keys}
	s := spinner.MiniDot
	s.FPS = 100 * time.Millisecond // fzf's speed
	m.spin = spinner.New(spinner.WithSpinner(s), spinner.WithStyle(st.spinner))
	if m.keys == nil {
		m.keys = DefaultKeys()
	}
	m.act = m.keys.actions()
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
	if m.loading {
		return m.spin.Tick
	}
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
	m.query, m.sel, m.pin = "", -1, ""
	m.load()
}

// load reloads candidates for the focused slot from the active source,
// keeping the query.
func (m *Model) load() {
	m.cands = map[string]*row{}
	m.message, m.errText = "", ""
	m.reqID++

	hist, vals := m.historyCands(), m.specCands()
	m.active = ""
	if slices.Contains(m.sources(hist, vals), m.source) {
		m.active = m.source
	}
	if m.active == "" || m.active == "history" {
		for _, c := range hist {
			m.merge(c.Value, "", "history", c.Score, 0)
		}
	}
	if m.active == "" || m.active == "spec" {
		for i, v := range vals {
			m.merge(v, "", "spec", 0, i)
		}
	}
	switch m.active {
	case "history", "spec":
		m.loading = false
	default:
		m.loading = true
		m.o.Engine.StartFrom(m.ctx, m.reqID, m.cmd, m.sp, m.engineFocus(), m.active, m.send)
	}
	m.filter()
}

// engineFocus is the focused slot as passed to the engine: -1 for flag
// names on the "+ flag" slot.
func (m *Model) engineFocus() int {
	if m.onAddSlot() {
		return -1
	}
	return m.focus
}

// historyCands returns the focused slot's values from history, or the flags
// not yet in the command on the "+ flag" slot.
func (m *Model) historyCands() []history.Candidate {
	now := time.Now()
	if !m.onAddSlot() {
		return m.o.History.Candidates(m.cmd.Name, m.cmd.Slots[m.focus].Key, now)
	}
	var out []history.Candidate
	for _, c := range m.o.History.Candidates(m.cmd.Name, history.FlagsKey, now) {
		if m.cmd.Find(m.flagKey(c.Value)) < 0 {
			out = append(out, c)
		}
	}
	return out
}

// specCands returns the focused slot's values from the spec, or the spec's
// flags not yet in the command on the "+ flag" slot.
func (m *Model) specCands() []string {
	if !m.onAddSlot() {
		return m.specValues(m.cmd.Slots[m.focus])
	}
	var out []string
	if m.sp != nil {
		for _, f := range m.sp.Flags {
			if m.cmd.Find(f.Name) < 0 {
				out = append(out, "--"+f.Name)
			}
		}
	}
	return out
}

// sources returns the sources Ctrl-S cycles through for the focused slot,
// starting with "" for all of them merged.
func (m *Model) sources(hist []history.Candidate, vals []string) []string {
	out := []string{""}
	if len(hist) > 0 {
		out = append(out, "history")
	}
	if len(vals) > 0 {
		out = append(out, "spec")
	}
	return append(out, m.o.Engine.Sources(m.cmd, m.sp, m.engineFocus())...)
}

// nextSource switches to the source after the active one and reloads.
func (m *Model) nextSource() {
	srcs := m.sources(m.historyCands(), m.specCands())
	i := slices.Index(srcs, m.active)
	m.source = srcs[(i+1)%len(srcs)]
	m.sel = -1
	m.load()
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

// Update handles input and completion results, and starts the spinner when
// a load begins.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasLoading := m.loading
	tm, cmd := m.update(msg)
	if m.loading && !wasLoading {
		cmd = tea.Batch(cmd, m.spin.Tick)
	}
	return tm, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		// Dropping ticks once loaded stops the spinner.
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
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
		m.selectPin()
		if msg.Done && m.cycle != nil && m.cycle.id == msg.ID {
			m.finishCycle()
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sp != nil {
		for _, t := range m.sp.Transforms {
			if t.Binding() == k.String() {
				// Keep a value picked on the focused slot, as Tab does.
				m.commit()
				if t.Op == "focus" {
					m.focusSlot(t.Target())
					return m, nil
				}
				err := transform.Apply(m.cmd, m.sp, t)
				if errors.Is(err, transform.ErrNoValues) {
					m.autoCycle(t.Target())
					return m, nil
				}
				if err != nil {
					m.errText = err.Error()
					return m, nil
				}
				if t.Op == "cycle" {
					m.afterCycle(t.Target())
				} else {
					m.refocus()
				}
				return m, nil
			}
		}
	}
	// Any other key is the user taking over the selection.
	m.pin = ""
	switch m.act[k.String()] {
	case ActCancel:
		m.cancelled, m.quitting = true, true
		return m, tea.Quit
	case ActBack:
		if m.query != "" || m.sel >= 0 {
			m.query, m.sel = "", -1
			m.filter()
			return m, nil
		}
		m.cancelled, m.quitting = true, true
		return m, tea.Quit
	case ActAccept:
		m.commit()
		m.quitting = true
		return m, tea.Quit
	case ActRun:
		m.commit()
		m.run, m.quitting = true, true
		return m, tea.Quit
	case ActNextSlot:
		if m.commit() {
			// Added a flag that takes a value: stay on it.
			m.refocus()
			return m, nil
		}
		m.focus = (m.focus + 1) % (len(m.cmd.Slots) + 1)
		m.refocus()
	case ActPrevSlot:
		m.commit()
		m.focus = (m.focus + len(m.cmd.Slots)) % (len(m.cmd.Slots) + 1)
		m.refocus()
	case ActUp:
		if m.sel > -1 {
			m.sel--
		}
	case ActDown:
		if m.sel < len(m.rows)-1 {
			m.sel++
		}
	case ActSource:
		m.nextSource()
	case ActDrop:
		// Delete the focused slot.
		if !m.onAddSlot() {
			m.cmd.Remove(m.focus)
			m.refocus()
		}
	case ActClear:
		m.query, m.sel = "", -1
		m.filter()
	default:
		m.typeKey(k)
	}
	return m, nil
}

// typeKey edits the search with keys that aren't bound to an action.
func (m *Model) typeKey(k tea.KeyMsg) {
	switch k.Type {
	case tea.KeyBackspace:
		if m.query != "" {
			r := []rune(m.query)
			m.query = string(r[:len(r)-1])
			m.filter()
			if m.query == "" {
				m.sel = -1
			}
		}
	case tea.KeySpace:
		m.query += " "
		m.filter()
	case tea.KeyRunes:
		if !k.Alt {
			m.query += string(k.Runes)
			m.filter()
		}
	}
}

// focusSlot moves to the slot with key, adding it empty if the command
// doesn't have it, and loads its candidates.
func (m *Model) focusSlot(key string) {
	i := m.cmd.Find(key)
	if i < 0 {
		s, err := transform.NewSlot(m.cmd, m.sp, key)
		if err != nil {
			m.errText = err.Error()
			return
		}
		i = m.cmd.Add(s)
	}
	m.focus = i
	m.refocus()
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
	if i != m.focus || added {
		m.focus = i
		m.refocus()
	}
	m.cycle = &pendingCycle{id: m.reqID, key: key, added: added}
	// History and spec sources load synchronously.
	if !m.loading {
		m.finishCycle()
	}
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
	m.afterCycle(c.key)
}

// afterCycle focuses the cycled slot and selects its new value in the list.
// A flag cycled to absent has no slot, so focus stays where it was.
func (m *Model) afterCycle(key string) {
	if i := m.cmd.Find(key); i >= 0 {
		m.focus = i
	}
	m.refocus()
	if i := m.cmd.Find(key); i >= 0 && i == m.focus {
		m.pin = m.cmd.Slots[i].Value
		m.selectPin()
	}
}

// selectPin selects the row for the pinned value, if it has arrived.
func (m *Model) selectPin() {
	if m.pin == "" || m.query != "" {
		return
	}
	for i, r := range m.rows {
		if r.value == m.pin {
			m.sel = i
			return
		}
	}
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

// View renders the editor in a box, with the key legend below a separator.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	w := m.innerWidth()
	body := strings.Split(ansi.Wrap(m.preview(), w, ""), "\n")

	label := "+ flag"
	ls := m.st.flag
	if !m.onAddSlot() {
		s := m.cmd.Slots[m.focus]
		label, ls = s.Label, m.kindStyle(s.Kind)
	}
	if m.active != "" {
		label += " [" + m.active + "]"
	}
	body = append(body, fmt.Sprintf("%s %s▏", ls.Bold(true).Render(label+":"), m.query))

	start := 0
	if m.sel >= listHeight {
		start = m.sel - listHeight + 1
	}
	for i := start; i < len(m.rows) && i < start+listHeight; i++ {
		body = append(body, m.renderRow(i))
	}
	switch {
	case m.loading:
		// Also shown over cached candidates while they are refreshed.
		body = append(body, "  "+m.spin.View())
	case len(m.rows) == 0:
		body = append(body, m.st.dim.Render("  (no candidates, type a value)"))
	}
	if m.message != "" {
		body = append(body, m.st.dim.Render("  "+firstLine(m.message)))
	}
	if m.errText != "" {
		body = append(body, m.st.err.Render("  "+firstLine(m.errText)))
	}
	return m.box(body, m.legend(w))
}

// innerWidth is the width of the text inside the box.
func (m *Model) innerWidth() int {
	return max(m.width-4, 10)
}

// box draws a rounded border around body and legend, with a separator
// joined to the border between them. Lines are padded or truncated to fit.
func (m *Model) box(body, legend []string) string {
	w := m.innerWidth()
	bar := strings.Repeat("─", w+2)
	side := m.st.border.Render("│")
	line := func(s string) string {
		s = ansi.Truncate(s, w, "…")
		return side + " " + s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) + " " + side
	}
	out := []string{m.st.border.Render("╭" + bar + "╮")}
	for _, s := range body {
		out = append(out, line(s))
	}
	if len(legend) > 0 {
		out = append(out, m.st.border.Render("├"+bar+"┤"))
		for _, s := range legend {
			out = append(out, line(s))
		}
	}
	out = append(out, m.st.border.Render("╰"+bar+"╯"))
	return strings.Join(out, "\n")
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
		line += "  " + m.st.dim.Render(truncate(extra, max(m.innerWidth()-lipgloss.Width(line)-2, 10)))
	}
	return line
}

// kindStyle is the color of a slot of kind k in the preview.
func (m *Model) kindStyle(k model.Kind) lipgloss.Style {
	if k == model.Positional {
		return m.st.arg
	}
	return m.st.flag
}

// preview renders the command with the selected candidate applied, slots
// colored by kind and the focused slot highlighted.
func (m *Model) preview() string {
	parts := []string{m.st.head.Render(m.cmd.Head.Raw)}
	for i, s := range m.cmd.Slots {
		focused := i == m.focus
		if focused && m.sel >= 0 && m.sel < len(m.rows) {
			s.Value, s.Dirty = m.rows[m.sel].value, true
		}
		name, sep, val := slotParts(s)
		if name+sep+val == "" {
			if focused {
				parts = append(parts, m.kindStyle(s.Kind).Reverse(true).Render("<"+s.Label+">"))
			}
			continue
		}
		parts = append(parts, m.renderSlot(s.Kind, name, sep, val, focused))
	}
	if m.onAddSlot() {
		v := "+flag"
		if m.sel >= 0 && m.sel < len(m.rows) {
			v = m.rows[m.sel].value
		}
		parts = append(parts, m.st.flag.Reverse(true).Render(v))
	}
	line := strings.Join(parts, " ")
	if m.o.Prompt != "" {
		line = m.st.prompt.Render(m.o.Prompt) + " " + line
	}
	return line
}

// renderSlot colors a slot's flag name and value, reversed if focused.
func (m *Model) renderSlot(k model.Kind, name, sep, val string, focused bool) string {
	ns, vs, ss := m.st.flag, m.st.value, lipgloss.NewStyle()
	if k == model.Positional {
		vs = m.st.arg
	}
	if focused {
		ns, vs, ss = ns.Reverse(true), vs.Reverse(true), m.st.focus
	}
	var out string
	if name != "" {
		out += ns.Render(name)
	}
	if sep != "" {
		out += ss.Render(sep)
	}
	if val != "" {
		out += vs.Render(val)
	}
	return out
}

// slotParts splits a slot's rendered text into the flag name, the
// separator ("=", spaces or nothing) and the value. Positionals have only a
// value and bool flags only a name. Text that doesn't start with the flag's
// name is returned whole as the name.
func slotParts(s model.Slot) (name, sep, value string) {
	r := s.Render()
	switch {
	case r == "":
		return "", "", ""
	case s.Kind == model.Positional:
		return "", "", r
	case s.Kind == model.Bool, s.Name == "", !strings.HasPrefix(r, s.Name):
		return r, "", ""
	}
	rest := r[len(s.Name):]
	if strings.HasPrefix(rest, "=") {
		sep = "="
	} else {
		sep = rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
	}
	return s.Name, sep, rest[len(sep):]
}

// helpGroups returns the legend items: the editor's keys, then the
// plugin's shortcuts.
func (m *Model) helpGroups() [][]string {
	groups := [][]string{m.keys.helpItems()}
	if m.sp != nil {
		var ts []string
		for _, t := range m.sp.Transforms {
			d := t.Desc
			if d == "" {
				d = t.Op + " " + t.Target()
			}
			ts = append(ts, keyLabel(t.Binding())+" "+d)
		}
		if len(ts) > 0 {
			groups = append(groups, ts)
		}
	}
	return groups
}

func (m *Model) help() string {
	var lines []string
	for _, g := range m.helpGroups() {
		lines = append(lines, strings.Join(g, " · "))
	}
	return m.st.dim.Render(strings.Join(lines, "\n"))
}

// legend returns the help items packed into lines of at most w columns,
// breaking only between items. Each group starts a new line.
func (m *Model) legend(w int) []string {
	var out []string
	for _, g := range m.helpGroups() {
		cur := ""
		for _, it := range g {
			switch {
			case cur == "":
				cur = it
			case ansi.StringWidth(cur+" · "+it) <= w:
				cur += " · " + it
			default:
				out = append(out, m.st.dim.Render(cur))
				cur = it
			}
		}
		if cur != "" {
			out = append(out, m.st.dim.Render(cur))
		}
	}
	return out
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
