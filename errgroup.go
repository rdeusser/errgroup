// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package errgroup provides synchronization, error propagation, and Context
// cancelation for groups of goroutines working on subtasks of a common task.
package errgroup

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// A Group is a collection of goroutines working on subtasks that are part of
// the same overall task.
//
// A zero Group is valid and does not cancel on error.
type Group struct {
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	stop         chan struct{}
	stopOnce     sync.Once
	finally      func() error
	finallyOnce  sync.Once
	catchSignals bool
	errOnce      sync.Once
	err          error
}

// WithSignalHandler returns a new Group configured with a signal handler, an
// associated Context derived from ctx, and a stop channel.
func WithSignalHandler(ctx context.Context) (*Group, context.Context, chan struct{}) {
	stop := make(chan struct{})
	ctx, cancel := context.WithCancel(ctx)
	return &Group{
		cancel:       cancel,
		stop:         stop,
		catchSignals: true,
	}, ctx, stop
}

// WithContext returns a new Group and an associated Context derived from ctx.
//
// The derived Context is canceled the first time a function passed to Go
// returns a non-nil error or the first time Wait returns, whichever occurs
// first.
func WithContext(ctx context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{cancel: cancel}, ctx
}

// Finally configures the Group with a callback of sorts that returns an error
// that propogates to the Wait method.
func (g *Group) Finally(fn func() error) {
	g.finally = fn
}

// Wait blocks until all function calls from the Go method have returned, then
// returns the first non-nil error (if any) from them.
//
// If SIGINT, SIGKILL, or SIGTERM is caught, run finally, cancel the context,
// and close the stop channel.
func (g *Group) Wait() error {
	if g.catchSignals {
		c := make(chan os.Signal, 2)
		signal.Notify(c, os.Interrupt, os.Kill, syscall.SIGTERM)

		go func() {
			<-c

			g.runFinally()

			if g.cancel != nil {
				g.cancel()
			}

			g.closeStop()

			<-c
			os.Exit(0)
		}()
	}

	g.wg.Wait()

	g.runFinally()

	if g.cancel != nil {
		g.cancel()
	}

	g.closeStop()

	return g.err
}

// Go calls the given function in a new goroutine.
//
// The first call to return a non-nil error cancels the group; its error will be
// returned by Wait.
func (g *Group) Go(f func() error) {
	g.wg.Add(1)

	go func() {
		defer g.wg.Done()

		if err := f(); err != nil {
			g.errOnce.Do(func() {
				g.err = err
				if g.cancel != nil {
					g.cancel()
				}
			})
		}
	}()
}

func (g *Group) closeStop() {
	g.stopOnce.Do(func() {
		if g.stop != nil {
			close(g.stop)
		}
	})
}

func (g *Group) runFinally() {
	g.finallyOnce.Do(func() {
		if g.finally == nil {
			g.finally = func() error { return nil }
		}

		if err := g.finally(); err != nil {
			if g.err == nil {
				g.err = err
			} else {
				g.err = fmt.Errorf("%s: %w", g.err, err) // not sure if I should do this
			}
		}
	})
}
