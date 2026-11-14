package complete_test

import (
	"reflect"
	"testing"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

func classify(t *testing.T, line string) *spec.Classifier {
	t.Helper()
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	return &spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
}

func TestSynthesize(t *testing.T) {
	tests := []struct {
		line  string
		focus string
		argv  []string
		zline string
	}{
		{
			"k get pods -l app=nginx -n production", "namespace",
			[]string{"kubectl", "get", "pods", "-l", "app=nginx", "-n", ""},
			"k get pods -l app=nginx -n ",
		},
		{
			// The name slot keeps -n so the completer lists that namespace.
			"k get pods my-pod -n production -o wide", "name",
			[]string{"kubectl", "get", "pods", "-n", "production", "-o", "wide", ""},
			"k get pods -n production -o wide ",
		},
		{
			// Later positionals are dropped when completing an earlier one.
			"k get pods my-pod -n prod", "resource",
			[]string{"kubectl", "get", "-n", "prod", ""},
			"k get -n prod ",
		},
		{
			"k get pods --namespace='a b'", "subcommand",
			[]string{"kubectl", "--namespace=a b", ""},
			"k --namespace='a b' ",
		},
	}
	for _, tt := range tests {
		c := classify(t, tt.line)
		cmd := c.Classify(parse.Parse(tt.line), 0)
		req := complete.Synthesize(cmd, cmd.Find(tt.focus))
		if !reflect.DeepEqual(req.Argv, tt.argv) {
			t.Errorf("%q/%s argv = %q, want %q", tt.line, tt.focus, req.Argv, tt.argv)
		}
		if req.Line != tt.zline {
			t.Errorf("%q/%s line = %q, want %q", tt.line, tt.focus, req.Line, tt.zline)
		}
	}
}

func TestSynthesizeFlags(t *testing.T) {
	c := classify(t, "")
	cmd := c.Classify(parse.Parse("k get pods -n x"), 0)
	req := complete.SynthesizeFlags(cmd)
	if want := []string{"kubectl", "get", "pods", "-n", "x", "-"}; !reflect.DeepEqual(req.Argv, want) {
		t.Errorf("argv = %q", req.Argv)
	}
	if req.Line != "k get pods -n x -" {
		t.Errorf("line = %q", req.Line)
	}
}

func TestCache(t *testing.T) {
	c := &complete.Cache{Dir: t.TempDir()}
	req := complete.Request{Argv: []string{"kubectl", "get", ""}}
	if _, _, ok := c.Get("cobra", req); ok {
		t.Fatal("unexpected hit")
	}
	want := complete.Result{Candidates: []complete.Candidate{{Value: "pods", Desc: "d"}}, Directive: 4}
	if err := c.Put("cobra", req, want); err != nil {
		t.Fatal(err)
	}
	got, _, ok := c.Get("cobra", req)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if _, _, ok := c.Get("zsh", req); ok {
		t.Fatal("backend must be part of the key")
	}
	t.Setenv("KUBECONFIG", "/other")
	if _, _, ok := c.Get("cobra", req); ok {
		t.Fatal("KUBECONFIG must be part of the key")
	}
}
