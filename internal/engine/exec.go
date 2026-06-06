package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

// ExecSpec describes one subprocess execution.
type ExecSpec struct {
	// Env is extra variables overlaid on the process environment. $VAR
	// references in Name and Args expand from Env first, then from the
	// process environment — sh.RunWith's contract.
	Env map[string]string

	// Capture returns stdout to the caller (trailing newline trimmed —
	// sh.Output's contract) instead of recording it in the step's buffer.
	// stderr is recorded either way.
	Capture bool

	Name string
	Args []string
}

// Exec runs one subprocess attributed to s (DESIGN.md §4.3): the expanded
// command line is recorded first with Origin Command, then stdout and
// stderr feed the step's buffer as origin-tagged lines in arrival order.
// stdin is the magefile process's own — magetui never competes for it.
//
// A nil s runs unattributed: output goes to the process's own streams (the
// misuse fallback of DESIGN.md §2); Capture still captures.
//
// A non-zero exit returns an error that unwraps to *exec.ExitError and
// implements ExitStatus() int, so mage's exit-code machinery honors it
// without magetui importing mg.
func Exec(ctx context.Context, s *Step, spec ExecSpec) (string, error) {
	expand := func(name string) string {
		if v, ok := spec.Env[name]; ok {
			return v
		}
		return os.Getenv(name)
	}
	name := os.Expand(spec.Name, expand)
	args := make([]string, len(spec.Args))
	for i, a := range spec.Args {
		args[i] = os.Expand(a, expand)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	if len(spec.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range spec.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	// On ctx cancellation the child is killed, but Wait still drains its
	// pipes — a grandchild holding them open would stall teardown forever.
	// Bound that wait; it costs nothing on the happy path.
	cmd.WaitDelay = 5 * time.Second

	var captured strings.Builder
	var writers []*lineWriter
	tail := func(origin events.Origin) *lineWriter {
		w := &lineWriter{s: s, origin: origin}
		writers = append(writers, w)
		return w
	}
	switch {
	case spec.Capture:
		cmd.Stdout = &captured
	case s != nil:
		cmd.Stdout = tail(events.Stdout)
	default:
		cmd.Stdout = os.Stdout
	}
	if s != nil {
		cmd.Stderr = tail(events.Stderr)
		s.Output(events.Command, strings.Join(append([]string{name}, args...), " "))
	} else {
		cmd.Stderr = os.Stderr
	}

	err := cmd.Run()
	for _, w := range writers {
		w.flush()
	}
	out := strings.TrimSuffix(captured.String(), "\n")
	out = strings.TrimSuffix(out, "\r")

	if xe, ok := errors.AsType[*exec.ExitError](err); ok {
		return out, &exitError{xe}
	}
	return out, err
}

// exitError carries a subprocess exit through mage's
// interface{ ExitStatus() int } convention without importing mg. Error()
// stays exec's familiar "exit status N" — the cause suffix the renderers
// print (DESIGN.md §4.4).
type exitError struct{ err *exec.ExitError }

func (e *exitError) Error() string   { return e.err.Error() }
func (e *exitError) Unwrap() error   { return e.err }
func (e *exitError) ExitStatus() int { return e.err.ExitCode() }

// maxLineBytes caps partial-line accumulation in lineWriter: a stream that
// never emits '\n' is split as if it had, so memory stays bounded before
// the step buffer's own budget can apply.
const maxLineBytes = 64 << 10

// lineWriter adapts a subprocess stream to the step's line-oriented
// buffer: splits on '\n' and trims a trailing '\r' (CRLF children exist on
// every platform). os/exec writes each stream from a single goroutine, so
// no locking is needed here; Step.Output serializes the merge, preserving
// arrival order across the two streams.
type lineWriter struct {
	s      *Step
	origin events.Origin
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	for len(w.buf) >= maxLineBytes {
		w.line(w.buf[:maxLineBytes])
		w.buf = w.buf[maxLineBytes:]
	}
	return len(p), nil
}

func (w *lineWriter) line(b []byte) {
	b = bytes.TrimSuffix(b, []byte{'\r'})
	w.s.Output(w.origin, string(b))
}

// flush records any trailing output that did not end in a newline.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.line(w.buf)
		w.buf = nil
	}
}
