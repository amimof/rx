package cli

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"

	"github.com/amimof/rx/internal/complete"
)

// Cobra is spf13/cobra's protocol: `<cmd> __complete <args...>`.
type Cobra struct{}

func (Cobra) Name() string { return "cobra" }

// Marker is the hidden subcommand cobra's generated scripts call.
func (Cobra) Marker() string { return "__complete" }

func (Cobra) Command(argv []string) ([]string, []string) {
	return append([]string{"__complete"}, argv[1:]...), nil
}

// Parse decodes __complete output: "value[\tdescription]" lines followed by
// a ":<directive>" line.
func (Cobra) Parse(data []byte) (complete.Result, error) {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return complete.Result{}, err
	}
	// The directive is the last non-empty line.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], ":") {
		return complete.Result{}, ErrUnsupported
	}
	d, err := strconv.Atoi(lines[len(lines)-1][1:])
	if err != nil {
		return complete.Result{}, ErrUnsupported
	}
	res := complete.Result{Directive: complete.Directive(d)}
	if res.Directive&complete.DirectiveError != 0 {
		return res, errors.New("completion returned an error")
	}
	for _, l := range lines[:len(lines)-1] {
		if l == "" {
			continue
		}
		v, desc, _ := strings.Cut(l, "\t")
		// Active help lines are prefixed with "_activeHelp_ ".
		if after, ok := strings.CutPrefix(v, "_activeHelp_ "); ok {
			if res.Message != "" {
				res.Message += "\n"
			}
			res.Message += after
			continue
		}
		res.Candidates = append(res.Candidates, complete.Candidate{Value: v, Desc: desc, Source: Source})
	}
	return res, nil
}
