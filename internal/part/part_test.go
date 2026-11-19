package part

import (
	"strings"
	"testing"
)

// show renders the editable parts as name=text, separated by spaces.
func show(s Split) string {
	var out []string
	for _, i := range s.Editable() {
		out = append(out, s.Parts[i].Name+"="+s.Parts[i].Text)
	}
	return strings.Join(out, " ")
}

func TestParse(t *testing.T) {
	for _, tt := range []struct{ in, kind, want string }{
		{"https://localhost:7878/compute/v2/servers", KindURL,
			"scheme=https host=localhost port=7878 path[0]=compute path[1]=v2 path[2]=servers"},
		{"http://localhost:7878", KindURL, "scheme=http host=localhost port=7878"},
		{"https://admin:pw@example.com/a?x=1&y=2#top", KindURL,
			"scheme=https user=admin:pw host=example.com path[0]=a query[0]=x=1 query[1]=y=2 fragment=top"},
		{"http://[::1]:8080/", KindURL, "scheme=http host=[::1] port=8080"},
		{"http://example.com//a/", KindURL, "scheme=http host=example.com path[1]=a"},
		{"localhost:7878", KindHost, "host=localhost port=7878"},
		{"git@github.com", KindHost, "user=git host=github.com"},
		{"root@10.0.0.1:2222", KindHost, "user=root host=10.0.0.1 port=2222"},
		{"[::1]:80", KindHost, "host=[::1] port=80"},
		{"app=web", KindPair, "key=app value=web"},
		{"a=1,b=2", KindList, "item[0]=a=1 item[1]=b=2"},
		{"pod/web-0", KindPath, "path[0]=pod path[1]=web-0"},
		{"/etc/hosts", KindPath, "path[1]=etc path[2]=hosts"},
		{"nginx:1.25", KindOther, "part[0]=nginx part[1]=1.25"},
		{"X-Auth-Token: abc", KindOther, "part[0]=X-Auth-Token part[1]=abc"},
		{"Content-Type: application/json", KindOther, "part[0]=Content-Type part[1]=application part[2]=json"},
		{"a//b", KindPath, "path[0]=a path[2]=b"},
		{"pods", KindOther, "part[0]=pods"},
		{"", KindOther, ""},
	} {
		s := Parse(tt.in)
		if s.Value() != tt.in {
			t.Errorf("%q: round trip gives %q", tt.in, s.Value())
		}
		if s.Kind != tt.kind || show(s) != tt.want {
			t.Errorf("%q: got %s %q, want %s %q", tt.in, s.Kind, show(s), tt.kind, tt.want)
		}
	}
}

func TestSet(t *testing.T) {
	s := Parse("https://localhost:7878/compute/v2/servers")
	i := s.Find("port")
	got := s.Set(i, "8080")
	if v := got.Value(); v != "https://localhost:8080/compute/v2/servers" {
		t.Fatalf("Set port: %q", v)
	}
	if s.Value() != "https://localhost:7878/compute/v2/servers" {
		t.Fatal("Set changed the original")
	}
	if a, b := got.Offset(i); got.Value()[a:b] != "8080" {
		t.Fatalf("Offset = %d,%d", a, b)
	}
	if s.Find("nope") != -1 {
		t.Fatal("Find found a missing part")
	}
}
