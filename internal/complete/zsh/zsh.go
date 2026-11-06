// Package zsh talks to the rx zsh widget, which runs rx as a coprocess and
// answers completion requests using the live shell's completion system.
//
// Protocol, one message per line, fields separated by tabs and escaped with
// complete.Escape:
//
//	rx → zsh   INFO <word>
//	zsh → rx   I <alias expansion> <completion function> <cobra 0|1>
//	           END
//	rx → zsh   COMPLETE <line>
//	zsh → rx   C <value> <description> <group>   (zero or more)
//	           M <message>                       (zero or more)
//	           END
//	rx → zsh   DONE <cursor> <line>              (final buffer, rx exits)
//	rx → zsh   CANCEL                            (keep original buffer)
//	rx → zsh   ERROR <message>                   (keep buffer, show message)
package zsh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/amimof/rx/internal/complete"
)

// Client is the rx side of the protocol. Requests are serialized because the
// shell can only run one completion at a time.
type Client struct {
	mu  sync.Mutex
	r   *bufio.Reader
	w   io.Writer
	err error
}

// NewClient returns a client reading responses from r and writing requests
// to w (rx's stdin and stdout when run as a coprocess).
func NewClient(r io.Reader, w io.Writer) *Client {
	return &Client{r: bufio.NewReader(r), w: w}
}

// Info describes how the shell sees a command word.
type Info struct {
	Alias    string
	CompFunc string
	Cobra    bool
}

func (c *Client) send(fields ...string) error {
	for i := range fields {
		fields[i] = complete.Escape(fields[i])
	}
	_, err := fmt.Fprintln(c.w, strings.Join(fields, "\t"))
	return err
}

// readUntilEnd reads response lines until END, calling fn for each.
func (c *Client) readUntilEnd(fn func(kind string, fields []string)) error {
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "END" {
			return nil
		}
		parts := strings.Split(line, "\t")
		for i := range parts {
			parts[i] = complete.Unescape(parts[i])
		}
		fn(parts[0], parts[1:])
	}
}

// do runs one request/response exchange. A failed exchange poisons the
// client since the stream position is unknown afterwards.
func (c *Client) do(ctx context.Context, fn func(kind string, fields []string), req ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	done := make(chan error, 1)
	go func() {
		if err := c.send(req...); err != nil {
			done <- err
			return
		}
		done <- c.readUntilEnd(fn)
	}()
	select {
	case err := <-done:
		c.err = err
		return err
	case <-ctx.Done():
		// The shell is still busy; wait for it so the stream stays in sync,
		// but report the timeout to the caller.
		go func() { <-done }()
		c.err = errors.New("zsh completion timed out; backend disabled for this session")
		return ctx.Err()
	}
}

// Info asks the shell about a command word.
func (c *Client) Info(ctx context.Context, word string) (Info, error) {
	var info Info
	err := c.do(ctx, func(kind string, f []string) {
		if kind == "I" && len(f) >= 3 {
			info = Info{Alias: f[0], CompFunc: f[1], Cobra: f[2] == "1"}
		}
	}, "INFO", word)
	return info, err
}

// Backend adapts the client to complete.Backend.
type Backend struct{ Client *Client }

func (b *Backend) Name() string { return "zsh" }

// Complete asks the shell to complete req.Line with the cursor at its end.
func (b *Backend) Complete(ctx context.Context, req complete.Request) (complete.Result, error) {
	var res complete.Result
	seen := map[string]bool{}
	err := b.Client.do(ctx, func(kind string, f []string) {
		switch kind {
		case "C":
			if len(f) == 0 || seen[f[0]] {
				return
			}
			seen[f[0]] = true
			cand := complete.Candidate{Value: f[0], Source: "zsh"}
			if len(f) > 1 {
				cand.Desc = cleanDesc(f[0], f[1])
			}
			if len(f) > 2 {
				cand.Group = f[2]
			}
			res.Candidates = append(res.Candidates, cand)
		case "M":
			if len(f) > 0 {
				if res.Message != "" {
					res.Message += "\n"
				}
				res.Message += f[0]
			}
		}
	}, "COMPLETE", req.Line)
	return res, err
}

// cleanDesc strips the "value  -- " prefix that _describe puts in display
// strings, leaving just the description.
func cleanDesc(value, d string) string {
	if d == value {
		return ""
	}
	if _, after, ok := strings.Cut(d, " -- "); ok {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(strings.TrimPrefix(d, value))
}

// Done sends the final buffer to the shell.
func (c *Client) Done(line string, cursor int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send("DONE", fmt.Sprint(cursor), line)
}

// Error tells the shell to keep its original buffer and show msg.
func (c *Client) Error(msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send("ERROR", msg)
}

// Cancel tells the shell to keep its original buffer.
func (c *Client) Cancel() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send("CANCEL")
}
