package magetui

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"golang.org/x/term"

	"github.com/dmotylev/magetui/internal/engine"
	"github.com/dmotylev/magetui/internal/events"
	"github.com/dmotylev/magetui/internal/render"
)

// ProgressMode selects the renderer family. ProgressAuto picks TTY when
// the output is a terminal and Plain otherwise (DESIGN.md §2). The
// MAGETUI_PROGRESS environment variable (auto|tty|plain) overrides
// whatever the code asked for; unrecognized values are ignored.
type ProgressMode int

const (
	ProgressAuto ProgressMode = iota
	ProgressTTY
	ProgressPlain
)

// TargetOption configures one Target lifecycle.
type TargetOption func(*targetOptions)

type targetOptions struct {
	out  io.Writer
	mode ProgressMode
}

// WithOutput redirects rendering away from os.Stdout. Auto mode treats a
// writer that is not a terminal file as not a TTY.
func WithOutput(w io.Writer) TargetOption {
	return func(o *targetOptions) { o.out = w }
}

// WithProgressMode selects the renderer explicitly instead of
// auto-detecting. MAGETUI_PROGRESS still wins: the person running the
// build outranks the person who wrote it.
func WithProgressMode(m ProgressMode) TargetOption {
	return func(o *targetOptions) { o.mode = m }
}

// Target is the lifecycle boundary: it boots the renderer, establishes the
// root step — named after the calling target — in the context, runs fn,
// and on exit replays the captured output of failed steps (DESIGN.md §2).
// The error comes back untouched, so mage's exit-code machinery (including
// mg.Fatal and subprocess exit statuses) behaves exactly as without
// magetui. Targets that don't wrap stay plain mage targets.
func Target(ctx context.Context, fn func(context.Context) error, opts ...TargetOption) error {
	o := targetOptions{out: os.Stdout}
	for _, opt := range opts {
		opt(&o)
	}
	r := newRenderer(resolveMode(o), o.out)

	eng := engine.New(r.Handle)
	root := eng.NewRoot(callerName(), "")
	err := eng.RunRoot(ctx, root, fn)

	// Shutdown ordering (DESIGN.md §3.3): close the renderer first — the
	// TUI commits its final blocks and restores the terminal — then write
	// the replay as plain prose below the vanished live region. A broken
	// renderer never eats the build's diagnosis.
	if cerr := r.Close(); cerr != nil {
		fmt.Fprintf(os.Stderr, "magetui: renderer: %v\n", cerr)
	}
	failed := eng.Failed()
	replays := make([]render.Replay, 0, len(failed))
	for _, s := range failed {
		head, elided, tail := s.Lines()
		replays = append(replays, render.Replay{
			Path:     s.Path(),
			Outcome:  s.Outcome(),
			Err:      s.Err(),
			Stack:    s.Stack(),
			Duration: s.Duration(),
			Head:     head,
			Elided:   elided,
			Tail:     tail,
		})
	}
	render.ReplayFailures(o.out, eng.StepCount(), replays)
	return err
}

// renderer is what Target needs from a progress front-end: events in,
// orderly teardown out. The failure replay is deliberately not here — it
// is one shared function fed engine facts (DESIGN.md §3.3).
type renderer interface {
	Handle(events.Event)
	Close() error
}

// newRenderer picks the renderer for the resolved mode.
func newRenderer(mode ProgressMode, w io.Writer) renderer {
	if mode == ProgressTTY {
		return render.NewTUI(w)
	}
	return render.NewPlain(w)
}

// resolveMode applies the precedence: MAGETUI_PROGRESS over code over
// auto-detection.
func resolveMode(o targetOptions) ProgressMode {
	mode := o.mode
	switch os.Getenv("MAGETUI_PROGRESS") {
	case "auto":
		mode = ProgressAuto
	case "tty":
		mode = ProgressTTY
	case "plain":
		mode = ProgressPlain
	}
	if mode != ProgressAuto {
		return mode
	}
	if f, ok := o.out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return ProgressTTY
	}
	return ProgressPlain
}

// callerName names the root step after the magefile target that called
// Target. Two frames up: callerName, Target, the target itself.
func callerName() string {
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		return "target"
	}
	return engine.FuncName(pc)
}
