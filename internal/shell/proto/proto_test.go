package proto

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/amimof/rx/internal/complete"
)

// fakeShell emulates the widget side of the protocol.
func fakeShell(t *testing.T, in io.Reader, out io.Writer, delay time.Duration) <-chan string {
	final := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			switch f[0] {
			case "INFO":
				fmt.Fprintf(out, "I\t%s\t%s\t%s\nEND\n", "kubectl", "__start_kubectl", "1")
			case "COMPLETE":
				time.Sleep(delay)
				line := Unescape(f[1])
				fmt.Fprintf(out, "C\thost-a\thost-a  -- first host\thosts\n")
				fmt.Fprintf(out, "C\t%s\t\tlines\n", Escape(line))
				fmt.Fprintf(out, "C\thost-a\t\thosts\n")
				fmt.Fprintf(out, "M\tremote host name\nEND\n")
			case "DONE":
				final <- strings.Join(append([]string{f[1], Unescape(f[2])}, f[3:]...), " ")
				return
			case "CANCEL":
				final <- "cancel"
				return
			}
		}
	}()
	return final
}

func TestClient(t *testing.T) {
	toShellR, toShellW := io.Pipe()
	fromShellR, fromShellW := io.Pipe()
	final := fakeShell(t, toShellR, fromShellW, 0)
	c := NewClient("zsh", fromShellR, toShellW)
	ctx := context.Background()

	info, err := c.Info(ctx, "k")
	if err != nil || !info.Cobra || info.Alias != "kubectl" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	res, err := c.Complete(ctx, complete.Request{Line: "ssh \tweird\nline "})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("want duplicates removed, got %+v", res.Candidates)
	}
	if res.Candidates[0].Desc != "first host" || res.Candidates[0].Group != "hosts" || res.Candidates[0].Source != "zsh" {
		t.Errorf("candidate 0 = %+v", res.Candidates[0])
	}
	if res.Candidates[1].Value != "ssh \tweird\nline " {
		t.Errorf("escaping round trip = %q", res.Candidates[1].Value)
	}
	if res.Message != "remote host name" {
		t.Errorf("message = %q", res.Message)
	}
	if err := c.Done("ssh host-a", 10); err != nil {
		t.Fatal(err)
	}
	if got := <-final; got != "10 ssh host-a" {
		t.Errorf("final = %q", got)
	}
}

func TestClientRun(t *testing.T) {
	toShellR, toShellW := io.Pipe()
	fromShellR, fromShellW := io.Pipe()
	final := fakeShell(t, toShellR, fromShellW, 0)
	c := NewClient("zsh", fromShellR, toShellW)
	if err := c.Run("ssh\thost-a", 10); err != nil {
		t.Fatal(err)
	}
	if got := <-final; got != "10 ssh\thost-a run" {
		t.Errorf("final = %q", got)
	}
}

func TestClientTimeoutDisables(t *testing.T) {
	toShellR, toShellW := io.Pipe()
	fromShellR, fromShellW := io.Pipe()
	fakeShell(t, toShellR, fromShellW, 300*time.Millisecond)
	c := NewClient("zsh", fromShellR, toShellW)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Complete(ctx, complete.Request{Line: "ssh "}); err == nil {
		t.Fatal("want timeout")
	}
	_, err := c.Complete(context.Background(), complete.Request{Line: "ssh "})
	if err == nil || !strings.Contains(err.Error(), "zsh completion timed out") {
		t.Fatalf("client should stay disabled after a timeout, got %v", err)
	}
}

func TestCleanDesc(t *testing.T) {
	for _, tt := range [][3]string{
		{"main", "main  -- default branch", "default branch"},
		{"main", "main", ""},
		{"-n", "-n  -- namespace", "namespace"},
		{"x", "", ""},
	} {
		if got := cleanDesc(tt[0], tt[1]); got != tt[2] {
			t.Errorf("cleanDesc(%q, %q) = %q, want %q", tt[0], tt[1], got, tt[2])
		}
	}
}

func TestEscape(t *testing.T) {
	for _, s := range []string{"", "plain", "a\tb", "a\nb", `back\slash`, `\n literal`, "mixed\\\n\t"} {
		e := Escape(s)
		for _, c := range e {
			if c == '\n' || c == '\t' {
				t.Errorf("Escape(%q) = %q contains separator", s, e)
			}
		}
		if got := Unescape(e); got != s {
			t.Errorf("round trip %q -> %q -> %q", s, e, got)
		}
	}
}
