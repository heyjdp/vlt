package ui

import (
	"context"

	"fyne.io/fyne/v2"
)

// runner moves blocking vault work (scrypt, file I/O) off the Fyne main
// goroutine so the window never freezes. Every blocking store call in this
// package goes through background.
//
// spawn starts the work: a new goroutine in production. Tests use an inline
// or queued spawn, which together with fyne/test's inline fyne.Do makes
// app-level tests deterministic and race-free.
//
// A runner may be bound to a session context (see withContext). Work started
// on a bound runner receives that context, and its result is dropped if the
// context has been cancelled by the time it completes, so nothing finishing
// after Lock can touch the UI or act on a closed vault.
type runner struct {
	spawn func(func())
	ctx   context.Context
}

// goroutineRunner is the production runner.
func goroutineRunner() runner {
	return runner{spawn: func(f func()) { go f() }}
}

// withContext returns a copy of r bound to ctx.
func (r runner) withContext(ctx context.Context) runner {
	r.ctx = ctx
	return r
}

// context returns the runner's session context, or context.Background.
func (r runner) context() context.Context {
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

// background runs work via r.spawn with r's context, then delivers its result
// to done on the Fyne main goroutine, unless that context has been cancelled
// meanwhile. done must not block.
func background[T any](r runner, work func(context.Context) (T, error), done func(T, error)) {
	ctx := r.context()
	r.spawn(func() {
		v, err := work(ctx)
		fyne.Do(func() {
			if ctx.Err() != nil {
				return // the session ended (vault locked); drop the result
			}
			done(v, err)
		})
	})
}

// none is the result type for background work that returns only an error.
type none struct{}
