// Package part splits a slot value into parts that can be edited on their
// own, such as the host and port of a URL.
package part

import (
	"fmt"
	"regexp"
	"strings"
)

// Kinds of values Parse recognises.
const (
	KindURL   = "url"
	KindHost  = "host"  // [user@]host[:port]
	KindPair  = "pair"  // key=value
	KindList  = "list"  // a,b,c
	KindPath  = "path"  // a/b/c
	KindOther = "other" // split on common delimiters
)

// Part is a piece of a value. Fixed parts are separators, which can't be
// edited. Parts with the same name in values of the same kind hold the same
// kind of thing, e.g. "port".
type Part struct {
	Name  string
	Text  string
	Fixed bool
}

// Split is a value cut into parts. Joining the parts' text gives the value
// back exactly.
type Split struct {
	Kind  string
	Parts []Part
}

// Value returns the parts joined.
func (s Split) Value() string {
	var b strings.Builder
	for _, p := range s.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// Offset returns the byte range of part i in Value.
func (s Split) Offset(i int) (start, end int) {
	for _, p := range s.Parts[:i] {
		start += len(p.Text)
	}
	return start, start + len(s.Parts[i].Text)
}

// Set returns a copy of s with part i's text set to text.
func (s Split) Set(i int, text string) Split {
	ps := make([]Part, len(s.Parts))
	copy(ps, s.Parts)
	ps[i].Text = text
	return Split{Kind: s.Kind, Parts: ps}
}

// Editable returns the indexes of the parts that aren't separators.
func (s Split) Editable() []int {
	var out []int
	for i, p := range s.Parts {
		if !p.Fixed {
			out = append(out, i)
		}
	}
	return out
}

// Find returns the index of the editable part named name, or -1.
func (s Split) Find(name string) int {
	for i, p := range s.Parts {
		if !p.Fixed && p.Name == name {
			return i
		}
	}
	return -1
}

// Parse splits v with the first detector that recognises it: a URL,
// [user@]host[:port], key=value, a comma list, a slash path, and finally
// any of the delimiters / : = , @ ? & ;. A value with nothing to split is
// a single part.
func Parse(v string) Split {
	for _, d := range []func(string) (Split, bool){parseURL, parseHost, parsePair, parseList, parsePath} {
		if s, ok := d(v); ok && len(s.Editable()) > 1 {
			return s
		}
	}
	return parseOther(v)
}

// builder collects parts, leaving out empty ones.
type builder struct{ s Split }

func (b *builder) part(name, text string) {
	if text != "" {
		b.s.Parts = append(b.s.Parts, Part{Name: name, Text: text})
	}
}

func (b *builder) sep(text string) {
	if text != "" {
		b.s.Parts = append(b.s.Parts, Part{Text: text, Fixed: true})
	}
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// parseURL splits scheme://[user@]host[:port][/path][?query][#fragment].
// Path segments are path[0], path[1]… and query parameters query[0]….
func parseURL(v string) (Split, bool) {
	loc := schemeRe.FindStringIndex(v)
	if loc == nil {
		return Split{}, false
	}
	b := builder{Split{Kind: KindURL}}
	scheme := v[:loc[1]-3]
	b.part("scheme", scheme)
	b.sep("://")
	rest := v[loc[1]:]

	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority, rest := rest[:end], rest[end:]
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		b.part("user", authority[:i])
		b.sep("@")
		authority = authority[i+1:]
	}
	host, port := splitHostPort(authority)
	b.part("host", host)
	if port != "" || strings.HasSuffix(authority, ":") {
		b.sep(":")
		b.part("port", port)
	}

	path, query, fragment := rest, "", ""
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path, fragment = path[:i], path[i+1:]
	}
	hasQuery := false
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path, query, hasQuery = path[:i], path[i+1:], true
	}
	if path != "" {
		for i, seg := range strings.Split(path[1:], "/") {
			b.sep("/")
			b.part(fmt.Sprintf("path[%d]", i), seg)
		}
	}
	if hasQuery {
		b.sep("?")
		for i, q := range strings.Split(query, "&") {
			if i > 0 {
				b.sep("&")
			}
			b.part(fmt.Sprintf("query[%d]", i), q)
		}
	}
	if strings.Contains(rest, "#") {
		b.sep("#")
		b.part("fragment", fragment)
	}
	return b.s, true
}

// splitHostPort splits host[:port], where host may be a bracketed IPv6
// address. A port that isn't numeric is left in host.
func splitHostPort(s string) (host, port string) {
	if strings.HasPrefix(s, "[") {
		if i := strings.IndexByte(s, ']'); i >= 0 {
			if p, ok := strings.CutPrefix(s[i+1:], ":"); ok && digits(p) {
				return s[:i+1], p
			}
			return s, ""
		}
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && digits(s[i+1:]) && !strings.Contains(s[:i], ":") {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var hostRe = regexp.MustCompile(`^(?:([^@/:\s]+)@)?(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9_-][A-Za-z0-9._-]*)(?::([0-9]+))?$`)

// parseHost splits user@host, host:port and user@host:port.
func parseHost(v string) (Split, bool) {
	m := hostRe.FindStringSubmatch(v)
	if m == nil || m[1] == "" && m[3] == "" {
		return Split{}, false
	}
	b := builder{Split{Kind: KindHost}}
	if m[1] != "" {
		b.part("user", m[1])
		b.sep("@")
	}
	b.part("host", m[2])
	if m[3] != "" {
		b.sep(":")
		b.part("port", m[3])
	}
	return b.s, true
}

var pairRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*)=([^,]+)$`)

// parsePair splits key=value.
func parsePair(v string) (Split, bool) {
	m := pairRe.FindStringSubmatch(v)
	if m == nil {
		return Split{}, false
	}
	b := builder{Split{Kind: KindPair}}
	b.part("key", m[1])
	b.sep("=")
	b.part("value", m[2])
	return b.s, true
}

// parseList splits a comma list into item[0], item[1]….
func parseList(v string) (Split, bool) {
	return splitOn(v, KindList, ",", "item")
}

// parsePath splits a slash path into path[0], path[1]…. Values with
// spaces or other delimiters are left to parseOther.
func parsePath(v string) (Split, bool) {
	if strings.ContainsAny(v, " \t"+strings.ReplaceAll(delims, "/", "")) {
		return Split{}, false
	}
	return splitOn(v, KindPath, "/", "path")
}

// splitOn splits v on sep, naming the pieces name[i] by their position.
func splitOn(v, kind, sep, name string) (Split, bool) {
	if !strings.Contains(v, sep) {
		return Split{}, false
	}
	b := builder{Split{Kind: kind}}
	for i, s := range strings.Split(v, sep) {
		if i > 0 {
			b.sep(sep)
		}
		b.part(fmt.Sprintf("%s[%d]", name, i), s)
	}
	return b.s, true
}

const delims = "/:=,@?&;"

// parseOther splits v on runs of delims and whitespace, naming the pieces
// part[0], part[1]… in order.
func parseOther(v string) Split {
	b := builder{Split{Kind: KindOther}}
	isSep := func(c byte) bool { return strings.IndexByte(delims+" \t", c) >= 0 }
	n := 0
	for i := 0; i < len(v); {
		j := i
		for j < len(v) && isSep(v[j]) == isSep(v[i]) {
			j++
		}
		if isSep(v[i]) {
			b.sep(v[i:j])
		} else {
			b.part(fmt.Sprintf("part[%d]", n), v[i:j])
			n++
		}
		i = j
	}
	return b.s
}
