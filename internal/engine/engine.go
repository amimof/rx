// Package engine selects completion backends for a slot and streams their
// results, serving cached values first and refreshing them in the background.
package engine

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/complete/cli"
	"github.com/amimof/rx/internal/complete/provider"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/shell/proto"
	"github.com/amimof/rx/internal/spec"
)

// Shell is the live shell rx runs under. It completes command lines and
// describes command words, reporting which of markers their completion
// function contains; Name identifies it as a completion source.
type Shell interface {
	complete.Backend
	Info(ctx context.Context, word string, markers ...string) (proto.Info, error)
}

// Update is a batch of candidates delivered to the editor.
type Update struct {
	// ID is the request ID passed to Start, so stale updates for a slot that
	// is no longer focused can be ignored.
	ID     uint64
	Source string
	Result complete.Result
	// Stale is set for cached results older than the TTL; a refresh follows.
	Stale bool
	Err   error
	// Done is set on the last update for a request.
	Done bool
}

// Engine runs completions.
type Engine struct {
	Shell   Shell // nil when not running under a shell widget
	Cache   *complete.Cache
	Timeout time.Duration
	TTL     time.Duration
	// Complete lists the sources enabled for commands whose spec doesn't
	// set its own; nil enables all of them.
	Complete spec.Sources

	cli      map[string]*cli.Backend // by protocol name
	mu       sync.Mutex
	infos    map[string]proto.Info
	disabled map[string]bool // cli protocol unsupported, by command
}

// New returns an engine with defaults. sh may be nil.
func New(sh Shell, cache *complete.Cache) *Engine {
	e := &Engine{
		Shell: sh, Cache: cache,
		Timeout:  2 * time.Second,
		TTL:      30 * time.Second,
		cli:      map[string]*cli.Backend{},
		infos:    map[string]proto.Info{},
		disabled: map[string]bool{},
	}
	env := os.Environ()
	for _, p := range cli.Protocols {
		e.cli[p.Name()] = &cli.Backend{Protocol: p, Env: env}
	}
	return e
}

type job struct {
	backend complete.Backend
	req     complete.Request
	ttl     time.Duration
	// key names the backend in the cache; empty for its Name.
	key string
}

// cliJob returns a job running b, cached per protocol.
func cliJob(b *cli.Backend, req complete.Request, ttl time.Duration) *job {
	return &job{backend: b, req: req, ttl: ttl, key: cli.Source + ":" + b.Protocol.Name()}
}

// execBackend adapts a YAML provider to complete.Backend.
type execBackend struct {
	name   string
	p      spec.Provider
	script string
}

func (b *execBackend) Name() string { return "provider:" + b.name }

func (b *execBackend) Complete(ctx context.Context, _ complete.Request) (complete.Result, error) {
	return provider.Run(ctx, b.name, b.p, b.script, os.Environ())
}

// Start completes slot focus of cmd (or flag names when focus is -1) and
// calls send with updates from another goroutine. cmd is read before Start
// returns, so the caller may keep mutating it.
//
// send is never called before Start returns, not even for slots with
// nothing to complete: the editor calls Start from its event loop, and
// send delivers to that same loop, so a synchronous call would deadlock.
func (e *Engine) Start(ctx context.Context, id uint64, cmd *model.Command, sp *spec.Spec, focus int, send func(Update)) {
	e.StartFrom(ctx, id, cmd, sp, focus, "", send)
}

// StartFrom is Start with the backend forced to source, one of the names
// returned by Sources. An empty source picks the backend as Start does; a
// source that isn't available completes with no candidates.
func (e *Engine) StartFrom(ctx context.Context, id uint64, cmd *model.Command, sp *spec.Spec, focus int, source string, send func(Update)) {
	en := e.Enabled(sp)
	var req complete.Request
	var prov *execBackend
	var early *Update // delivered instead of running a backend
	if focus < 0 {
		req = complete.SynthesizeFlags(cmd)
	} else {
		slot := cmd.Slots[focus]
		req = complete.Synthesize(cmd, focus)
		name := providerFor(sp, slot)
		if slot.Kind == model.Bool {
			early = &Update{ID: id, Done: true}
		} else if name != "" && en[spec.SourceProvider] && (source == "" || source == "provider:"+name) {
			p := sp.Providers[name]
			script, err := provider.Render(p, cmd)
			if err != nil {
				early = &Update{ID: id, Err: err, Done: true}
			} else {
				prov = &execBackend{name: name, p: p, script: script}
				// Make the cache key depend on the rendered script.
				req.Line = script
			}
		}
	}
	head, name := cmd.Head.Value, cmd.Name
	go func() {
		if early != nil {
			send(*early)
			return
		}
		var j *job
		if prov != nil {
			ttl := time.Duration(prov.p.TTL)
			if ttl == 0 {
				ttl = e.TTL
			}
			j = &job{backend: prov, req: req, ttl: ttl}
		} else if source != "" {
			j = e.job(e.forced(source, head, name, sp, en), req)
		} else {
			j = e.job(e.backendFor(ctx, head, name, sp, en), req)
		}
		if j == nil {
			send(Update{ID: id, Done: true})
			return
		}
		e.run(ctx, id, *j, name, send)
	}()
}

// job returns a job running b with the engine's TTL, or nil if b is nil.
func (e *Engine) job(b complete.Backend, req complete.Request) *job {
	switch b := b.(type) {
	case nil:
		return nil
	case *cli.Backend:
		return cliJob(b, req, e.TTL)
	default:
		return &job{backend: b, req: req, ttl: e.TTL}
	}
}

func (e *Engine) run(ctx context.Context, id uint64, j job, cmdName string, send func(Update)) {
	src := j.backend.Name()
	key := j.key
	if key == "" {
		key = src
	}
	if res, at, ok := e.Cache.Get(key, j.req); ok {
		stale := time.Since(at) > j.ttl
		send(Update{ID: id, Source: src, Result: res, Stale: stale, Done: !stale})
		if !stale {
			return
		}
	}
	cctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	res, err := j.backend.Complete(cctx, j.req)
	if errors.Is(err, cli.ErrUnsupported) {
		e.mu.Lock()
		e.disabled[cmdName] = true
		e.mu.Unlock()
	}
	if err == nil {
		_ = e.Cache.Put(key, j.req, res)
	}
	send(Update{ID: id, Source: src, Result: res, Err: err, Done: true})
}

// Enabled returns the sources enabled for sp's command: the spec's own, else
// the engine's default, else all of them.
func (e *Engine) Enabled(sp *spec.Spec) map[string]bool {
	return sp.Enabled(e.Complete)
}

// forced returns the cli or shell backend named source if it is enabled and
// usable for the command, or nil. Like Sources, it never blocks.
func (e *Engine) forced(source, head, name string, sp *spec.Spec, en map[string]bool) complete.Backend {
	switch {
	case source == cli.Source:
		if b := e.cliBackend(head, name, sp, en); b != nil {
			return b
		}
	case e.Shell != nil && source == e.Shell.Name() && en[spec.SourceShell]:
		return e.Shell
	}
	return nil
}

// cliBackend returns the cli backend for the command if the cli source is
// enabled, the command hasn't turned out not to support its protocol, and
// the protocol is pinned by the spec or found in a cached INFO. It never
// blocks.
func (e *Engine) cliBackend(head, name string, sp *spec.Spec, en map[string]bool) *cli.Backend {
	if !en[spec.SourceCLI] {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disabled[name] {
		return nil
	}
	if p := pinned(sp); p != nil {
		return e.cli[p.Name()]
	}
	if p := cli.ByMarker(e.infos[head].Marker); p != nil {
		return e.cli[p.Name()]
	}
	return nil
}

// pinned returns the protocol the spec pins with its cli setting, or nil.
func pinned(sp *spec.Spec) cli.Protocol {
	if sp == nil || sp.CLI == "" {
		return nil
	}
	return cli.Lookup(sp.CLI)
}

// Sources returns the names of the enabled backends that can complete slot
// focus of cmd (flag names when focus is -1): its provider, cli and the
// shell. It never blocks, so cli is only offered once the spec pins a
// protocol or a cached INFO found one; unknown binaries are never probed.
func (e *Engine) Sources(cmd *model.Command, sp *spec.Spec, focus int) []string {
	en := e.Enabled(sp)
	var out []string
	if focus >= 0 {
		slot := cmd.Slots[focus]
		if slot.Kind == model.Bool {
			return nil
		}
		if name := providerFor(sp, slot); name != "" && en[spec.SourceProvider] {
			out = append(out, "provider:"+name)
		}
	}
	if e.cliBackend(cmd.Head.Value, cmd.Name, sp, en) != nil {
		out = append(out, cli.Source)
	}
	if e.Shell != nil && en[spec.SourceShell] {
		out = append(out, e.Shell.Name())
	}
	return out
}

func providerFor(sp *spec.Spec, s model.Slot) string {
	if sp == nil {
		return ""
	}
	if s.Kind == model.Positional {
		if p := sp.PositionalByName(s.Key); p != nil {
			return p.Provider
		}
		return ""
	}
	if f := sp.Flag("--" + s.Key); f != nil {
		return f.Provider
	}
	return ""
}

// backendFor resolves the backend for a command from the enabled sources:
// with cli enabled, the protocol the spec pins, or else the one the shell's
// completion function for the command is generated by; otherwise the shell
// if it is enabled. Detection asks the shell even when the shell source is
// disabled. Unknown binaries are never probed.
func (e *Engine) backendFor(ctx context.Context, head, name string, sp *spec.Spec, en map[string]bool) complete.Backend {
	var sb complete.Backend
	if e.Shell != nil && en[spec.SourceShell] {
		sb = e.Shell
	}
	e.mu.Lock()
	disabled := e.disabled[name]
	_, known := e.infos[head]
	e.mu.Unlock()
	if !en[spec.SourceCLI] || disabled {
		return sb
	}
	if pinned(sp) == nil && !known && e.Shell != nil {
		ictx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		info, err := e.Shell.Info(ictx, head, cli.Markers()...)
		if err != nil {
			return sb
		}
		e.mu.Lock()
		e.infos[head] = info
		e.mu.Unlock()
	}
	if b := e.cliBackend(head, name, sp, en); b != nil {
		return b
	}
	return sb
}
