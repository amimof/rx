package history

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

const fixture = `: 1700000000:0;k get pods -n kube-system
: 1700000100:0;k get pods -n monitoring
: 1700000200:0;k get pods -A -o wide
: 1700000300:0;k get pods my-pod-123 -n production
: 1700000400:0;k get pods -l app=nginx -n production
: 1700000500:0;for f in a b; do\
echo $f\
done
: 1700000600:0;mytool --env staging deploy
: 1700000700:0;mytool --env prod deploy
: 1700000800:0;mytool --env prod rollback
: 1700000900:0;mytool -v --env staging status
: 1700001000:0;mytool -v --env prod status
: 1700001100:0;mytool -v -q status
`

func classifier(t *testing.T) spec.Classifier {
	t.Helper()
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, ""); err != nil {
		t.Fatal(err)
	}
	return spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
}

func TestReadZsh(t *testing.T) {
	es, err := ReadZsh(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 12 {
		t.Fatalf("got %d entries", len(es))
	}
	if es[5].Cmd != "for f in a b; do\necho $f\ndone" || es[5].Time != 1700000500 {
		t.Fatalf("multi-line entry = %+v", es[5])
	}
}

func TestReadBash(t *testing.T) {
	for _, tt := range []struct {
		name, in string
		want     []Entry
	}{
		{"plain", "ls -la\ngit status\n\n#not a timestamp\n", []Entry{
			{Cmd: "ls -la"}, {Cmd: "git status"}, {Cmd: ""}, {Cmd: "#not a timestamp"},
		}},
		{"timestamps", "#1700000000\nls -la\n#1700000100\nfor f in a b; do\necho $f\ndone\n#1700000200\ngit status\n", []Entry{
			{1700000000, "ls -la"}, {1700000100, "for f in a b; do\necho $f\ndone"}, {1700000200, "git status"},
		}},
		// Load reads appended data starting at an entry boundary, which with
		// timestamps is the timestamp line.
		{"appended", "#1700000300\nk get pods\n", []Entry{{1700000300, "k get pods"}}},
		{"no trailing newline", "#1700000000\nls", []Entry{{1700000000, "ls"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			es, err := ReadBash(strings.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(es) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", es, tt.want)
			}
			for i := range es {
				if es[i] != tt.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, es[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadFish(t *testing.T) {
	for _, tt := range []struct {
		name, in string
		want     []Entry
	}{
		{"entries", "- cmd: ls -la\n  when: 1700000000\n- cmd: git status\n  when: 1700000100\n  paths:\n    - status\n", []Entry{
			{1700000000, "ls -la"}, {1700000100, "git status"},
		}},
		{"escapes", `- cmd: for f in a b\n  echo $f\nend
  when: 1700000200
- cmd: echo a\\nb \x
  when: 1700000300
`, []Entry{
			{1700000200, "for f in a b\n  echo $f\nend"}, {1700000300, `echo a\nb \x`},
		}},
		{"no time", "- cmd: ls\n", []Entry{{Cmd: "ls"}}},
		// Load reads appended data starting at an entry boundary.
		{"appended", "- cmd: k get pods\n  when: 1700000400\n", []Entry{{1700000400, "k get pods"}}},
		{"empty", "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			es, err := ReadFish(strings.NewReader(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(es) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", es, tt.want)
			}
			for i := range es {
				if es[i] != tt.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, es[i], tt.want[i])
				}
			}
		})
	}
}

func TestUnmetafy(t *testing.T) {
	// "é" is 0xc3 0xa9; zsh metafies bytes >= 0x83, but only 0x83..0x9f and
	// a few others in practice. Exercise the transform itself.
	in := []byte{'a', meta, 0xa9 ^ 32, 'b'}
	if got := unmetafy(in); string(got) != "a\xa9b" {
		t.Fatalf("got %q", got)
	}
}

func TestIndex(t *testing.T) {
	es, _ := ReadZsh(strings.NewReader(fixture))
	x := Build(es, classifier(t))
	now := time.Unix(1700002000, 0)

	ns := x.Candidates("kubectl", "namespace", now)
	if len(ns) != 3 || ns[0].Value != "production" || ns[0].Count != 2 {
		t.Fatalf("namespace candidates = %+v", ns)
	}
	if got := x.Candidates("kubectl", "selector", now); len(got) != 1 || got[0].Value != "app=nginx" {
		t.Fatalf("selector candidates = %+v", got)
	}
	if got := x.Candidates("kubectl", "name", now); len(got) != 1 || got[0].Value != "my-pod-123" {
		t.Fatalf("name candidates = %+v", got)
	}

	// No spec for mytool: --env is inferred to take a value, -v is not.
	if tv, ok := x.TakesValue("mytool", "--env"); !tv || !ok {
		t.Errorf("--env: takes=%v ok=%v", tv, ok)
	}
	if tv, ok := x.TakesValue("mytool", "-v"); tv || !ok {
		t.Errorf("-v: takes=%v ok=%v", tv, ok)
	}
	env := x.Candidates("mytool", "env", now)
	if len(env) != 2 || env[0].Value != "prod" {
		t.Fatalf("env candidates = %+v", env)
	}

	c := classifier(t)
	c.Infer = x
	cmd := c.Classify(parse.Parse("mytool -v --env prod status"), 0)
	if len(cmd.Slots) != 3 || cmd.Slots[1].Value != "prod" || cmd.Slots[2].Value != "status" {
		t.Fatalf("classified with inference = %+v", cmd.Slots)
	}
}

func TestIndexAliasToWrapper(t *testing.T) {
	c := classifier(t)
	c.Aliases = spec.Aliases{"k": "kubecolor"}
	x := Build([]Entry{
		{Cmd: "k get pods -n monitoring", Time: 1700000000},
		{Cmd: "kubectl get pods -n monitoring", Time: 1700000100},
	}, c)
	ns := x.Candidates("kubectl", "namespace", time.Unix(1700002000, 0))
	if len(ns) != 1 || ns[0].Count != 2 {
		t.Fatalf("namespace candidates = %+v", ns)
	}
}

func TestLoadCache(t *testing.T) {
	dir := t.TempDir()
	hf := filepath.Join(dir, "hist")
	if err := os.WriteFile(hf, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	for range 2 {
		x, err := Load(hf, cache, "salt", ReadZsh, classifier(t))
		if err != nil {
			t.Fatal(err)
		}
		if len(x.Candidates("kubectl", "namespace", time.Now())) != 3 {
			t.Fatal("bad index")
		}
	}
	m, _ := filepath.Glob(filepath.Join(cache, "history-*.gob"))
	if len(m) != 1 {
		t.Fatalf("want one cache file, got %v", m)
	}

	// Appending indexes only the new entry on top of the cached counts.
	f, _ := os.OpenFile(hf, os.O_APPEND|os.O_WRONLY, 0)
	if _, err := f.WriteString(": 1700002000:0;k get pods -n production\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	x, err := Load(hf, cache, "salt", ReadZsh, classifier(t))
	if err != nil {
		t.Fatal(err)
	}
	if c := x.Candidates("kubectl", "namespace", time.Now()); c[0].Value != "production" || c[0].Count != 3 {
		t.Fatalf("after append = %+v", c)
	}

	// Rewriting the file (e.g. history dedup) forces a full rebuild.
	if err := os.WriteFile(hf, []byte(": 1:0;k get pods -n only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	x, err = Load(hf, cache, "salt", ReadZsh, classifier(t))
	if err != nil {
		t.Fatal(err)
	}
	if c := x.Candidates("kubectl", "namespace", time.Now()); len(c) != 1 || c[0].Value != "only" {
		t.Fatalf("after rewrite = %+v", c)
	}
}

func TestLoadReader(t *testing.T) {
	dir := t.TempDir()
	hf := filepath.Join(dir, "hist")
	if err := os.WriteFile(hf, []byte("k get pods -n a\nk get pods -n b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Load must parse with the reader it is given, not ReadZsh.
	read := func(r io.Reader) ([]Entry, error) {
		es, err := ReadZsh(r)
		for i := range es {
			es[i].Cmd = strings.Replace(es[i].Cmd, "-n ", "-n x", 1)
		}
		return es, err
	}
	x, err := Load(hf, "", "salt", read, classifier(t))
	if err != nil {
		t.Fatal(err)
	}
	c := x.Candidates("kubectl", "namespace", time.Now())
	if len(c) != 2 || c[0].Value != "xa" || c[1].Value != "xb" {
		t.Fatalf("candidates = %+v", c)
	}
}

func BenchmarkBuild50k(b *testing.B) {
	lines := strings.Split(strings.TrimSpace(fixture), "\n")
	var sb strings.Builder
	for i := 0; sb.Len() == 0 || i < 50000; i++ {
		sb.WriteString(lines[i%len(lines)])
		sb.WriteByte('\n')
	}
	es, _ := ReadZsh(strings.NewReader(sb.String()))
	reg := spec.NewRegistry()
	_ = reg.LoadFS(plugins.FS, "")
	c := spec.Classifier{Specs: reg, Aliases: spec.Aliases{"k": "kubectl"}}
	b.ResetTimer()
	for range b.N {
		Build(es, c)
	}
}
