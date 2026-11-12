// Package cli completes commands through the completion protocol of the CLI
// framework they are built with, such as cobra's __complete subcommand or
// clap's COMPLETE environment variable. Each framework is a Protocol; one
// Backend runs any of them.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/amimof/rx/internal/complete"
)

// Source is the completion source name of every protocol.
const Source = "cli"

// ErrUnsupported is returned when a command doesn't speak the protocol.
var ErrUnsupported = errors.New("command does not support the completion protocol")

// Protocol is a CLI framework's completion protocol. Running it executes
// the command, so it must only be used for commands known to support it.
type Protocol interface {
	// Name identifies the protocol, e.g. in a plugin's cli setting.
	Name() string
	// Marker is a string found in the shell completion functions the
	// framework generates, used to detect it without running the command.
	Marker() string
	// Command returns the arguments after the command word and the extra
	// environment that complete argv, whose last element is the word being
	// completed.
	Command(argv []string) (args, env []string)
	// Parse decodes the command's output.
	Parse(out []byte) (complete.Result, error)
}

// Protocols lists the built-in protocols in detection order.
var Protocols = []Protocol{Cobra{}, Clap{}}

// Lookup returns the protocol with the given name, or nil.
func Lookup(name string) Protocol {
	for _, p := range Protocols {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// ByMarker returns the protocol with the given marker, or nil.
func ByMarker(marker string) Protocol {
	if marker == "" {
		return nil
	}
	for _, p := range Protocols {
		if p.Marker() == marker {
			return p
		}
	}
	return nil
}

// Names returns the protocol names.
func Names() []string {
	out := make([]string, len(Protocols))
	for i, p := range Protocols {
		out[i] = p.Name()
	}
	return out
}

// Markers returns the protocol markers in detection order.
func Markers() []string {
	out := make([]string, len(Protocols))
	for i, p := range Protocols {
		out[i] = p.Marker()
	}
	return out
}

// Backend completes requests by running the command with a protocol.
type Backend struct {
	Protocol Protocol
	// Env, if set, replaces the environment of the completion process.
	// The protocol's variables are added to it.
	Env []string
}

func (b *Backend) Name() string { return Source }

// Complete runs the completion. The process is killed when ctx is done.
func (b *Backend) Complete(ctx context.Context, req complete.Request) (complete.Result, error) {
	if len(req.Argv) < 1 {
		return complete.Result{}, errors.New("empty command")
	}
	args, env := b.Protocol.Command(req.Argv)
	cmd := exec.CommandContext(ctx, req.Argv[0], args...)
	if len(env) > 0 {
		base := b.Env
		if base == nil {
			base = os.Environ()
		}
		cmd.Env = append(append([]string{}, base...), env...)
	} else {
		cmd.Env = b.Env
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	// Frameworks write debug output to stderr; discard it.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return complete.Result{}, ctx.Err()
		}
		if out.Len() == 0 {
			return complete.Result{}, fmt.Errorf("%s %s completion: %w", req.Argv[0], b.Protocol.Name(), err)
		}
	}
	return b.Protocol.Parse(out.Bytes())
}
