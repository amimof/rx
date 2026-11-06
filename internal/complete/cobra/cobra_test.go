package cobra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amimof/rx/internal/complete"
)

func TestParse(t *testing.T) {
	res, err := Parse([]byte("default\tActive\nkube-system\nmonitoring\tsome desc\n_activeHelp_ pick one\n:4\n"))
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
	if _, err := Parse([]byte("usage: foo\n")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("non-cobra output: err = %v", err)
	}
	if _, err := Parse([]byte(":1\n")); err == nil {
		t.Error("error directive should fail")
	}
}

// fakeBin writes a cobra-like executable that prints namespaces when asked
// to complete -n, and pods otherwise, echoing the -n value it got.
func fakeBin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fakectl")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBackend(t *testing.T) {
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
	b := &Backend{}
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

func TestBackendTimeout(t *testing.T) {
	bin := fakeBin(t, "exec sleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (&Backend{}).Complete(ctx, complete.Request{Argv: []string{bin, ""}})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}
