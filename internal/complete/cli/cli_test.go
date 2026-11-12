package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/amimof/rx/internal/complete"
)

func TestCobraParse(t *testing.T) {
	res, err := Cobra{}.Parse([]byte("default\tActive\nkube-system\nmonitoring\tsome desc\n_activeHelp_ pick one\n:4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Directive != complete.DirectiveNoFileComp {
		t.Errorf("directive = %d", res.Directive)
	}
	if len(res.Candidates) != 3 || res.Candidates[0].Desc != "Active" || res.Candidates[2].Value != "monitoring" {
		t.Errorf("candidates = %+v", res.Candidates)
	}
	if res.Message != "pick one" {
		t.Errorf("message = %q", res.Message)
	}
	if _, err := (Cobra{}).Parse([]byte("usage: foo\n")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("non-cobra output: err = %v", err)
	}
	if _, err := (Cobra{}).Parse([]byte(":1\n")); err == nil {
		t.Error("error directive should fail")
	}
}

func TestClapParse(t *testing.T) {
	res, err := Clap{}.Parse([]byte("log\tShow revision history\nnew\n\n--help\tPrint help\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 3 || res.Candidates[0].Desc != "Show revision history" || res.Candidates[1].Value != "new" || res.Candidates[2].Value != "--help" {
		t.Errorf("candidates = %+v", res.Candidates)
	}
	if res, err := (Clap{}).Parse(nil); err != nil || len(res.Candidates) != 0 {
		t.Errorf("empty output: %+v, %v", res, err)
	}
}

func TestRegistry(t *testing.T) {
	for _, p := range Protocols {
		if Lookup(p.Name()) != p {
			t.Errorf("%s: lookup by name failed", p.Name())
		}
		for _, m := range p.Markers() {
			if ByMarker(m) != p {
				t.Errorf("%s: lookup by marker %q failed", p.Name(), m)
			}
		}
	}
	if m := Markers(); !slices.Contains(m, "COMPLETE=fish") || m[0] != "__complete" {
		t.Errorf("Markers() = %q", m)
	}
	if Lookup("nope") != nil || ByMarker("") != nil || ByMarker("nope") != nil {
		t.Error("unknown names and markers should not resolve")
	}
}

// fakeBin writes an executable script with the given body.
func fakeBin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fakectl")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCobraBackend(t *testing.T) {
	// Prints namespaces when asked to complete -n, and pods otherwise,
	// echoing the -n value it got.
	bin := fakeBin(t, `
[ "$1" = __complete ] || exit 1
shift
ns=default
prev=
for a in "$@"; do
  [ "$prev" = -n ] && ns=$a
  pp=$prev
  prev=$a
done
if [ "$pp" = -n ]; then printf 'default\nkube-system\n:4\n'; exit 0; fi
printf 'pod-a\t%s\npod-b\t%s\n:4\n' "$ns" "$ns"
echo "Completion ended with directive: ShellCompDirectiveNoFileComp" >&2
`)
	b := &Backend{Protocol: Cobra{}}
	res, err := b.Complete(context.Background(), complete.Request{Argv: []string{bin, "get", "pods", "-n", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 || res.Candidates[1].Value != "kube-system" {
		t.Fatalf("ns = %+v", res.Candidates)
	}
	res, err = b.Complete(context.Background(), complete.Request{Argv: []string{bin, "get", "pods", "-n", "prod", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 || res.Candidates[0].Desc != "prod" {
		t.Fatalf("pods = %+v", res.Candidates)
	}
}

func TestClapBackend(t *testing.T) {
	// Echoes its arguments as one candidate when COMPLETE=fish is set, like
	// a CompleteEnv binary, and fails otherwise.
	bin := fakeBin(t, `
[ "$COMPLETE" = fish ] || exit 3
[ "$1" = -- ] || exit 4
shift
printf '%s\targs\n' "$*"
printf 'two\n'
`)
	b := &Backend{Protocol: Clap{}, Env: []string{"PATH=" + os.Getenv("PATH"), "COMPLETE=bash"}}
	res, err := b.Complete(context.Background(), complete.Request{Argv: []string{bin, "log", "-r", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if want := bin + " log -r "; len(res.Candidates) != 2 || res.Candidates[0].Value != want || res.Candidates[0].Desc != "args" {
		t.Fatalf("candidates = %+v, want first %q", res.Candidates, want)
	}

	// A failing command without output is an error.
	fail := fakeBin(t, "exit 2\n")
	if _, err := b.Complete(context.Background(), complete.Request{Argv: []string{fail, ""}}); err == nil {
		t.Fatal("want an error from a failing command")
	}
}

func TestBackendTimeout(t *testing.T) {
	bin := fakeBin(t, "exec sleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (&Backend{Protocol: Cobra{}}).Complete(ctx, complete.Request{Argv: []string{bin, ""}})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}
