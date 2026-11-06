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

func TestInit(t *testing.T) {
	if out, _ := rx(t, nil, "init", "zsh"); !strings.Contains(out, "zle -N rx-edit") {
		t.Errorf("init zsh = %.80q", out)
	}
	if out, errOut := rx(t, nil, "init", "nosuch"); out != "" || !strings.Contains(errOut, `unsupported shell "nosuch"`) {
		t.Errorf("init nosuch: stdout %q, stderr %q", out, errOut)
	}
}
