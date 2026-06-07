//go:build !unix

package magetui

import "context"

// notifyTerm is a no-op off unix: there is no SIGTERM to watch for —
// Windows terminations don't arrive as catchable signals. Interruption
// handling still works through mage's ^C context cancellation.
func notifyTerm(parent context.Context) (ctx context.Context, stop func(), fired func() bool) {
	return parent, func() {}, func() bool { return false }
}
