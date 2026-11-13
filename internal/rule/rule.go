// Package rule runs a plugin's rules: conditional transformations such as
// "logs removes the resource" for kubectl or "POST adds a body" for curl.
//
// A rule fires when an edit makes its conditions hold, and records what the
// command looked like before its actions ran. When the conditions stop
// holding, the rule is reverted from that record, so `kubectl get pods NAME`
// → logs → get gets "pods" back. Slots the user edited after the rule
// touched them are left as the user made them.
package rule

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/internal/transform"
)

// MaxRounds bounds how often Settle re-checks the rules after an edit, so
// rules that undo each other can't loop forever.
const MaxRounds = 10

// State is what Settle needs to know between edits: which rules held after
// the last one, and the rules that fired and haven't been reverted.
type State struct {
	holding map[string]bool
	active  []Activation
}

// Init returns the state for cmd as the editor opens it. Rules that already
// hold are recorded as holding, so they don't fire: opening the editor
// never changes the command.
func Init(cmd *model.Command, sp *spec.Spec) *State {
	st := &State{holding: map[string]bool{}}
	if sp == nil {
		return st
	}
	for i := range sp.Rules {
		r := &sp.Rules[i]
		st.holding[r.Name], _ = Holds(cmd, sp, r)
	}
	return st
}

// Clone returns a copy of st that can change without affecting it.
func (st *State) Clone() *State {
	out := &State{holding: map[string]bool{}, active: slices.Clone(st.active)}
	for k, v := range st.holding {
		out.holding[k] = v
	}
	return out
}

// Active returns the names of the rules that fired and haven't been
// reverted, oldest first.
func (st *State) Active() []string {
	var out []string
	for _, a := range st.active {
		out = append(out, a.Rule)
	}
	return out
}

func (st *State) isActive(name string) bool {
	return slices.ContainsFunc(st.active, func(a Activation) bool { return a.Rule == name })
}

// Holds reports whether every condition of r holds for cmd, and if not,
// why: "needs " and the first condition that doesn't.
func Holds(cmd *model.Command, sp *spec.Spec, r *spec.Rule) (bool, string) {
	for _, c := range r.When {
		if !holds(cmd, sp, c) {
			return false, "needs " + c.String()
		}
	}
	return true, ""
}

// isSet reports whether a slot counts as present: a bool flag, or a slot
// with a value. Empty placeholders don't.
func isSet(s model.Slot) bool { return s.Kind == model.Bool || s.Value != "" }

func holds(cmd *model.Command, sp *spec.Spec, c spec.Cond) bool {
	if c.Not != nil {
		return !holds(cmd, sp, *c.Not)
	}
	ch, _ := c.Check()
	if ch == nil {
		return false
	}
	var vals []string
	for _, s := range cmd.Slots {
		if s.Key == ch.Name && isSet(s) {
			vals = append(vals, s.Value)
		}
	}
	if ch.Exists != nil {
		return (len(vals) > 0) == *ch.Exists
	}
	canon := func(v string) string { return sp.Canonical(ch.Name, v) }
	var re *regexp.Regexp
	if ch.Matches != "" {
		var err error
		if re, err = regexp.Compile(ch.Matches); err != nil {
			return false
		}
	}
	// A repeated flag matches if any of its values does.
	return slices.ContainsFunc(vals, func(v string) bool {
		switch {
		case ch.Equals != "":
			return canon(v) == canon(ch.Equals)
		case ch.In != nil:
			return slices.ContainsFunc(ch.In, func(w string) bool { return canon(w) == canon(v) })
		case re != nil:
			return re.MatchString(v)
		}
		return false
	})
}

// holdsWithout reports whether r, which fired as a, still holds on cmd
// without a's own changes, so a rule whose actions touch the slots it tests
// (removing the resource it checked is a pod) doesn't revert itself.
func holdsWithout(cmd *model.Command, sp *spec.Spec, r *spec.Rule, a Activation) bool {
	c := cmd.Clone()
	Revert(c, a)
	ok, _ := Holds(c, sp, r)
	return ok
}

// Result is what Settle did.
type Result struct {
	// Log describes each rule fired or reverted, e.g. "post-adds-body:
	// added --data".
	Log []string
	// Focus is the ID of a slot a rule added without a value, for the
	// editor to focus; 0 if none.
	Focus int
}

// Settle brings the rules up to date after an edit to cmd: it reverts the
// active rules whose conditions no longer hold, newest first, then fires
// the rules whose conditions have started to hold. An active rule is judged
// on the command without its own changes. It repeats until nothing changes,
// at most MaxRounds times.
func Settle(cmd *model.Command, sp *spec.Spec, st *State) Result {
	var res Result
	if sp == nil || len(sp.Rules) == 0 {
		return res
	}
	cmd.AssignIDs()
	for range MaxRounds {
		changed := false
		for i := len(st.active) - 1; i >= 0; i-- {
			a := st.active[i]
			if r := sp.Rule(a.Rule); r != nil && holdsWithout(cmd, sp, r, a) {
				continue
			}
			res.Log = append(res.Log, Revert(cmd, a))
			st.active = slices.Delete(st.active, i, i+1)
			st.holding[a.Rule] = false
			changed = true
		}
		for i := range sp.Rules {
			r := &sp.Rules[i]
			if st.isActive(r.Name) {
				continue
			}
			ok, _ := Holds(cmd, sp, r)
			was := st.holding[r.Name]
			st.holding[r.Name] = ok
			if !ok || was {
				continue
			}
			a := Fire(cmd, sp, r)
			st.active = append(st.active, a)
			res.Log = append(res.Log, a.String())
			if id := a.focus(); id != 0 {
				res.Focus = id
			}
			changed = true
		}
		if !changed {
			return res
		}
	}
	res.Log = append(res.Log, fmt.Sprintf("rules stopped after %d rounds", MaxRounds))
	return res
}

type kind int

const (
	added kind = iota
	removed
	changed
)

// change records one slot a rule touched.
type change struct {
	kind  kind
	label string
	// slot is the slot as the rule left it, or for removed, the slot as
	// it was.
	slot model.Slot
	// old is the slot before a changed value.
	old model.Slot
	// prev and next are the IDs of a removed slot's neighbours, 0 if none,
	// to put it back where it was.
	prev, next int
}

// Activation records what a rule's actions changed.
type Activation struct {
	Rule    string
	changes []change
}

// String describes the activation, e.g. "post-adds-body: added --data".
func (a Activation) String() string {
	var parts []string
	for _, c := range a.changes {
		switch c.kind {
		case added:
			parts = append(parts, "added "+c.label)
		case removed:
			parts = append(parts, "removed "+c.label)
		case changed:
			parts = append(parts, "set "+c.label)
		}
	}
	if len(parts) == 0 {
		return a.Rule + ": no change"
	}
	return a.Rule + ": " + strings.Join(parts, ", ")
}

// focus returns the ID of a positional the rule added or set without a
// value, or 0.
func (a Activation) focus() int {
	for _, c := range a.changes {
		if c.kind != removed && c.slot.Kind == model.Positional && c.slot.Value == "" {
			return c.slot.ID
		}
	}
	return 0
}

// data returns the template data for cmd: each slot's first value by key.
func data(cmd *model.Command) map[string]string {
	out := map[string]string{}
	for _, s := range cmd.Slots {
		if _, ok := out[s.Key]; !ok && s.Value != "" {
			out[s.Key] = s.Value
		}
	}
	return out
}

func render(text string, cmd *model.Command) string {
	if text == "" {
		return ""
	}
	t, err := spec.ParseTemplate(text)
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data(cmd)); err != nil {
		return ""
	}
	return buf.String()
}

// Fire runs r's actions on cmd, without checking its conditions, and
// returns what they changed.
func Fire(cmd *model.Command, sp *spec.Spec, r *spec.Rule) Activation {
	cmd.AssignIDs()
	a := Activation{Rule: r.Name}
	for _, act := range r.Then {
		op, c := act.Op()
		if c == nil {
			continue
		}
		key, flag := c.Key()
		switch op {
		case "add":
			a.add(cmd, sp, c, key, flag)
		case "remove":
			a.remove(cmd, c, key)
		case "set":
			v := render(c.Value, cmd)
			if v == "" {
				continue
			}
			if i := cmd.Find(key); i >= 0 {
				a.set(cmd, i, c, v)
			} else {
				a.insert(cmd, sp, c, key, v)
			}
		}
	}
	return a
}

// add adds the slot. A positional that is already there gets the value; a
// flag is added again, as flags like curl's -H repeat. With unless_present,
// a slot that has a value is left alone, and a bool flag always is.
func (a *Activation) add(cmd *model.Command, sp *spec.Spec, c *spec.Change, key string, flag bool) {
	v := render(c.Value, cmd)
	first, set := -1, -1
	for i, s := range cmd.Slots {
		if s.Key != key {
			continue
		}
		if first < 0 {
			first = i
		}
		if set < 0 && isSet(s) {
			set = i
		}
	}
	switch {
	case set >= 0 && (c.UnlessPresent || cmd.Slots[set].Kind == model.Bool):
	case !flag && first >= 0:
		a.set(cmd, first, c, v)
	case flag && first >= 0 && set < 0 && cmd.Slots[first].Kind == model.Flag:
		// A flag added without a value yet.
		a.set(cmd, first, c, v)
	default:
		a.insert(cmd, sp, c, key, v)
	}
}

func (a *Activation) insert(cmd *model.Command, sp *spec.Spec, c *spec.Change, key, v string) {
	s, err := transform.NewSlot(cmd, sp, key)
	if err != nil {
		return
	}
	s.Value, s.Dirty = v, true
	var i int
	if s.Kind == model.Positional {
		i = cmd.InsertPositional(s)
	} else {
		i = cmd.Add(s)
	}
	a.changes = append(a.changes, change{kind: added, label: c.Label(), slot: cmd.Slots[i]})
}

func (a *Activation) set(cmd *model.Command, i int, c *spec.Change, v string) {
	old := cmd.Slots[i]
	if old.Value == v && old.Value != "" {
		return
	}
	cmd.SetValue(i, v)
	a.changes = append(a.changes, change{kind: changed, label: c.Label(), slot: cmd.Slots[i], old: old})
}

func (a *Activation) remove(cmd *model.Command, c *spec.Change, key string) {
	for i := 0; i < len(cmd.Slots); {
		if cmd.Slots[i].Key != key {
			i++
			continue
		}
		ch := change{kind: removed, label: c.Label()}
		if i > 0 {
			ch.prev = cmd.Slots[i-1].ID
		}
		if i+1 < len(cmd.Slots) {
			ch.next = cmd.Slots[i+1].ID
		}
		ch.slot = cmd.Extract(i)
		a.changes = append(a.changes, ch)
	}
}

// Revert undoes a's changes to cmd, newest first, and describes what it
// did, e.g. "reverted logs-removes-resource". Slots the user changed since
// are kept as they are: a value they edited, a slot they deleted, or a slot
// they added back themselves.
func Revert(cmd *model.Command, a Activation) string {
	var kept []string
	for i := len(a.changes) - 1; i >= 0; i-- {
		c := a.changes[i]
		switch c.kind {
		case added:
			j := cmd.FindID(c.slot.ID)
			if j < 0 {
				continue
			}
			if cmd.Slots[j].Value != c.slot.Value {
				kept = append(kept, c.label)
				continue
			}
			cmd.Extract(j)
		case changed:
			j := cmd.FindID(c.slot.ID)
			if j < 0 {
				continue
			}
			if cmd.Slots[j].Value != c.slot.Value {
				kept = append(kept, c.label)
				continue
			}
			old := c.old
			old.Index = cmd.Slots[j].Index
			cmd.Slots[j] = old
		case removed:
			restore(cmd, c, &kept)
		}
	}
	msg := "reverted " + a.Rule
	if len(kept) > 0 {
		slices.Reverse(kept)
		msg += " (kept " + strings.Join(kept, ", ") + ")"
	}
	return msg
}

// restore puts a removed slot back between its old neighbours, unless the
// user has added the slot back with a value.
func restore(cmd *model.Command, c change, kept *[]string) {
	key := c.slot.Key
	if slices.ContainsFunc(cmd.Slots, func(s model.Slot) bool { return s.Key == key && isSet(s) }) {
		*kept = append(*kept, c.label)
		return
	}
	// Empty placeholders for the slot make way for it.
	for j := len(cmd.Slots) - 1; j >= 0; j-- {
		if cmd.Slots[j].Key == key {
			cmd.Extract(j)
		}
	}
	switch {
	case c.next != 0 && cmd.FindID(c.next) >= 0:
		cmd.Insert(cmd.FindID(c.next), c.slot)
	case c.prev != 0 && cmd.FindID(c.prev) >= 0:
		cmd.Insert(cmd.FindID(c.prev)+1, c.slot)
	case c.slot.Kind == model.Positional:
		cmd.InsertPositional(c.slot)
	default:
		at := len(cmd.Slots)
		if i := cmd.Find("--"); i >= 0 {
			at = i
		}
		cmd.Insert(at, c.slot)
	}
}
