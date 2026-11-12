package bash

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/amimof/rx/internal/shell/proto"
)

func TestScript(t *testing.T) {
	s := Script(Options{Bin: "/opt/it's/rx", Key: "^[e", Log: "/tmp/rx.log"})
	if want := "_rx_bin='/opt/it'\\''s/rx'\n_rx_key='^[e'\n_rx_log='/tmp/rx.log'\n"; !strings.HasPrefix(s, want) {
		t.Fatalf("header = %q", strings.Join(strings.SplitN(s, "\n", 4)[:3], "\n"))
	}
	if s := Script(Options{Bin: "rx"}); strings.Contains(s, "_rx_key=") {
		t.Error("no key should leave the widget's default")
	}
	for _, want := range []string{"bind -m", "-x", "READLINE_LINE", "mkfifo", "--shell bash"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if strings.Contains(s, "coproc") {
		t.Error("script uses coproc, which would replace the user's coprocess")
	}
}

// bashrc sets up fixture commands and completion specs. %[1]s is the test
// directory, %[2]s the rx binary and %[3]s the widget script.
const bashrc = `
# The driver waits for this pid to exit before the test removes the dir.
echo $$ > %[1]s/pid
stty rows 30 cols 120
PS1='> '
HISTFILE=%[1]s/hist
HISTCONTROL=

ran() { printf '%%s\n' "$*" >> %[1]s/ran; }

_fixture() { COMPREPLY=($(compgen -W 'alpha beta' -- "$2")); }
fixture() { ran fixture "$@"; }
complete -F _fixture fixture
alias fx=fixture

# Completes to the whole line, to check escaping in both directions.
_echofix() { COMPREPLY=("$COMP_LINE"); }
echofix() { ran echofix "$@"; }
complete -F _echofix echofix

_bigfix() { COMPREPLY=(v{1..1500}); }
complete -F _bigfix bigfix

# Shaped like cobra's and clap's generated functions: the protocol call is
# in a helper, not in the registered function.
__start_cobrafix() { __cobrafix_handle_word; }
__cobrafix_handle_word() { __cobrafix_get_completion_results; }
__cobrafix_get_completion_results() { local out; out=$(cobrafix __complete "${COMP_WORDS[@]:1}"); }
complete -o default -F __start_cobrafix cobrafix
_clap_complete_clapfix() { local _CLAP_COMPLETE_INDEX=$COMP_CWORD; COMPREPLY=(); }
complete -F _clap_complete_clapfix clapfix

complete -W 'red green blue' colorfix
# Shows what the function was given: $2, the current word and its index.
_argfix() { COMPREPLY=("[$2|${COMP_WORDS[COMP_CWORD]}|$COMP_CWORD|$COMP_TYPE]"); }
complete -F _argfix argfix
# bash-completion's on-demand loader.
_comp_load() { [[ $2 == lazyfix ]] && complete -W 'loaded' lazyfix; }

export RX_OUT=%[1]s RX_SCRIPT=%[1]s/script RX_LOG=%[1]s/log
# The fifo directory is created here, so tests can check it is cleaned up.
export XDG_RUNTIME_DIR=%[1]s/run
mkdir -p $XDG_RUNTIME_DIR
_rx_bin=%[2]s
source %[3]s
`

type shell struct {
	t       *testing.T
	dir     string
	zsh, sh string
}

// newShell prepares a .bashrc running the widget with bin as rx. Tests are
// skipped without bash 4.1+, or without zsh, whose zpty module drives the
// terminal.
func newShell(t *testing.T, bin string) *shell {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed (needed to drive the terminal)")
	}
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	out, err := exec.Command(sh, "-c", `echo "${BASH_VERSINFO[0]} ${BASH_VERSINFO[1]}"`).Output()
	if err != nil {
		t.Skipf("bash: %v", err)
	}
	var major, minor int
	if _, err := fmt.Sscan(string(out), &major, &minor); err != nil || major < 4 || major == 4 && minor < 1 {
		t.Skipf("bash %s is older than 4.1", strings.TrimSpace(string(out)))
	}
	dir := t.TempDir()
	script, _ := filepath.Abs("rx.bash")
	if err := os.WriteFile(filepath.Join(dir, ".bashrc"), fmt.Appendf(nil, bashrc, dir, bin, script), 0o600); err != nil {
		t.Fatal(err)
	}
	return &shell{t: t, dir: dir, zsh: zsh, sh: sh}
}

// fake returns a shell whose rx sends the given requests.
func fake(t *testing.T, requests ...string) *shell {
	t.Helper()
	bin, _ := filepath.Abs("../testdata/fake-rx")
	s := newShell(t, bin)
	s.write("script", strings.Join(requests, "\n")+"\n")
	return s
}

func (s *shell) write(name, data string) {
	if err := os.WriteFile(filepath.Join(s.dir, name), []byte(data), 0o700); err != nil {
		s.t.Fatal(err)
	}
}

// rc appends lines to .bashrc, after the widget script has been sourced.
func (s *shell) rc(lines string) {
	f, err := os.OpenFile(filepath.Join(s.dir, ".bashrc"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		s.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(lines + "\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shell) read(name string) string {
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		s.t.Fatalf("%s: %v\ntranscript:\n%q", name, err, s.transcript())
	}
	return string(b)
}

// checkCleanedUp fails if the widget left its fifo directory behind.
func (s *shell) checkCleanedUp() {
	s.t.Helper()
	left, err := os.ReadDir(filepath.Join(s.dir, "run"))
	if err != nil {
		s.t.Fatal(err)
	}
	for _, e := range left {
		s.t.Errorf("left behind in XDG_RUNTIME_DIR: %s", e.Name())
	}
}

func (s *shell) transcript() string {
	b, _ := os.ReadFile(filepath.Join(s.dir, "transcript"))
	return string(b)
}

// run plays steps (see ../testdata/driver.zsh) after the first prompt.
func (s *shell) run(steps ...string) {
	s.t.Helper()
	args := append([]string{"-f", "../testdata/driver.zsh", s.dir, s.sh, "expect:> "}, steps...)
	out, err := exec.Command(s.zsh, args...).CombinedOutput()
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(s.dir, "log"))
		s.t.Fatalf("driver: %v %s\ntranscript:\n%q\nrx log:\n%s", err, out, s.transcript(), log)
	}
}

// typeAndEdit types line, then presses Ctrl-X Ctrl-X.
func typeAndEdit(line string) []string {
	return []string{"key:" + line, "key:\x18\x18"}
}

// accept presses Enter and waits for the command to be recorded.
func (s *shell) accept() []string {
	return []string{"key:\r", "file:" + filepath.Join(s.dir, "ran")}
}

func req(fields ...string) string {
	for i := range fields {
		fields[i] = proto.Escape(fields[i])
	}
	return strings.Join(fields, "\t")
}

// noise fails if job notices or broken pipe errors reached the terminal.
func (s *shell) noise() {
	s.t.Helper()
	tr := s.transcript()
	for _, bad := range []string{"[1]", "Done", "Broken pipe", "broken pipe"} {
		if strings.Contains(tr, bad) {
			s.t.Errorf("%q leaked to the terminal:\n%q", bad, tr)
		}
	}
}

func TestWidgetProtocol(t *testing.T) {
	s := fake(t,
		req("INFO", "fx", "__complete", "_CLAP_COMPLETE_INDEX"),
		req("INFO", "cobrafix", "__complete", "_CLAP_COMPLETE_INDEX"),
		req("INFO", "clapfix", "__complete", "_CLAP_COMPLETE_INDEX"),
		req("INFO", "nosuchcmd", "__complete"),
		req("COMPLETE", "fixture "),
		req("COMPLETE", "fx al"),
		req("DONE", "13", "fixture alpha"),
	)
	s.run(append(typeAndEdit("fixture b"), s.accept()...)...)

	if got := s.read("args"); got != "edit --shell bash --cursor 9 -- fixture b\n" {
		t.Errorf("rx args = %q", got)
	}
	for i, want := range []string{
		"I\tfixture\t_fixture\t\nEND\n",
		"I\t\t__start_cobrafix\t__complete\nEND\n",
		"I\t\t_clap_complete_clapfix\t_CLAP_COMPLETE_INDEX\nEND\n",
		"I\t\t\t\nEND\n",
	} {
		if got := s.read(fmt.Sprintf("resp.%d", i+1)); got != want {
			t.Errorf("INFO %d = %q, want %q", i+1, got, want)
		}
	}
	if got := s.read("resp.5"); got != "C\talpha\t\t\nC\tbeta\t\t\nEND\n" {
		t.Errorf("COMPLETE = %q", got)
	}
	// Completion through the alias filters by the typed prefix.
	if got := s.read("resp.6"); got != "C\talpha\t\t\nEND\n" {
		t.Errorf("COMPLETE via alias = %q", got)
	}
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}
	s.noise()
	s.checkCleanedUp()
}

// The kinds of completion specs bash has, beyond -F.
func TestWidgetCompletionSpecs(t *testing.T) {
	lines := []string{
		"colorfix g",    // -W
		"argfix --opt=", // break characters: $2 is empty after "="
		"argfix 'a b",   // quotes keep a word together
		"lazyfix ",      // loaded on demand
		"fixtur",        // command position: command names
	}
	var reqs []string
	for _, l := range lines {
		reqs = append(reqs, req("COMPLETE", l))
	}
	s := fake(t, append(reqs, req("CANCEL"))...)
	s.run(append(typeAndEdit("fixture"), "file:"+filepath.Join(s.dir, "resp."+strconv.Itoa(len(lines))), "sleep:0.2")...)
	for i, want := range []string{
		"C\tgreen\t\t\nEND\n",
		"C\t[|=|2|37]\t\t\nEND\n",
		"C\t[a b|'a b|1|37]\t\t\nEND\n",
		"C\tloaded\t\t\nEND\n",
		"C\tfixture\t\tcommands\n",
	} {
		if got := s.read(fmt.Sprintf("resp.%d", i+1)); !strings.Contains(got, want) {
			t.Errorf("COMPLETE %q = %q, want %q", lines[i], got, want)
		}
	}
}

// The widget exports aliases and the history file, flushed so this
// session's commands are in it.
func TestWidgetEnv(t *testing.T) {
	s := newShell(t, "")
	stub := filepath.Join(s.dir, "stub-rx")
	s.write("stub-rx", "#!/bin/sh\ncp \"$RX_HISTFILE\" \"$RX_OUT/histcopy\"\nprintf '%s' \"$RX_ALIASES\" > \"$RX_OUT/aliases\"\n")
	s.rc("_rx_bin=" + stub + "\nalias tabby=$'a\\tb'")
	s.run("key:fixture one\r", "file:"+filepath.Join(s.dir, "ran"), "key:fixture two", "key:\x18\x18", "file:"+filepath.Join(s.dir, "aliases"), "sleep:0.2")
	if got := s.read("histcopy"); !strings.Contains(got, "fixture one\n") {
		t.Errorf("history seen by rx = %q", got)
	}
	al := strings.Split(s.read("aliases"), "\n")
	for _, want := range []string{"fx=fixture", `tabby=a\tb`} {
		found := false
		for _, a := range al {
			found = found || a == want
		}
		if !found {
			t.Errorf("RX_ALIASES %q missing %q", al, want)
		}
	}
}

// DONE with the run field runs the command without Enter being pressed;
// without it the command only goes back into the prompt.
func TestWidgetRun(t *testing.T) {
	s := fake(t, req("DONE", "13", "fixture alpha", "run"))
	s.run(append(typeAndEdit("fixture b"), "file:"+filepath.Join(s.dir, "ran"))...)
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}
	s.checkCleanedUp()

	// A plain DONE afterwards must not run: the second key of the macro is
	// rebound on every use.
	s = fake(t, req("DONE", "13", "fixture alpha"))
	s.rc("_rx_bindafter accept-line")
	s.run(append(typeAndEdit("fixture b"), "sleep:0.5")...)
	if _, err := os.Stat(filepath.Join(s.dir, "ran")); err == nil {
		t.Errorf("plain DONE ran the command: %q", s.read("ran"))
	}
}

func TestWidgetEscaping(t *testing.T) {
	// Each value goes rx → bash (unescaped into the line) and back (escaped
	// candidate), so it must survive both directions intact.
	vals := []string{`echofix a\cb`, "echofix tab\there", `echofix back\\slash\n`, `echofix \x41\e`, `echofix trailing\`}
	var reqs []string
	for _, v := range vals {
		reqs = append(reqs, req("COMPLETE", v))
	}
	s := fake(t, append(reqs, req("CANCEL"))...)
	s.run(append(typeAndEdit("echofix"), "file:"+filepath.Join(s.dir, fmt.Sprintf("resp.%d", len(vals))), "sleep:0.2")...)
	for i, v := range vals {
		want := fmt.Sprintf("C\t%s\t", proto.Escape(v))
		if got := s.read(fmt.Sprintf("resp.%d", i+1)); !strings.HasPrefix(got, want) {
			t.Errorf("value %q: response %q, want prefix %q", v, got, want)
		}
	}
}

func TestWidgetLargeLists(t *testing.T) {
	s := fake(t, req("COMPLETE", "bigfix "), req("CANCEL"))
	s.run(append(typeAndEdit("bigfix"), "file:"+filepath.Join(s.dir, "resp.1"), "sleep:0.3")...)
	if n := strings.Count(s.read("resp.1"), "\nC\t") + 1; n != 1500 {
		t.Errorf("got %d of 1500 candidates", n)
	}
	if tr := s.transcript(); strings.Contains(tr, "v1499") {
		t.Errorf("candidates leaked to the terminal:\n%q", tr)
	}
}

// After cancel or an rx crash, the line and cursor must be unchanged, the
// shell must survive writing to a dead rx, and nothing may be printed over
// the prompt.
func TestWidgetRestoresBuffer(t *testing.T) {
	for name, script := range map[string][]string{
		"cancel":             {req("CANCEL")},
		"crash":              {"DIE"},
		"crash mid-request":  {"DIEAFTER " + req("COMPLETE", "bigfix ")},
		"crash after result": {req("COMPLETE", "fixture "), "DIE"},
	} {
		t.Run(name, func(t *testing.T) {
			s := fake(t, script...)
			// Cursor goes back two characters before Ctrl-X Ctrl-X; "XY" typed
			// afterwards must land there.
			steps := append([]string{"key:fixture beta", "key:\x02\x02"}, typeAndEdit("")...)
			steps = append(steps, "sleep:0.3", "key:XY")
			s.run(append(steps, s.accept()...)...)
			if got := s.read("ran"); got != "fixture beXYta\n" {
				t.Errorf("ran %q", got)
			}
			s.noise()
			s.checkCleanedUp()
		})
	}
}

// Job control and the SIGPIPE trap are switched off while rx runs and must
// be back afterwards.
func TestWidgetRestoresShellState(t *testing.T) {
	s := fake(t, req("CANCEL"))
	s.rc("trap 'echo piped' PIPE")
	s.run(append(typeAndEdit("ran $- \"$(trap -p PIPE)\""), "sleep:0.3", "key:\r", "file:"+filepath.Join(s.dir, "ran"))...)
	got := s.read("ran")
	if flags, trap, _ := strings.Cut(strings.TrimSpace(got), " "); !strings.Contains(flags, "m") || trap != "trap -- 'echo piped' SIGPIPE" {
		t.Errorf("after the widget: %q", got)
	}
}

// The cursor rx gets and sends back counts characters.
func TestWidgetCursor(t *testing.T) {
	// DONE's cursor 9 is right after "é", where X is typed next.
	s := fake(t, req("DONE", "9", "fixture é b"))
	steps := append([]string{"key:fixture é b", "key:\x02"}, typeAndEdit("")...)
	s.run(append(append(steps, "sleep:0.3", "key:X"), s.accept()...)...)
	// The cursor was before "b": 10 characters, 11 bytes.
	if got := s.read("args"); got != "edit --shell bash --cursor 10 -- fixture é b\n" {
		t.Errorf("rx args = %q", got)
	}
	if got := s.read("ran"); got != "fixture éX b\n" {
		t.Errorf("ran %q", got)
	}
}

// The widget is bound in every keymap, so vi mode enabled after loading rx
// still works, and rx-bind rebinds.
func TestWidgetKeymaps(t *testing.T) {
	for name, extra := range map[string]string{
		"vi insert":   "set -o vi",
		"vi command":  "set -o vi",
		"rx-bind key": "rx-bind '^Xr'",
		"readline":    `rx-bind '\C-xr'`,
		// rx init bakes the config's key into _rx_key; RX_KEY overrides it.
		"config key":  "_rx_key='^Xr'; rx-bind",
		"RX_KEY wins": "_rx_key='^Xe'; RX_KEY='^Xr'; rx-bind",
	} {
		t.Run(name, func(t *testing.T) {
			s := fake(t, req("DONE", "13", "fixture alpha"))
			s.rc(extra + "\nbind 'set keyseq-timeout 200'")
			steps := []string{"key:fixture b"}
			switch name {
			case "vi command":
				steps = append(steps, "key:\x1b", "sleep:0.4", "key:\x18\x18")
			case "rx-bind key", "readline", "config key", "RX_KEY wins":
				steps = append(steps, "key:\x18r")
			default:
				steps = append(steps, "key:\x18\x18")
			}
			s.run(append(steps, "sleep:0.3", "key:\r", "file:"+filepath.Join(s.dir, "ran"))...)
			if got := s.read("ran"); got != "fixture alpha\n" {
				t.Errorf("ran %q", got)
			}
		})
	}
}

// Failures are shown below the prompt instead of silently doing nothing.
func TestWidgetErrorMessages(t *testing.T) {
	tests := []struct {
		name   string
		script []string
		line   string
		rc     string
		want   string
	}{
		{"rx error", []string{req("ERROR", "boom: no such thing")}, "fixture b", "", "rx: boom: no such thing"},
		{"rx crash", []string{"DIE"}, "fixture b", "", "rx: exited unexpectedly"},
		{"empty prompt", nil, "", "", "rx: nothing to edit"},
		{"missing binary", nil, "fixture b", "_rx_bin=/nonexistent/rx", "rx: binary not found: /nonexistent/rx"},
		{"no temp dir", nil, "fixture b", "XDG_RUNTIME_DIR=/nonexistent", "rx: cannot create a temporary directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fake(t, tt.script...)
			if tt.rc != "" {
				s.rc(tt.rc)
			}
			// bash redraws the prompt below the message; the line is kept.
			s.run(append(typeAndEdit(tt.line), "expect:"+tt.want, "key:\r", "sleep:0.3")...)
			if tt.line != "" {
				if got := s.read("ran"); got != tt.line+"\n" {
					t.Errorf("ran %q", got)
				}
			}
		})
	}
}

func TestWidgetDoctor(t *testing.T) {
	s := newShell(t, "true")
	s.rc("set -o vi; rx-bind '^Xr'")
	s.run("key:rx-doctor\r", "sleep:0.5")
	tr := s.transcript()
	for _, want := range []string{"editing mode vi", `vi-insert     "\C-x\C-x" "\C-xr"`, "log          " + s.dir + "/log"} {
		if !strings.Contains(tr, want) {
			t.Errorf("rx-doctor output missing %q:\n%s", want, tr)
		}
	}
}

// bashFuncs runs code with args as $1... after sourcing the widget in a
// non-interactive bash, which defines the functions without binding keys.
func bashFuncs(t *testing.T, code string, args ...string) string {
	t.Helper()
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	cmd := exec.Command(sh, append([]string{"--norc", "-c", "source rx.bash; " + code, "_"}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out)
}

func TestWords(t *testing.T) {
	for line, want := range map[string]string{
		"cmd":                 "<cmd>",
		"cmd ":                "<cmd><>",
		"cmd  a\tb ":          "<cmd><a><b><>",
		"cmd --opt=v":         "<cmd><--opt><=><v>",
		"cmd --opt=":          "<cmd><--opt><=>",
		"cmd a>>b":            "<cmd><a><>>><b>",
		`cmd 'a b' "c \" d"`:  `<cmd><'a b'><"c \" d">`,
		`cmd a\ b`:            `<cmd><a\ b>`,
		"cmd host:path":       "<cmd><host><:><path>",
		`cmd "unterminated x`: `<cmd><"unterminated x>`,
	} {
		got := bashFuncs(t, `_rx_words "$1"; printf '<%s>' "${_rx_cw[@]}"`, line)
		if got != want {
			t.Errorf("_rx_words %q = %s, want %s", line, got, want)
		}
	}
}

func TestKeySeq(t *testing.T) {
	for key, want := range map[string]string{
		"^X^X":     `\C-x\C-x`,
		"^[e":      `\ee`,
		"^Xr":      `\C-xr`,
		"^?":       `\C-?`,
		`\C-x\C-x`: `\C-x\C-x`,
	} {
		if got := bashFuncs(t, `_rx_keyseq "$1"; printf %s "$REPLY"`, key); got != want {
			t.Errorf("_rx_keyseq %q = %q, want %q", key, got, want)
		}
	}
}

func TestEscape(t *testing.T) {
	for _, v := range []string{"plain", `a\b`, "tab\there", "new\nline", `\n literal`, `trailing\`, ""} {
		got := bashFuncs(t, `_rx_esc "$1"; e=$REPLY; _rx_unesc "$e"; printf '%s|%s' "$e" "$REPLY"`, v)
		if want := proto.Escape(v) + "|" + v; got != want {
			t.Errorf("escape %q = %q, want %q", v, got, want)
		}
	}
	if got := bashFuncs(t, `_rx_unesc 'a\xb\'; printf %s "$REPLY"`); got != proto.Unescape(`a\xb\`) {
		t.Errorf("unescape of unknown escapes = %q, want %q", got, proto.Unescape(`a\xb\`))
	}
}

// bash 4 counts READLINE_POINT in bytes; the widget converts.
func TestCursorConversion(t *testing.T) {
	got := bashFuncs(t, `l='fixture é b'; _rx_tochars "$l" 11; c=$REPLY; _rx_tobytes "$l" 9; printf '%s %s %s' "$c" "$REPLY" "${#l}"`)
	// The locale is restored afterwards: the line is 11 characters again.
	if got != "10 10 11" {
		t.Errorf("got %q, want 10 10 11", got)
	}
}

func buildRx(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rx")
	build := exec.Command("go", "build", "-o", bin, "../../../cmd/rx")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// TestWidgetEndToEnd drives the real rx binary: open the editor on
// "fixture b", filter the shell's completion to alpha and pick it with
// Ctrl-N, accept, and run with Ctrl-E.
func TestWidgetEndToEnd(t *testing.T) {
	newShell(t, "") // skip early without bash or zsh
	bin := buildRx(t)
	setup := func() *shell {
		s := newShell(t, bin)
		s.rc("export XDG_CACHE_HOME=" + s.dir + "/cache XDG_CONFIG_HOME=" + s.dir + "/config")
		return s
	}

	s := setup()
	steps := append(typeAndEdit("fixture b"),
		"expect:alpha", // the shell's completion reached the editor
		"key:alp",
		"key:\x0e", // typing selects only an exact match; Ctrl-N picks alpha
		"expect:alpha",
		"key:\r",
	)
	s.run(append(steps, s.accept()...)...)
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}

	// Ctrl-E accepts and runs.
	s = setup()
	s.run(append(typeAndEdit("fixture b"), "expect:alpha", "key:alp", "key:\x0e", "expect:alpha", "key:\x05", "file:"+filepath.Join(s.dir, "ran"))...)
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("after Ctrl-E ran %q", got)
	}

	// Esc Esc cancels and keeps the buffer.
	s = setup()
	steps = append(typeAndEdit("fixture b"), "expect:alpha", "key:x", "key:\x1b", "sleep:0.2", "key:\x1b", "sleep:0.3")
	s.run(append(steps, s.accept()...)...)
	if got := s.read("ran"); got != "fixture b\n" {
		t.Errorf("after cancel ran %q", got)
	}
}
