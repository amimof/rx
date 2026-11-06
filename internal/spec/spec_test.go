package spec

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateComplete(t *testing.T) {
	for _, tt := range []struct{ backend, err string }{
		{"", ""},
		{"auto", ""},
		{"cobra", ""},
		{"shell", ""},
		{"none", ""},
		{"zsh", `unknown backend "zsh" (use "shell")`},
		{"bogus", `unknown backend "bogus"`},
	} {
		s, err := Parse(fmt.Appendf(nil, "match: { command: x }\ncomplete: %q\n", tt.backend), "x.yaml")
		if err != nil {
			t.Fatal(err)
		}
		errs := s.Validate()
		switch {
		case tt.err == "" && len(errs) > 0:
			t.Errorf("complete %q: unexpected %v", tt.backend, errs)
		case tt.err != "" && (len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.err)):
			t.Errorf("complete %q: errors %v, want %q", tt.backend, errs, tt.err)
		}
	}
}
