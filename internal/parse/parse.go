// Package parse splits a shell buffer into simple commands and words,
// keeping byte offsets so that edits can be spliced back into the original
// line without disturbing anything outside the edited command.
package parse

import (
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"
)

// Word is a single shell word of a simple command.
type Word struct {
	// Raw is the word exactly as it appears in the buffer.
	Raw string
	// Value is the unquoted value of the word. For words containing
	// expansions (variables, command substitutions, globs) Value equals Raw
	// and Dynamic is set.
	Value   string
	Dynamic bool
	// Start and End are byte offsets into the buffer.
	Start, End int
}

// Segment is one simple command within a buffer, e.g. one side of a pipe.
type Segment struct {
	Words []Word
	// Start and End delimit the words of the command (excluding leading
	// variable assignments and redirections that precede the first word).
	Start, End int
}

// Line is a parsed shell buffer.
type Line struct {
	Text     string
	Segments []Segment
}

// Parse parses buf. It never fails: buffers the shell parser rejects (for
// example an unterminated quote while the user is still typing) fall back to
// whitespace tokenization.
func Parse(buf string) *Line {
	l := &Line{Text: buf}
	f, err := syntax.NewParser().Parse(strings.NewReader(buf), "")
	if err != nil {
		l.Segments = fallback(buf)
		return l
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		seg := Segment{}
		for _, w := range call.Args {
			start, end := int(w.Pos().Offset()), int(w.End().Offset())
			val, dyn := unquote(w)
			if dyn {
				val = buf[start:end]
			}
			seg.Words = append(seg.Words, Word{Raw: buf[start:end], Value: val, Dynamic: dyn, Start: start, End: end})
		}
		seg.Start = seg.Words[0].Start
		seg.End = seg.Words[len(seg.Words)-1].End
		l.Segments = append(l.Segments, seg)
		// Keep walking: command substitutions contain their own calls.
		return true
	})
	return l
}

// SegmentAt returns the index of the segment the cursor is in or touching.
// If the cursor is past every segment the last one preceding it is returned.
// Returns -1 if the buffer has no commands.
func (l *Line) SegmentAt(cursor int) int {
	best := -1
	for i, s := range l.Segments {
		if cursor >= s.Start && cursor <= s.End {
			// Prefer the innermost (latest starting) segment, which handles
			// command substitutions nested in an outer command.
			if best == -1 || s.Start >= l.Segments[best].Start {
				best = i
			}
		}
	}
	if best != -1 {
		return best
	}
	for i, s := range l.Segments {
		if s.End <= cursor {
			best = i
		}
	}
	return best
}

// Splice returns the buffer with segment i replaced by text.
func (l *Line) Splice(i int, text string) string {
	s := l.Segments[i]
	return l.Text[:s.Start] + text + l.Text[s.End:]
}

func unquote(w *syntax.Word) (string, bool) {
	var sb strings.Builder
	for _, p := range w.Parts {
		if !unquotePart(&sb, p) {
			return "", true
		}
	}
	return sb.String(), false
}

func unquotePart(sb *strings.Builder, p syntax.WordPart) bool {
	switch p := p.(type) {
	case *syntax.Lit:
		v := p.Value
		if strings.ContainsAny(v, "*?[") {
			return false
		}
		// Unescape backslashes outside quotes.
		for i := 0; i < len(v); i++ {
			if v[i] == '\\' && i+1 < len(v) {
				i++
				if v[i] == '\n' {
					continue
				}
			}
			sb.WriteByte(v[i])
		}
		return true
	case *syntax.SglQuoted:
		if p.Dollar {
			return false
		}
		sb.WriteString(p.Value)
		return true
	case *syntax.DblQuoted:
		if p.Dollar {
			return false
		}
		for _, q := range p.Parts {
			lit, ok := q.(*syntax.Lit)
			if !ok {
				return false
			}
			v := lit.Value
			for i := 0; i < len(v); i++ {
				if v[i] == '\\' && i+1 < len(v) && strings.IndexByte("$`\"\\\n", v[i+1]) >= 0 {
					i++
				}
				sb.WriteByte(v[i])
			}
		}
		return true
	}
	return false
}

// fallback tokenizes on whitespace and splits segments on |, &&, || and ;.
func fallback(buf string) []Segment {
	var segs []Segment
	cur := Segment{}
	flush := func() {
		if len(cur.Words) > 0 {
			cur.Start = cur.Words[0].Start
			cur.End = cur.Words[len(cur.Words)-1].End
			segs = append(segs, cur)
		}
		cur = Segment{}
	}
	i := 0
	for i < len(buf) {
		if unicode.IsSpace(rune(buf[i])) {
			i++
			continue
		}
		j := i
		for j < len(buf) && !unicode.IsSpace(rune(buf[j])) {
			j++
		}
		tok := buf[i:j]
		switch tok {
		case "|", "||", "&&", ";", "&":
			flush()
		default:
			cur.Words = append(cur.Words, Word{Raw: tok, Value: strings.Trim(tok, `'"`), Start: i, End: j})
		}
		i = j
	}
	flush()
	return segs
}

// Quote returns s quoted for use as an argument if needed. Unlike
// syntax.Quote it leaves "=" alone, since arguments are never assignments.
// A leading "=" is quoted because zsh would expand it to a command path.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := s[0] != '='
	for i := 0; safe && i < len(s); i++ {
		c := s[i]
		safe = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("_@%+=:,./-", c) >= 0
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
