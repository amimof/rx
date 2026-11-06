// Package cobra completes commands built with spf13/cobra by running their
// hidden __complete subcommand.
package cobra

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/amimof/rx/internal/complete"
)

// ErrUnsupported is returned when the command doesn't speak __complete.
var ErrUnsupported = errors.New("command does not support cobra completion")

// Backend runs `<cmd> __complete <args...>`.
type Backend struct {
	// Env, if set, replaces the environment of the completion process.
	Env []string
}

func (b *Backend) Name() string { return "cobra" }

// Complete runs the completion. The process is killed when ctx is done.
func (b *Backend) Complete(ctx context.Context, req complete.Request) (complete.Result, error) {
	if len(req.Argv) < 1 {
		return complete.Result{}, errors.New("empty command")
	}
	args := append([]string{"__complete"}, req.Argv[1:]...)
	cmd := exec.CommandContext(ctx, req.Argv[0], args...)
	cmd.Env = b.Env
	var out bytes.Buffer
	cmd.Stdout = &out
	// cobra writes debug output to stderr; discard it.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return complete.Result{}, ctx.Err()
		}
		if out.Len() == 0 {
			return complete.Result{}, fmt.Errorf("%s __complete: %w", req.Argv[0], err)
		}
	}
	return Parse(out.Bytes())
}

// Parse decodes __complete output: "value[\tdescription]" lines followed by
// a ":<directive>" line.
func Parse(data []byte) (complete.Result, error) {
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
		res.Candidates = append(res.Candidates, complete.Candidate{Value: v, Desc: desc, Source: "cobra"})
	}
	return res, nil
}
