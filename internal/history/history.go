// Package history reads shell history and indexes the values used for each
// slot of each command.
package history

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
)

// Entry is one history command.
type Entry struct {
	Time int64
	Cmd  string
}

// Reader parses a history file. Load indexes appended data by calling it on
// the file from where the last read ended, so the format must be
// append-only and a reader must accept starting at any entry boundary.
type Reader func(io.Reader) ([]Entry, error)

// meta is the zsh metafication marker: the following byte is xor 32.
const meta = 0x83

func unmetafy(b []byte) []byte {
	if bytes.IndexByte(b, meta) < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == meta && i+1 < len(b) {
			i++
			out = append(out, b[i]^32)
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// ReadZsh parses a zsh history file in plain or EXTENDED_HISTORY format.
func ReadZsh(r io.Reader) ([]Entry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = unmetafy(data)
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var cur *Entry
	cont := false
	for sc.Scan() {
		line := sc.Text()
		if cont && cur != nil {
			cur.Cmd += "\n" + line
		} else {
			e := Entry{Cmd: line}
			// ": 1700000000:0;cmd"
			if strings.HasPrefix(line, ": ") {
				if semi := strings.IndexByte(line, ';'); semi > 0 {
					ts, _, _ := strings.Cut(line[2:semi], ":")
					if t, err := strconv.ParseInt(ts, 10, 64); err == nil {
						e = Entry{Time: t, Cmd: line[semi+1:]}
					}
				}
			}
			out = append(out, e)
			cur = &out[len(out)-1]
		}
		// zsh escapes embedded newlines with a trailing backslash.
		cont = strings.HasSuffix(cur.Cmd, "\\")
		if cont {
			cur.Cmd = strings.TrimSuffix(cur.Cmd, "\\")
		}
	}
	return out, sc.Err()
}

// Stat records how often and how recently a value was used.
type Stat struct {
	Count int
	Last  int64
}

// FlagStat counts how often a flag was followed by a non-flag word.
type FlagStat struct {
	Total, WithValue int
}

// FlagsKey is the pseudo slot key under which flag names are recorded.
const FlagsKey = "__flags"

// Index maps command → slot key → value → stats.
type Index struct {
	Values map[string]map[string]map[string]*Stat
	Flags  map[string]map[string]*FlagStat

	// Size is how many bytes of the history file have been indexed and Tail
	// holds the bytes just before Size, used to detect a rewritten file.
	Size int64
	Tail []byte
}

func newIndex() *Index {
	return &Index{Values: map[string]map[string]map[string]*Stat{}, Flags: map[string]map[string]*FlagStat{}}
}

// TakesValue implements spec.Inferrer: a flag takes a value if at least 80%
// of at least 3 uses are followed by a non-flag word.
func (x *Index) TakesValue(cmd, flag string) (bool, bool) {
	if x == nil {
		return false, false
	}
	st := x.Flags[cmd][flag]
	if st == nil || st.Total < 3 {
		return false, false
	}
	r := float64(st.WithValue) / float64(st.Total)
	switch {
	case r >= 0.8:
		return true, true
	case r <= 0.2:
		return false, true
	}
	return false, false
}

func (x *Index) record(cmd, key, value string, ts int64) {
	byKey := x.Values[cmd]
	if byKey == nil {
		byKey = map[string]map[string]*Stat{}
		x.Values[cmd] = byKey
	}
	byVal := byKey[key]
	if byVal == nil {
		byVal = map[string]*Stat{}
		byKey[key] = byVal
	}
	st := byVal[value]
	if st == nil {
		st = &Stat{}
		byVal[value] = st
	}
	st.Count++
	if ts > st.Last {
		st.Last = ts
	}
}

// Build indexes entries. The classifier's Infer field is replaced by the
// index under construction so that flags without a spec are inferred from
// the same history.
func Build(entries []Entry, c spec.Classifier) *Index {
	x := newIndex()
	x.add(entries, c)
	return x
}

func (x *Index) add(entries []Entry, c spec.Classifier) {
	type parsed struct {
		line *parse.Line
		ts   int64
	}
	lines := make([]parsed, 0, len(entries))
	// Pass 1: flag statistics.
	for _, e := range entries {
		l := parse.Parse(e.Cmd)
		lines = append(lines, parsed{l, e.Time})
		for _, s := range l.Segments {
			name := c.Aliases.Resolve(s.Words[0].Value)
			ws := s.Words[1:]
			for i, w := range ws {
				v := w.Value
				if w.Dynamic || len(v) < 2 || v[0] != '-' || v == "--" || strings.Contains(v, "=") {
					continue
				}
				fl := x.Flags[name]
				if fl == nil {
					fl = map[string]*FlagStat{}
					x.Flags[name] = fl
				}
				st := fl[v]
				if st == nil {
					st = &FlagStat{}
					fl[v] = st
				}
				st.Total++
				if i+1 < len(ws) && !strings.HasPrefix(ws[i+1].Value, "-") {
					st.WithValue++
				}
			}
		}
	}
	// Pass 2: slot values.
	c.Infer = x
	for _, p := range lines {
		for i := range p.line.Segments {
			cmd := c.Classify(p.line, i)
			for _, s := range cmd.Slots {
				switch s.Kind {
				case model.Bool:
					x.record(cmd.Name, FlagsKey, s.Name, p.ts)
				case model.Flag:
					x.record(cmd.Name, FlagsKey, s.Name, p.ts)
					fallthrough
				default:
					if s.Value != "" {
						x.record(cmd.Name, s.Key, s.Value, p.ts)
					}
				}
			}
		}
	}
}

// Candidate is a ranked value.
type Candidate struct {
	Value string
	Count int
	Score float64
}

// Candidates returns the values seen for cmd's slot key ranked by frecency.
func (x *Index) Candidates(cmd, key string, now time.Time) []Candidate {
	if x == nil {
		return nil
	}
	var out []Candidate
	for v, st := range x.Values[cmd][key] {
		out = append(out, Candidate{Value: v, Count: st.Count, Score: Frecency(st, now)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// Frecency combines use count and recency. Entries without timestamps
// (plain history format) rank on count alone.
func Frecency(st *Stat, now time.Time) float64 {
	s := math.Log1p(float64(st.Count))
	if st.Last > 0 {
		ageHours := now.Sub(time.Unix(st.Last, 0)).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		s += 2 / (1 + ageHours/24)
	}
	return s
}

// tailLen is how many bytes before the indexed size are compared to detect
// a history file that was rewritten rather than appended to.
const tailLen = 256

// Load returns the index for histfile, parsed with read, using an on-disk
// cache in cacheDir. The cache is keyed by histfile and salt (which should
// change whenever specs, aliases or the reader change). When the file has
// only been appended to since the cache was written, just the new entries
// are indexed.
func Load(histfile, cacheDir, salt string, read Reader, c spec.Classifier) (*Index, error) {
	f, err := os.Open(histfile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(fmt.Appendf(nil, "v2\x00%s\x00%s", histfile, salt))
	cachePath := ""
	var x *Index
	if cacheDir != "" {
		cachePath = filepath.Join(cacheDir, "history-"+hex.EncodeToString(h[:8])+".gob")
		x = readCache(cachePath)
	}
	if x != nil && !x.validPrefix(f, fi.Size()) {
		x = nil
	}
	if x != nil && x.Size == fi.Size() {
		return x, nil
	}
	if x == nil {
		x = newIndex()
	}
	if _, err := f.Seek(x.Size, io.SeekStart); err != nil {
		return nil, err
	}
	entries, err := read(f)
	if err != nil {
		return nil, err
	}
	x.add(entries, c)
	x.Size = fi.Size()
	x.Tail = readTail(f, x.Size)
	if cachePath != "" {
		_ = writeCache(cacheDir, cachePath, x)
	}
	return x, nil
}

func readTail(f *os.File, size int64) []byte {
	off := max(size-tailLen, 0)
	b := make([]byte, size-off)
	if _, err := f.ReadAt(b, off); err != nil {
		return nil
	}
	return b
}

// validPrefix reports whether the file still starts with what was indexed.
func (x *Index) validPrefix(f *os.File, size int64) bool {
	if size < x.Size {
		return false
	}
	return bytes.Equal(readTail(f, x.Size), x.Tail)
}

func readCache(path string) *Index {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	x := newIndex()
	if err := gob.NewDecoder(f).Decode(x); err != nil {
		return nil
	}
	return x
}

func writeCache(dir, path string, x *Index) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "history-*.tmp")
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(tmp).Encode(x); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
