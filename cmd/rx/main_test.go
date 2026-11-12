package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rx-test-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "rx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// rx runs the binary with args in an empty home.
func rx(t *testing.T, env []string, args ...string) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(bin, args...)
	cmd.Env = append([]string{"HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_CACHE_HOME=" + dir, "PATH=" + os.Getenv("PATH")}, env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	_ = cmd.Run()
	return o.String(), e.String()
}

// Only --shell puts rx in widget mode; an environment variable must not,
// since it would leak into every command rx runs.
func TestEditWidgetMode(t *testing.T) {
	out, errOut := rx(t, []string{"RX_COPROC=1"}, "edit", "--", "")
	if out != "" || !strings.Contains(errOut, "nothing to edit") {
		t.Errorf("standalone: stdout %q, stderr %q", out, errOut)
	}
	if out, _ := rx(t, nil, "edit", "--shell", "zsh", "--", ""); out != "ERROR\tnothing to edit\n" {
		t.Errorf("widget: stdout %q", out)
	}
	// An unknown shell is still reported to the widget that sent it.
	if out, _ := rx(t, nil, "edit", "--shell", "nosuch", "--", "ls"); !strings.HasPrefix(out, "ERROR\tunsupported shell \"nosuch\"") {
		t.Errorf("unknown shell: stdout %q", out)
	}
}

// configHome returns XDG_CONFIG_HOME for a config.yaml holding data.
func configHome(t *testing.T, data string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rx", "config.yaml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return "XDG_CONFIG_HOME=" + dir
}

func TestInitConfig(t *testing.T) {
	cfg := configHome(t, "key: '^[e'\nlog: /tmp/rx-test.log\n")
	out, errOut := rx(t, []string{cfg}, "init", "zsh")
	if !strings.Contains(out, "typeset -g _rx_key='^[e'\n") || !strings.Contains(out, "typeset -g _rx_log='/tmp/rx-test.log'\n") {
		t.Errorf("init with config = %.200q, stderr %q", out, errOut)
	}
	if out, _ := rx(t, []string{cfg, "RX_KEY=^Xr"}, "init", "zsh"); !strings.Contains(out, "typeset -g _rx_key='^Xr'\n") {
		t.Errorf("RX_KEY should override the config: %.200q", out)
	}
	if out, _ := rx(t, nil, "init", "zsh"); !strings.Contains(out, "typeset -g _rx_key='^X^X'\n") {
		t.Errorf("default key: %.200q", out)
	}
	// --config points at another file.
	other := filepath.Join(t.TempDir(), "other.yaml")
	if err := os.WriteFile(other, []byte("key: '^Xo'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := rx(t, nil, "--config", other, "init", "zsh"); !strings.Contains(out, "typeset -g _rx_key='^Xo'\n") {
		t.Errorf("--config: %.200q", out)
	}
}

// A bad config is reported to the widget, which shows it below the prompt.
func TestEditBadConfig(t *testing.T) {
	cfg := configHome(t, "keys: { jump: [ctrl+j] }\n")
	if out, _ := rx(t, []string{cfg}, "edit", "--shell", "zsh", "--", "ls"); !strings.HasPrefix(out, "ERROR\tconfig ") || !strings.Contains(out, `unknown action "jump"`) {
		t.Errorf("stdout %q", out)
	}
}

func TestInit(t *testing.T) {
	if out, _ := rx(t, nil, "init", "zsh"); !strings.Contains(out, "zle -N rx-edit") {
		t.Errorf("init zsh = %.80q", out)
	}
	cfg := configHome(t, "key: '^[e'\n")
	if out, _ := rx(t, []string{cfg}, "init", "bash"); !strings.Contains(out, "_rx_key='^[e'\n") || !strings.Contains(out, "rx-bind") {
		t.Errorf("init bash = %.120q", out)
	}
	if out, errOut := rx(t, nil, "init", "nosuch"); out != "" || !strings.Contains(errOut, `unsupported shell "nosuch"`) {
		t.Errorf("init nosuch: stdout %q, stderr %q", out, errOut)
	}
}
