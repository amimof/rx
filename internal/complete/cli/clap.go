package cli

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/amimof/rx/internal/complete"
)

// Clap is clap_complete's CompleteEnv protocol in its fish dialect:
// `COMPLETE=fish <cmd> -- <cmd> <args...> <word>`, which completes the last
// word and prints "value[\tdescription]" lines. Commands that set a
// different variable with CompleteEnv::var aren't supported.
type Clap struct{}

func (Clap) Name() string { return "clap" }

// Marker is a variable only clap's dynamic zsh registration sets.
func (Clap) Marker() string { return "_CLAP_COMPLETE_INDEX" }

func (Clap) Command(argv []string) ([]string, []string) {
	return append([]string{"--"}, argv...), []string{"COMPLETE=fish"}
}

// Parse decodes the fish dialect's output. Unlike cobra's, it has no
// trailer, so any output is accepted.
func (Clap) Parse(data []byte) (complete.Result, error) {
	var res complete.Result
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		v, desc, _ := strings.Cut(sc.Text(), "\t")
		if v != "" {
			res.Candidates = append(res.Candidates, complete.Candidate{Value: v, Desc: desc, Source: Source})
		}
	}
	return res, sc.Err()
}
