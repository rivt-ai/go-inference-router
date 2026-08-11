package transport

import (
	"context"
	"sync/atomic"
	"time"
)

// StallGuard cancels a request when the provider goes quiet for longer than the
// configured window. It bounds silence rather than total duration, which is
// what a slow model needs: minutes of steady tokens are fine, a minute of
// nothing is not.
//
// A zero or negative window disables the watchdog; the guard still owns a
// cancellable context so callers have one shape to work with.
type StallGuard struct {
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
	fired  atomic.Bool
	window time.Duration
}

// NewStallGuard derives a cancellable context from parent and arms the
// watchdog. Callers must call Stop.
func NewStallGuard(parent context.Context, window time.Duration) *StallGuard {
	ctx, cancel := context.WithCancel(parent)
	guard := &StallGuard{ctx: ctx, cancel: cancel, window: window}
	if window > 0 {
		guard.timer = time.AfterFunc(window, func() {
			guard.fired.Store(true)
			cancel()
		})
	}
	return guard
}

// Context is the request context to use for the guarded call.
func (g *StallGuard) Context() context.Context { return g.ctx }

// Window is the configured silence budget, for error messages.
func (g *StallGuard) Window() time.Duration { return g.window }

// Reset restarts the silence budget; call it on every byte of progress.
func (g *StallGuard) Reset() {
	if g.timer != nil {
		g.timer.Reset(g.window)
	}
}

// Stop disarms the watchdog and releases the context.
func (g *StallGuard) Stop() {
	if g.timer != nil {
		g.timer.Stop()
	}
	g.cancel()
}

// Stalled reports whether the watchdog, rather than the caller, cancelled the
// request. The two are indistinguishable from the context alone.
func (g *StallGuard) Stalled() bool { return g.fired.Load() }
