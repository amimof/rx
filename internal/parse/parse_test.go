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
