package zsh

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
				line := complete.Unescape(f[1])
				fmt.Fprintf(out, "C\thost-a\thost-a  -- first host\thosts\n")
				fmt.Fprintf(out, "C\t%s\t\tlines\n", complete.Escape(line))
				fmt.Fprintf(out, "C\thost-a\t\thosts\n")
				fmt.Fprintf(out, "M\tremote host name\nEND\n")
			case "DONE":
				final <- f[1] + " " + complete.Unescape(f[2])
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
	c := NewClient(fromShellR, toShellW)
	ctx := context.Background()

	info, err := c.Info(ctx, "k")
	if err != nil || !info.Cobra || info.Alias != "kubectl" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	res, err := (&Backend{Client: c}).Complete(ctx, complete.Request{Line: "ssh \tweird\nline "})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("want duplicates removed, got %+v", res.Candidates)
	}
	if res.Candidates[0].Desc != "first host" || res.Candidates[0].Group != "hosts" {
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

func TestClientTimeoutDisables(t *testing.T) {
	toShellR, toShellW := io.Pipe()
	fromShellR, fromShellW := io.Pipe()
	fakeShell(t, toShellR, fromShellW, 300*time.Millisecond)
	c := NewClient(fromShellR, toShellW)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (&Backend{Client: c}).Complete(ctx, complete.Request{Line: "ssh "}); err == nil {
		t.Fatal("want timeout")
	}
	if _, err := (&Backend{Client: c}).Complete(context.Background(), complete.Request{Line: "ssh "}); err == nil {
		t.Fatal("client should stay disabled after a timeout")
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
