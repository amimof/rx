package parse

import (
	"reflect"
	"testing"
)

func values(s Segment) []string {
	var out []string
	for _, w := range s.Words {
		out = append(out, w.Value)
	}
	return out
}

func TestParseSegments(t *testing.T) {
	tests := []struct {
		in   string
		want [][]string
	}{
		{"k get pods -n kube-system", [][]string{{"k", "get", "pods", "-n", "kube-system"}}},
		{"k get pods -A -o wide", [][]string{{"k", "get", "pods", "-A", "-o", "wide"}}},
		{`k get pods -l 'app=nginx' -n "prod"`, [][]string{{"k", "get", "pods", "-l", "app=nginx", "-n", "prod"}}},
		{"k get pods | grep foo && echo ok", [][]string{{"k", "get", "pods"}, {"grep", "foo"}, {"echo", "ok"}}},
		{"FOO=1 kubectl --namespace=x get po", [][]string{{"kubectl", "--namespace=x", "get", "po"}}},
		{`echo a\ b`, [][]string{{"echo", "a b"}}},
		// Unterminated quote falls back to whitespace splitting.
		{`k get pods -n 'kube`, [][]string{{"k", "get", "pods", "-n", "kube"}}},
	}
	for _, tt := range tests {
		l := Parse(tt.in)
		var got [][]string
		for _, s := range l.Segments {
			got = append(got, values(s))
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDynamicWords(t *testing.T) {
	l := Parse(`k get pods -n $NS "$(date)" *.yaml`)
	if len(l.Segments) != 2 {
		t.Fatalf("want outer command and nested $(date), got %d segments", len(l.Segments))
	}
	if l.SegmentAt(22) != 1 {
		t.Errorf("cursor inside $(date) should select the nested command")
	}
	ws := l.Segments[0].Words
	for _, i := range []int{4, 5, 6} {
		if !ws[i].Dynamic || ws[i].Value != ws[i].Raw {
			t.Errorf("word %d %q: want dynamic with raw value, got %+v", i, ws[i].Raw, ws[i])
		}
	}
}

func TestSegmentAtAndSplice(t *testing.T) {
	in := "k get pods -n a | grep x"
	l := Parse(in)
	if got := l.SegmentAt(3); got != 0 {
		t.Fatalf("SegmentAt(3) = %d", got)
	}
	if got := l.SegmentAt(len(in)); got != 1 {
		t.Fatalf("SegmentAt(end) = %d", got)
	}
	if got := l.Splice(0, "k get svc"); got != "k get svc | grep x" {
		t.Fatalf("Splice = %q", got)
	}
}

func TestWordQuote(t *testing.T) {
	ws := Parse(`k 'a b' "c" d e'f' $'g' "$NS"`).Segments[0].Words
	want := []byte{0, '\'', '"', 0, 0, 0, 0}
	for i, w := range ws {
		if w.Quote != want[i] {
			t.Errorf("word %q: Quote = %q, want %q", w.Raw, w.Quote, want[i])
		}
	}
	for raw, want := range map[string]byte{`'x'`: '\'', `"x y"`: '"', `x`: 0, `'x'y`: 0, `$'x'`: 0, `"x`: 0, ``: 0} {
		if got := QuoteOf(raw); got != want {
			t.Errorf("QuoteOf(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestQuoteAs(t *testing.T) {
	tests := []struct {
		in   string
		q    byte
		want string
	}{
		{"plain", 0, "plain"},
		{"a b", 0, "'a b'"},
		{"plain", '\'', "'plain'"},
		{"it's", '\'', `'it'\''s'`},
		{"plain", '"', `"plain"`},
		{"$x `y` \"z\" \\", '"', `"\$x \` + "`y\\`" + ` \"z\" \\"`},
		{"hi!", '"', "'hi!'"},
		{"", '"', `""`},
	}
	for _, tt := range tests {
		if got := QuoteAs(tt.in, tt.q); got != tt.want {
			t.Errorf("QuoteAs(%q, %q) = %q, want %q", tt.in, tt.q, got, tt.want)
		}
		// Quoted output must parse back to the input.
		if ws := Parse("x " + QuoteAs(tt.in, tt.q)).Segments[0].Words; ws[1].Value != tt.in {
			t.Errorf("QuoteAs(%q, %q) parses back as %q", tt.in, tt.q, ws[1].Value)
		}
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":     "plain",
		"app=nginx": "app=nginx",
		"a b":       "'a b'",
		"":          "''",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}
