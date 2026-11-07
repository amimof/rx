package transform

import (
	"errors"
	"testing"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

func setup(t *testing.T, line string) (*model.Command, *spec.Spec) {
	t.Helper()
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	c := &spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
	l := parse.Parse(line)
	cmd := c.Classify(l, 0)
	return cmd, reg.Lookup(cmd.Name)
}

// The examples from the design discussion must round-trip unchanged.
func TestRoundTrip(t *testing.T) {
	for _, in := range []string{
		"k get pods -n kube-system",
		"k get pods -n monitoring",
		"k get pods -A -o wide",
		"k get pods my-pod-123 -n production",
		"k get pods -l app=nginx -n production",
		`k  get pods   -l 'app=nginx'  --namespace="prod"`,
		"k get pods --output=yaml",
	} {
		cmd, _ := setup(t, in)
		if got := cmd.Render(); got != in {
			t.Errorf("round trip %q = %q", in, got)
		}
	}
}

func TestClassify(t *testing.T) {
	cmd, _ := setup(t, "k get pods my-pod -n prod -A --output=wide -ojson")
	want := []struct {
		kind  model.Kind
		key   string
		value string
	}{
		{model.Positional, "verb", "get"},
		{model.Positional, "resource", "pods"},
		{model.Positional, "name", "my-pod"},
		{model.Flag, "namespace", "prod"},
		{model.Bool, "all-namespaces", ""},
		{model.Flag, "output", "wide"},
		{model.Flag, "output", "json"},
	}
	if cmd.Name != "kubectl" {
		t.Fatalf("name = %q", cmd.Name)
	}
	if len(cmd.Slots) != len(want) {
		t.Fatalf("got %d slots: %+v", len(cmd.Slots), cmd.Slots)
	}
	for i, w := range want {
		s := cmd.Slots[i]
		if s.Kind != w.kind || s.Key != w.key || s.Value != w.value {
			t.Errorf("slot %d = {%v %q %q}, want %+v", i, s.Kind, s.Key, s.Value, w)
		}
	}
}

func TestTransforms(t *testing.T) {
	tests := []struct {
		in   string
		do   func(*model.Command, *spec.Spec) error
		want string
	}{
		{
			"k get pods -n monitoring",
			func(c *model.Command, s *spec.Spec) error { return Toggle(c, s, "all-namespaces") },
			"k get pods -A",
		},
		{
			"k get pods -A -o wide",
			func(c *model.Command, s *spec.Spec) error { return Set(c, s, "namespace", "kube-system") },
			"k get pods -o wide -n kube-system",
		},
		{
			"k get pods -A",
			func(c *model.Command, s *spec.Spec) error { return Toggle(c, s, "all-namespaces") },
			"k get pods",
		},
		{
			"k get pods",
			func(c *model.Command, s *spec.Spec) error { return Cycle(c, s, "output", nil) },
			"k get pods -o wide",
		},
		{
			"k get pods -o wide",
			func(c *model.Command, s *spec.Spec) error { return Cycle(c, s, "output", nil) },
			"k get pods -o yaml",
		},
		{
			"k get pods -o name",
			func(c *model.Command, s *spec.Spec) error { return Cycle(c, s, "output", nil) },
			"k get pods",
		},
		{
			"k get pods my-pod-123 -n production",
			func(c *model.Command, s *spec.Spec) error { return Cycle(c, s, "verb", nil) },
			"k describe pods my-pod-123 -n production",
		},
		{
			"k delete pods x",
			func(c *model.Command, s *spec.Spec) error { return Cycle(c, s, "verb", nil) },
			"k get pods x",
		},
		{
			"k get pods -l app=nginx -n production",
			func(c *model.Command, s *spec.Spec) error { return Set(c, s, "name", "nginx-abc") },
			"k get pods nginx-abc -l app=nginx -n production",
		},
		{
			"k get pods -n production",
			func(c *model.Command, s *spec.Spec) error { return Set(c, s, "namespace", "my ns") },
			"k get pods -n 'my ns'",
		},
		{
			"k get pods -l 'app=nginx' -n production",
			func(c *model.Command, s *spec.Spec) error { return Set(c, s, "namespace", "staging") },
			"k get pods -l 'app=nginx' -n staging",
		},
	}
	for _, tt := range tests {
		cmd, sp := setup(t, tt.in)
		if err := tt.do(cmd, sp); err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if got := cmd.Render(); got != tt.want {
			t.Errorf("%q => %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCycleWithoutValues(t *testing.T) {
	cmd, sp := setup(t, "k get pods")
	if err := Cycle(cmd, sp, "resource", nil); !errors.Is(err, ErrNoValues) {
		t.Fatalf("err = %v, want ErrNoValues", err)
	}
	if got := cmd.Render(); got != "k get pods" {
		t.Fatalf("command changed to %q", got)
	}
	// Values supplied by the caller wrap around for positionals.
	values := []string{"pods", "secrets"}
	for _, want := range []string{"k get secrets", "k get pods"} {
		if err := Cycle(cmd, sp, "resource", values); err != nil {
			t.Fatal(err)
		}
		if got := cmd.Render(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestApplySpecTransforms(t *testing.T) {
	cmd, sp := setup(t, "k get pods -n monitoring")
	for _, tr := range sp.Transforms {
		if tr.Key == "A" {
			if err := Apply(cmd, sp, tr); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := cmd.Render(); got != "k get pods -A" {
		t.Fatalf("got %q", got)
	}
}

func TestBundledSpecsValid(t *testing.T) {
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	for _, s := range reg.Specs() {
		for _, err := range s.Validate() {
			t.Errorf("%s: %v", s.Source, err)
		}
	}
}
