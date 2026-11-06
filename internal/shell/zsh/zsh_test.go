package zsh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amimof/rx/internal/complete"
)

func TestScript(t *testing.T) {
	s := Script("/opt/it's/rx")
	if !strings.HasPrefix(s, `typeset -g _rx_bin='/opt/it'\''s/rx'`) {
		t.Fatalf("header = %q", strings.SplitN(s, "\n", 2)[0])
	}
	for _, want := range []string{"zle -N rx-edit", "zle -C _rx_capture", "coproc"} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q", want)
		}
	}
}

// zshrc sets up fixture commands and completion functions. %[1]s is the
// test directory, %[2]s the rx binary and %[3]s the widget script.
const zshrc = `
stty rows 30 cols 120
PS1='> '
HISTFILE=%[1]s/hist
autoload -Uz compinit; compinit -u -d %[1]s/zcompdump

ran() { print -r -- "${(j: :)@}" >> %[1]s/ran }

_fixture() { local -a h=('alpha:first host' 'beta:second'); _describe host h }
fixture() { ran fixture "$@" }
compdef _fixture fixture
alias fx=fixture

# Completes to the whole buffer, to check escaping in both directions.
_echofix() { compadd -U -Q -- "$BUFFER" }
echofix() { ran echofix "$@" }
compdef _echofix echofix

_msgfix() { _message 'remote host name' }
compdef _msgfix msgfix

_bigfix() { compadd -- v{1..1500} }
compdef _bigfix bigfix

_cobrafix() { local out=$(cobrafix __complete "${words[@]:1}") }
compdef _cobrafix cobrafix

export RX_OUT=%[1]s RX_SCRIPT=%[1]s/script RX_LOG=%[1]s/log
typeset -g _rx_bin=%[2]s
source %[3]s
`

type shell struct {
	t   *testing.T
	dir string
}

// newShell prepares a ZDOTDIR running the widget with bin as rx. Tests are
// skipped without zsh.
func newShell(t *testing.T, bin string) *shell {
	t.Helper()
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not installed")
	}
	dir := t.TempDir()
	script, _ := filepath.Abs("rx.zsh")
	if err := os.WriteFile(filepath.Join(dir, ".zshrc"), fmt.Appendf(nil, zshrc, dir, bin, script), 0o600); err != nil {
		t.Fatal(err)
	}
	return &shell{t: t, dir: dir}
}

// fake returns a shell whose rx sends the given requests.
func fake(t *testing.T, requests ...string) *shell {
	t.Helper()
	bin, _ := filepath.Abs("testdata/fake-rx")
	s := newShell(t, bin)
	s.write("script", strings.Join(requests, "\n")+"\n")
	return s
}

func (s *shell) write(name, data string) {
	if err := os.WriteFile(filepath.Join(s.dir, name), []byte(data), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// rc appends lines to .zshrc, after the widget script has been sourced.
func (s *shell) rc(lines string) {
	f, err := os.OpenFile(filepath.Join(s.dir, ".zshrc"), os.O_APPEND|os.O_WRONLY, 0)
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

func (s *shell) transcript() string {
	b, _ := os.ReadFile(filepath.Join(s.dir, "transcript"))
	return string(b)
}

// run plays steps (see testdata/driver.zsh) after the first prompt.
func (s *shell) run(steps ...string) {
	s.t.Helper()
	args := append([]string{"-f", "testdata/driver.zsh", s.dir, "expect:> "}, steps...)
	out, err := exec.Command("zsh", args...).CombinedOutput()
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(s.dir, "log"))
		s.t.Fatalf("driver: %v %s\ntranscript:\n%q\nrx log:\n%s", err, out, s.transcript(), log)
	}
}

// typeAndEdit types line, then presses Alt-e.
func typeAndEdit(line string) []string {
	return []string{"key:" + line, "key:\x1be"}
}

// accept presses Enter and waits for the command to be recorded.
func (s *shell) accept() []string {
	return []string{"key:\n", "file:" + filepath.Join(s.dir, "ran")}
}

func req(fields ...string) string {
	for i := range fields {
		fields[i] = complete.Escape(fields[i])
	}
	return strings.Join(fields, "\t")
}

func TestWidgetProtocol(t *testing.T) {
	s := fake(t,
		req("INFO", "fx"),
		req("INFO", "cobrafix"),
		req("COMPLETE", "fixture "),
		req("COMPLETE", "fx al"),
		req("DONE", "13", "fixture alpha"),
	)
	s.run(append(typeAndEdit("fixture b"), s.accept()...)...)

	if got := s.read("args"); got != "edit --cursor 9 -- fixture b\n" {
		t.Errorf("rx args = %q", got)
	}
	if got := s.read("resp.1"); got != "I\tfixture\t_fixture\t0\nEND\n" {
		t.Errorf("INFO fx = %q", got)
	}
	if got := s.read("resp.2"); got != "I\t\t_cobrafix\t1\nEND\n" {
		t.Errorf("INFO cobrafix = %q", got)
	}
	comp := s.read("resp.3")
	for _, want := range []string{"C\talpha\talpha  -- first host\t", "C\tbeta\t", "\nEND\n"} {
		if !strings.Contains(comp, want) {
			t.Errorf("COMPLETE missing %q:\n%s", want, comp)
		}
	}
	// Completion through the alias filters by the typed prefix.
	if comp := s.read("resp.4"); !strings.Contains(comp, "C\talpha\t") || strings.Contains(comp, "beta") {
		t.Errorf("COMPLETE via alias = %q", comp)
	}
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}
	if tr := s.transcript(); strings.Contains(tr, "done") {
		t.Errorf("job notification leaked to the terminal:\n%q", tr)
	}
}

func TestWidgetEscaping(t *testing.T) {
	// Each value goes rx → zsh (unescaped into BUFFER) and back (escaped
	// candidate), so it must survive both directions intact.
	vals := []string{`echofix a\cb`, "echofix tab\there", `echofix back\\slash\n`, `echofix \x41\e`}
	var reqs []string
	for _, v := range vals {
		reqs = append(reqs, req("COMPLETE", v))
	}
	s := fake(t, append(reqs, req("CANCEL"))...)
	s.run(append(typeAndEdit("echofix"), "file:"+filepath.Join(s.dir, fmt.Sprintf("resp.%d", len(vals))), "sleep:0.2")...)
	for i, v := range vals {
		want := fmt.Sprintf("C\t%s\t", complete.Escape(v))
		if got := s.read(fmt.Sprintf("resp.%d", i+1)); !strings.HasPrefix(got, want) {
			t.Errorf("value %q: response %q, want prefix %q", v, got, want)
		}
	}
}

func TestWidgetMessageAndLargeLists(t *testing.T) {
	s := fake(t, req("COMPLETE", "msgfix "), req("COMPLETE", "bigfix "), req("CANCEL"))
	s.run(append(typeAndEdit("msgfix"), "file:"+filepath.Join(s.dir, "resp.2"), "sleep:0.3")...)
	if got := s.read("resp.1"); !strings.Contains(got, "M\tremote host name\n") {
		t.Errorf("message response = %q", got)
	}
	if n := strings.Count(s.read("resp.2"), "\nC\t") + 1; n != 1500 {
		t.Errorf("got %d of 1500 candidates", n)
	}
	tr := s.transcript()
	for _, bad := range []string{"remote host name", "do you wish", "v1499"} {
		if strings.Contains(tr, bad) {
			t.Errorf("%q leaked to the terminal:\n%q", bad, tr)
		}
	}
}

// After cancel or an rx crash, the buffer and cursor must be unchanged and
// nothing may be printed over the prompt.
func TestWidgetRestoresBuffer(t *testing.T) {
	for name, script := range map[string][]string{
		"cancel":             {req("CANCEL")},
		"crash":              {"DIE"},
		"crash mid-request":  {"DIEAFTER " + req("COMPLETE", "bigfix ")},
		"crash after result": {req("COMPLETE", "fixture "), "DIE"},
	} {
		t.Run(name, func(t *testing.T) {
			s := fake(t, script...)
			// Cursor goes back two characters before Alt-e; "XY" typed
			// afterwards must land there.
			steps := append([]string{"key:fixture beta", "key:\x02\x02"}, typeAndEdit("")...)
			steps = append(steps, "sleep:0.3", "key:XY")
			s.run(append(steps, s.accept()...)...)
			if got := s.read("ran"); got != "fixture beXYta\n" {
				t.Errorf("ran %q", got)
			}
			if tr := s.transcript(); strings.Contains(tr, "broken pipe") || strings.Contains(tr, "done") {
				t.Errorf("noise on terminal:\n%q", tr)
			}
		})
	}
}

// The widget is bound in every keymap, so vi mode enabled after loading rx
// (the usual .zshrc order problem) still works, and rx-bind rebinds.
func TestWidgetKeymaps(t *testing.T) {
	for name, extra := range map[string]string{
		"vi insert":   "bindkey -v",
		"vi command":  "bindkey -v",
		"rx-bind key": "bindkey -e; rx-bind '^Xr'",
	} {
		t.Run(name, func(t *testing.T) {
			s := fake(t, req("DONE", "13", "fixture alpha"))
			s.rc(extra + "\nKEYTIMEOUT=20")
			steps := []string{"key:fixture b"}
			switch name {
			case "vi command":
				steps = append(steps, "key:\x1b", "sleep:0.4", "key:\x1be")
			case "rx-bind key":
				steps = append(steps, "key:\x18r")
			default:
				steps = append(steps, "key:\x1be")
			}
			s.run(append(steps, "sleep:0.3", "key:\r", "file:"+filepath.Join(s.dir, "ran"))...)
			if got := s.read("ran"); got != "fixture alpha\n" {
				t.Errorf("ran %q", got)
			}
		})
	}
}

// Running the widget must not change keymaps: prompt themes such as
// starship hook zle-keymap-select and break when it fires unexpectedly.
func TestWidgetKeepsKeymap(t *testing.T) {
	s := fake(t, req("CANCEL"))
	s.rc(`zle-keymap-select() { print -r -- $KEYMAP >> ` + s.dir + `/keymaps }; zle -N zle-keymap-select`)
	s.run(append(typeAndEdit("fixture b"), "sleep:0.3", "key:\n", "file:"+filepath.Join(s.dir, "ran"))...)
	if b, err := os.ReadFile(filepath.Join(s.dir, "keymaps")); err == nil {
		t.Errorf("zle-keymap-select fired: %q", b)
	}
}

// Failures are shown below the prompt instead of silently doing nothing.
func TestWidgetErrorMessages(t *testing.T) {
	tests := []struct {
		name   string
		script []string
		line   string
		bin    string
		want   string
	}{
		{"rx error", []string{req("ERROR", "boom: no such thing")}, "fixture b", "", "rx: boom: no such thing"},
		{"rx crash", []string{"DIE"}, "fixture b", "", "rx: exited unexpectedly"},
		{"empty prompt", nil, "", "", "rx: nothing to edit"},
		{"missing binary", nil, "fixture b", "/nonexistent/rx", "rx: binary not found: /nonexistent/rx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fake(t, tt.script...)
			if tt.bin != "" {
				s.rc("_rx_bin=" + tt.bin)
			}
			s.run(append(typeAndEdit(tt.line), "expect:"+tt.want, "key:\n")...)
		})
	}
}

// TestWidgetEndToEnd drives the real rx binary: open the editor on
// "fixture b", fuzzy-pick alpha from the shell's completion, accept and run.
func TestWidgetEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not installed")
	}
	bin := filepath.Join(t.TempDir(), "rx")
	build := exec.Command("go", "build", "-o", bin, "../../../cmd/rx")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	s := newShell(t, bin)
	s.write(".zshenv", "export XDG_CACHE_HOME="+s.dir+"/cache XDG_CONFIG_HOME="+s.dir+"/config\n")
	steps := append(typeAndEdit("fixture b"),
		"expect:first host", // the shell's completion reached the editor
		"key:alp",
		"expect:alpha",
		"key:\r",
	)
	s.run(append(steps, s.accept()...)...)
	if got := s.read("ran"); got != "fixture alpha\n" {
		t.Errorf("ran %q", got)
	}

	// Esc Esc cancels and keeps the buffer.
	s = newShell(t, bin)
	s.write(".zshenv", "export XDG_CACHE_HOME="+s.dir+"/cache XDG_CONFIG_HOME="+s.dir+"/config\n")
	steps = append(typeAndEdit("fixture b"), "expect:first host", "key:x", "key:\x1b", "sleep:0.2", "key:\x1b", "sleep:0.3")
	s.run(append(steps, s.accept()...)...)
	if got := s.read("ran"); got != "fixture b\n" {
		t.Errorf("after cancel ran %q", got)
	}
}
