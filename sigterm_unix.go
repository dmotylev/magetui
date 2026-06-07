//go:build unix

package magetui

import (
	"context"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

// notifyTerm derives a context canceled on SIGTERM. Mage's mainfile
// handles SIGINT — cancel on the first, force-exit on the second — but
// ignores SIGTERM, which would otherwise kill the process with the
// terminal raw and the OSC progress state dangling (DESIGN.md §6).
//
// After the first SIGTERM the watcher restores the default disposition,
// so a second SIGTERM kills outright: the user outranks the renderer,
// the same rule as mage's second ^C. fired reports whether SIGTERM is
// what canceled the run — the 143-vs-130 distinction. stop releases the
// watcher; Target defers it.
func notifyTerm(parent context.Context) (ctx context.Context, stop func(), fired func() bool) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	var f atomic.Bool
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			f.Store(true)
			signal.Stop(ch)
			cancel()
		case <-done:
		}
	}()
	stop = func() {
		signal.Stop(ch)
		close(done)
		cancel()
	}
	return ctx, stop, f.Load
}
