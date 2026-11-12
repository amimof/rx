package fish

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amimof/rx/internal/shell/proto"
)

func TestScript(t *testing.T) {
	s := Script(Options{Bin: `/opt/it's/r\x`, Key: "^[e", Log: "/tmp/rx.log"})
	if want := "set -g _rx_bin '/opt/it\\'s/r\\\\x'\nset -g _rx_key '^[e'\nset -g _rx_log '/tmp/rx.log'\n"; !strings.HasPrefix(s, want) {
		t.Fatalf("header = %q", strings.Join(strings.SplitN(s, "\n", 4)[:3], "\n"))
	}
	if s := Script(Options{Bin: "rx"}); strings.Contains(s, "set -g _rx_key") {
		t.Error("no key should leave the widget's default")
	}
	for _, want := range []string{"function rx-edit", "commandline", "mkfifo", "--shell fish"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q", want)
		}
	}
}

// config sets up fixture commands and completions. %[1]s is the test
// directory, %[2]s the rx binary and %[3]s the widget script.
const config = `
# fish 4's --no-config also turns on private mode, which keeps history out
# of the history file.
set -ge fish_private_mode
function fish_prompt; printf '> '; end
set -g fish_greeting
set -g fish_autosuggestion_enabled 0
set -g fish_history rxtest

function ran; printf '%%s\n' "$argv" >> %[1]s/ran; end

function fixture; ran fixture $argv; end
complete -c fixture -f -a 'alpha\t"first host" beta\tsecond'
alias fx=fixture

function escfix; ran escfix $argv; end
function __escfix; printf '%%s\t%%s\n' 'a\\b' 'back\\slash' 'x\\n' 'lit\\nd'; end
complete -c escfix -f -a '(__escfix)'

complete -c bigfix -f -a '(seq 1500 | string replace -r "^" v)'
complete -c colorfix -f -l color -xa 'red green blue'

# Shaped like cobra's and clap's generated completions: cobra calls
# __complete from a helper, clap from the arguments script.
function cobrafix; end
function __cobrafix_perform_completion; set -l r "cobrafix __complete $argv"; end
function __cobrafix_prepare; __cobrafix_perform_completion; end
complete -c cobrafix -f -a '(__cobrafix_prepare)'
function clapfix; end
complete -c clapfix -f -a '(COMPLETE=fish clapfix -- (commandline -opc))'

# Loaded on demand from fish_complete_path.
function lazyfix; end
mkdir -p %[1]s/comp
echo "complete -c lazyfix -f -a loaded" > %[1]s/comp/lazyfix.fish
set -g fish_complete_path %[1]s/comp $fish_complete_path

set -gx RX_OUT %[1]s
set -gx RX_SCRIPT %[1]s/script
set -gx RX_LOG %[1]s/log
# The fifo directory is created here, so tests can check it is cleaned up.
set -gx XDG_RUNTIME_DIR %[1]s/run
mkdir -p $XDG_RUNTIME_DIR
set -g _rx_bin %[2]s
source %[3]s
`

type shell struct {
	t       *testing.T
	dir     string
	zsh, sh string
	major   int
}

// fishVersion returns fish's path and version. Tests are skipped without
// fish 3.4+.
func fishVersion(t *testing.T) (string, int, int) {
	t.Helper()
	sh, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not installed")
	}
	out, err := exec.Command(sh, "--version").Output()
	if err != nil {
		t.Skipf("fish: %v", err)
	}
	var major, minor int
	v := strings.TrimSpace(string(out))
	if _, err := fmt.Sscanf(v[strings.LastIndexByte(v, ' ')+1:], "%d.%d", &major, &minor); err != nil || major < 3 || major == 3 && minor < 4 {
		t.Skipf("%s is older than 3.4", v)
	}
	return sh, major, minor
}

// newShell prepares a config.fish running the widget with bin as rx. Tests
// are skipped without fish 3.4+, or without zsh, whose zpty module drives
// the terminal.
func newShell(t *testing.T, bin string) *shell {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed (needed to drive the terminal)")
	}
	sh, major, _ := fishVersion(t)
	dir := t.TempDir()
	script, _ := filepath.Abs("rx.fish")
	if err := os.WriteFile(filepath.Join(dir, "config.fish"), fmt.Appendf(nil, config, dir, bin, script), 0o600); err != nil {
		t.Fatal(err)
	}
	return &shell{t: t, dir: dir, zsh: zsh, sh: sh, major: major}
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

// rc appends lines to config.fish, after the widget script has been
// sourced.
func (s *shell) rc(lines string) {
	f, err := os.OpenFile(filepath.Join(s.dir, "config.fish"), os.O_APPEND|os.O_WRONLY, 0)
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

// noise fails if job notices or write errors reached the terminal.
func (s *shell) noise() {
	s.t.Helper()
	tr := s.transcript()
	for _, bad := range []string{"has ended", "Broken pipe", "write error", "Unknown command"} {
		if strings.Contains(tr, bad) {
			s.t.Errorf("%q leaked to the terminal:\n%q", bad, tr)
		}
	}
}

func TestWidgetProtocol(t *testing.T) {
	s := fake(t,
		req("INFO", "fx", "__complete", "COMPLETE=fish"),
		req("INFO", "cobrafix", "__complete", "COMPLETE=fish"),
		req("INFO", "clapfix", "__complete", "COMPLETE=fish"),
		req("INFO", "lazyfix", "__complete"),
		req("INFO", "nosuchcmd", "__complete"),
		req("COMPLETE", "fixture "),
		req("COMPLETE", "fx al"),
		req("DONE", "13", "fixture alpha"),
	)
	s.run(append(typeAndEdit("fixture b"), s.accept()...)...)

	if got := s.read("args"); got != "edit --shell fish --cursor 9 -- fixture b\n" {
		t.Errorf("rx args = %q", got)
	}
	for i, want := range []string{
		"I\tfixture\tcomplete\t\nEND\n",
		"I\t\tcomplete\t__complete\nEND\n",
		"I\t\tcomplete\tCOMPLETE=fish\nEND\n",
		"I\t\t" + s.dir + "/comp/lazyfix.fish\t\nEND\n",
		"I\t\t\t\nEND\n",
	} {
		if got := s.read(fmt.Sprintf("resp.%d", i+1)); got != want {
			t.Errorf("INFO %d = %q, want %q", i+1, got, want)
		}
	}
	if got := s.read("resp.6"); got != "C\talpha\tfirst host\t\nC\tbeta\tsecond\t\nEND\n" {
		t.Errorf("COMPLETE = %q", got)
	}
	// Completion through the alias filters by the typed prefix.
	if got := s.read("resp.7"); got != "C\talpha\tfirst host\t\nEND\n" {
		t.Errorf("COMPLETE via alias = %q", got)
	}
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}
	s.noise()
	s.checkCleanedUp()
}

func TestWidgetCompletion(t *testing.T) {
	lines := []string{
		"colorfix --color ", // flag values
		"lazyfix ",          // loaded on demand
		"fixtur",            // command position: commands and functions
	}
	var reqs []string
	for _, l := range lines {
		reqs = append(reqs, req("COMPLETE", l))
	}
	s := fake(t, append(reqs, req("CANCEL"))...)
	s.run(append(typeAndEdit("fixture"), "file:"+filepath.Join(s.dir, fmt.Sprintf("resp.%d", len(lines))), "sleep:0.2")...)
	for i, want := range []string{
		"C\tblue\t\t\nC\tgreen\t\t\nC\tred\t\t\nEND\n",
		"C\tloaded\t\t\nEND\n",
		"C\tfixture\t",
	} {
		if got := s.read(fmt.Sprintf("resp.%d", i+1)); !strings.Contains(got, want) {
			t.Errorf("COMPLETE %q = %q, want %q", lines[i], got, want)
		}
	}
}

// The widget exports aliases and the session's history file.
func TestWidgetEnv(t *testing.T) {
	s := newShell(t, "")
	stub := filepath.Join(s.dir, "stub-rx")
	s.write("stub-rx", "#!/bin/sh\ncp \"$RX_HISTFILE\" \"$RX_OUT/histcopy\"\nprintf '%s' \"$RX_ALIASES\" > \"$RX_OUT/aliases\"\n")
	s.rc("set -g _rx_bin " + stub + "\nalias gs='git status'\nalias ll 'ls -l'")
	s.run("key:fixture one\r", "file:"+filepath.Join(s.dir, "ran"), "key:fixture two", "key:\x18\x18", "file:"+filepath.Join(s.dir, "aliases"), "sleep:0.2")
	if got := s.read("histcopy"); !strings.Contains(got, "- cmd: fixture one\n") {
		t.Errorf("history seen by rx = %q", got)
	}
	al := strings.Split(s.read("aliases"), "\n")
	for _, want := range []string{"fx=fixture", "gs=git status", "ll=ls -l"} {
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

	s = fake(t, req("DONE", "13", "fixture alpha"))
	s.run(append(typeAndEdit("fixture b"), "sleep:0.5")...)
	if _, err := os.Stat(filepath.Join(s.dir, "ran")); err == nil {
		t.Errorf("plain DONE ran the command: %q", s.read("ran"))
	}
}

// Values must survive the protocol in both directions: completions with
// backslashes going to rx, and a line with backslashes, a tab and a newline
// coming back.
func TestWidgetEscaping(t *testing.T) {
	line := "escfix 'a\\b' \"x\ty\"\nescfix z"
	s := fake(t, req("COMPLETE", "escfix "), req("DONE", "0", line))
	s.run(append(typeAndEdit("escfix"), "sleep:0.3", "key:\r", "file:"+filepath.Join(s.dir, "ran"), "sleep:0.3")...)
	want := fmt.Sprintf("C\t%s\t%s\t\nC\t%s\t%s\t\nEND\n", proto.Escape(`a\b`), proto.Escape(`back\slash`), proto.Escape(`x\n`), proto.Escape(`lit\nd`))
	if got := s.read("resp.1"); got != want {
		t.Errorf("COMPLETE = %q, want %q", got, want)
	}
	if got := s.read("ran"); got != "escfix a\\b x\ty\nescfix z\n" {
		t.Errorf("ran %q", got)
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

// The cursor rx gets and sends back counts characters.
func TestWidgetCursor(t *testing.T) {
	// DONE's cursor 9 is right after "é", where X is typed next.
	s := fake(t, req("DONE", "9", "fixture é b"))
	steps := append([]string{"key:fixture é b", "key:\x02"}, typeAndEdit("")...)
	s.run(append(append(steps, "sleep:0.3", "key:X"), s.accept()...)...)
	if got := s.read("args"); got != "edit --shell fish --cursor 10 -- fixture é b\n" {
		t.Errorf("rx args = %q", got)
	}
	if got := s.read("ran"); got != "fixture éX b\n" {
		t.Errorf("ran %q", got)
	}
}

// The widget is bound in the default and insert modes, so vi mode works,
// also when enabled after loading rx, and rx-bind rebinds.
func TestWidgetKeymaps(t *testing.T) {
	tests := map[string]string{
		"vi insert":    "fish_vi_key_bindings",
		"vi normal":    "fish_vi_key_bindings",
		"rx-bind key":  "rx-bind '^Xr'",
		"fish escapes": `rx-bind '\cxr'`,
		// rx init bakes the config's key into _rx_key; RX_KEY overrides it.
		"config key":  "set -g _rx_key '^Xr'; rx-bind",
		"RX_KEY wins": "set -g _rx_key '^Xe'; set -g RX_KEY '^Xr'; rx-bind",
	}
	_, major, _ := fishVersion(t)
	if major >= 4 {
		tests["key names"] = "rx-bind ctrl-x,r"
	}
	for name, extra := range tests {
		t.Run(name, func(t *testing.T) {
			s := fake(t, req("DONE", "13", "fixture alpha"))
			s.rc(extra)
			steps := []string{"key:fixture b"}
			switch name {
			case "vi normal":
				steps = append(steps, "key:\x1b", "sleep:0.4", "key:\x18\x18")
			case "vi insert":
				steps = append(steps, "key:\x18\x18")
			default:
				steps = append(steps, "key:\x18r")
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
		{"missing binary", nil, "fixture b", "set -g _rx_bin /nonexistent/rx", "rx: binary not found: /nonexistent/rx"},
		{"no temp dir", nil, "fixture b", "set -gx XDG_RUNTIME_DIR /nonexistent", "rx: cannot create a temporary directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fake(t, tt.script...)
			if tt.rc != "" {
				s.rc(tt.rc)
			}
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
	s.rc("fish_vi_key_bindings; rx-bind '^Xr'")
	s.run("key:rx-doctor\r", "sleep:0.5")
	tr := s.transcript()
	wantKeys := `\cx\cx \cxr`
	if s.major >= 4 {
		wantKeys = "ctrl-x,ctrl-x ctrl-x,r"
	}
	for _, want := range []string{"key bindings fish_vi_key_bindings", "insert       " + wantKeys, "log          " + s.dir + "/log"} {
		if !strings.Contains(tr, want) {
			t.Errorf("rx-doctor output missing %q:\n%s", want, tr)
		}
	}
}

// fishFuncs runs code with args as $argv after sourcing the widget in a
// non-interactive fish, which defines the functions without binding keys.
func fishFuncs(t *testing.T, code string, args ...string) string {
	t.Helper()
	sh, _, _ := fishVersion(t)
	cmd := exec.Command(sh, append([]string{"--no-config", "-c", "source rx.fish; " + code}, args...)...)
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "HOME="+home, "XDG_CONFIG_HOME="+home, "XDG_DATA_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish: %v\n%s", err, out)
	}
	return string(out)
}

func TestKeySeq(t *testing.T) {
	for key, want := range map[string]string{
		"^X^X":          "\x18\x18",
		"^[e":           "\x1be",
		"^Xr":           "\x18r",
		"^?":            "\x7f",
		`\cx\cx`:        "\x18\x18",
		"ctrl-x,ctrl-x": "ctrl-x,ctrl-x",
		"alt-e":         "alt-e",
	} {
		if got := fishFuncs(t, `printf %s (_rx_keyseq $argv[1])`, key); got != want {
			t.Errorf("_rx_keyseq %q = %q, want %q", key, got, want)
		}
	}
}

func TestEscape(t *testing.T) {
	for _, v := range []string{"plain", `a\b`, "tab\there", "new\nline", `\n literal`, `trailing\`, "", "end\n"} {
		got := fishFuncs(t, `set e (_rx_esc $argv[1]); printf '%s|%s' $e (_rx_unesc $e | string collect -N -a)`, v)
		if want := proto.Escape(v) + "|" + v; got != want {
			t.Errorf("escape %q = %q, want %q", v, got, want)
		}
	}
	if got := fishFuncs(t, `_rx_unesc 'a\xb\\'`); got != proto.Unescape(`a\xb\`) {
		t.Errorf("unescape of unknown escapes = %q, want %q", got, proto.Unescape(`a\xb\`))
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
// "fixture b", fuzzy-pick alpha from the shell's completion, accept, and
// run with Ctrl-E.
func TestWidgetEndToEnd(t *testing.T) {
	newShell(t, "") // skip early without fish or zsh
	bin := buildRx(t)
	setup := func() *shell {
		s := newShell(t, bin)
		s.rc("set -gx XDG_CACHE_HOME " + s.dir + "/cache")
		return s
	}

	s := setup()
	steps := append(typeAndEdit("fixture b"),
		"expect:first host", // the shell's completion, with its description, reached the editor
		"key:alp",
		"expect:alpha",
		"key:\r",
	)
	s.run(append(steps, s.accept()...)...)
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}

	// Ctrl-E accepts and runs.
	s = setup()
	s.run(append(typeAndEdit("fixture b"), "expect:first host", "key:alp", "expect:alpha", "key:\x05", "file:"+filepath.Join(s.dir, "ran"))...)
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("after Ctrl-E ran %q", got)
	}

	// Esc Esc cancels and keeps the buffer.
	s = setup()
	steps = append(typeAndEdit("fixture b"), "expect:first host", "key:x", "key:\x1b", "sleep:0.2", "key:\x1b", "sleep:0.3")
	s.run(append(steps, s.accept()...)...)
	if got := s.read("ran"); got != "fixture b\n" {
		t.Errorf("after cancel ran %q", got)
	}
}
