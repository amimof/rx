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
	"github.com/amimof/rx/internal/complete/cobra"
	"github.com/amimof/rx/internal/complete/provider"
	"github.com/amimof/rx/internal/complete/zsh"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/spec"
)

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
	Zsh     *zsh.Client // nil when not running under the zsh widget
	Cache   *complete.Cache
	Timeout time.Duration
	TTL     time.Duration

	cobra    cobra.Backend
	mu       sync.Mutex
	infos    map[string]zsh.Info
	disabled map[string]bool // cobra unsupported, by command
}

// New returns an engine with defaults.
func New(z *zsh.Client, cache *complete.Cache) *Engine {
	return &Engine{
		Zsh: z, Cache: cache,
		Timeout:  2 * time.Second,
		TTL:      30 * time.Second,
		cobra:    cobra.Backend{Env: os.Environ()},
		infos:    map[string]zsh.Info{},
		disabled: map[string]bool{},
	}
}

type job struct {
	backend complete.Backend
	req     complete.Request
	ttl     time.Duration
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
func (e *Engine) Start(ctx context.Context, id uint64, cmd *model.Command, sp *spec.Spec, focus int, send func(Update)) {
	var req complete.Request
	var prov *execBackend
	if focus < 0 {
		req = complete.SynthesizeFlags(cmd)
	} else {
		slot := cmd.Slots[focus]
		if slot.Kind == model.Bool {
			send(Update{ID: id, Done: true})
			return
		}
		req = complete.Synthesize(cmd, focus)
		if name := providerFor(sp, slot); name != "" {
			p := sp.Providers[name]
			script, err := provider.Render(p, cmd)
			if err != nil {
				send(Update{ID: id, Err: err, Done: true})
				return
			}
			prov = &execBackend{name: name, p: p, script: script}
			// Make the cache key depend on the rendered script.
			req.Line = script
		}
	}
	head, name := cmd.Head.Value, cmd.Name
	go func() {
		var j *job
		if prov != nil {
			ttl := time.Duration(prov.p.TTL)
			if ttl == 0 {
				ttl = e.TTL
			}
			j = &job{backend: prov, req: req, ttl: ttl}
		} else if b := e.backendFor(ctx, head, name, sp); b != nil {
			j = &job{backend: b, req: req, ttl: e.TTL}
		}
		if j == nil {
			send(Update{ID: id, Done: true})
			return
		}
		e.run(ctx, id, *j, name, send)
	}()
}

func (e *Engine) run(ctx context.Context, id uint64, j job, cmdName string, send func(Update)) {
	src := j.backend.Name()
	if res, at, ok := e.Cache.Get(src, j.req); ok {
		stale := time.Since(at) > j.ttl
		send(Update{ID: id, Source: src, Result: res, Stale: stale, Done: !stale})
		if !stale {
			return
		}
	}
	cctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	res, err := j.backend.Complete(cctx, j.req)
	if errors.Is(err, cobra.ErrUnsupported) {
		e.mu.Lock()
		e.disabled[cmdName] = true
		e.mu.Unlock()
	}
	if err == nil {
		_ = e.Cache.Put(src, j.req, res)
	}
	send(Update{ID: id, Source: src, Result: res, Err: err, Done: true})
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

// backendFor resolves the backend for a command: the spec's explicit choice,
// or in auto mode cobra if the shell's completion function for it is
// cobra-generated, otherwise zsh. Unknown binaries are never probed with
// __complete.
func (e *Engine) backendFor(ctx context.Context, head, name string, sp *spec.Spec) complete.Backend {
	var zb complete.Backend
	if e.Zsh != nil {
		zb = &zsh.Backend{Client: e.Zsh}
	}
	e.mu.Lock()
	disabled := e.disabled[name]
	e.mu.Unlock()
	switch sp.Backend() {
	case spec.CompleteNone:
		return nil
	case spec.CompleteCobra:
		if disabled {
			return zb
		}
		return &e.cobra
	case spec.CompleteZsh:
		return zb
	}
	if e.Zsh == nil {
		return nil
	}
	e.mu.Lock()
	info, ok := e.infos[head]
	e.mu.Unlock()
	if !ok {
		ictx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		var err error
		if info, err = e.Zsh.Info(ictx, head); err != nil {
			return zb
		}
		e.mu.Lock()
		e.infos[head] = info
		e.mu.Unlock()
	}
	if info.Cobra && !disabled {
		return &e.cobra
	}
	return zb
}
