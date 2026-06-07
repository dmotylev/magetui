package magetui

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/charmbracelet/colorprofile"
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
	out   io.Writer
	mode  ProgressMode
	theme Theme
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
//
// The one shaped error is magetui's own: when the run is interrupted —
// mage cancels the context on ^C, or magetui's SIGTERM watcher fires —
// the aggregate comes back carrying the conventional 128+signal exit
// status (130 or 143), which mage's machinery honors (DESIGN.md §6).
//
// A Target called inside another Target — a library target wrapping
// defensively, run as somebody's dependency — boots nothing: it becomes
// an ordinary step of the outer run, options ignored, error untouched.
func Target(ctx context.Context, fn func(context.Context) error, opts ...TargetOption) error {
	if s := engine.StepFrom(ctx); s != nil {
		// Nested Target (DESIGN.md §6): the outer run owns the renderer,
		// the signal watcher, and the replay; the options are silently
		// ignored because a defensive wrapper must be invisible, not
		// chatty.
		return s.Engine().RunStep(ctx, s, callerName(), "", fn)
	}
	o := targetOptions{out: os.Stdout, theme: ThemeColor}
	for _, opt := range opts {
		opt(&o)
	}
	theme := render.Theme(resolveTheme(o))
	// The profile writer downsamples the palette to what the output can
	// show and strips it entirely for pipes, CI, and NO_COLOR. Plain and
	// the replay write through it; the TUI gets the raw writer because
	// bubbletea detects the terminal's profile itself.
	pw := colorprofile.NewWriter(o.out, os.Environ())
	r := newRenderer(resolveMode(o), o.out, pw, theme)

	// SIGINT belongs to mage: its mainfile cancels ctx on the first ^C
	// and force-exits on the second — magetui only observes. SIGTERM is
	// mage's blind spot, and a process killed with the terminal raw is
	// unforgivable, so Target watches it itself (DESIGN.md §6).
	ctx, stop, sigterm := notifyTerm(ctx)
	defer stop()

	sink := r.Handle
	if on, percent := oscMode(os.Getenv, stderrIsTerminal()); on {
		osc := render.NewOSC(os.Stderr, percent)
		// Deferred the moment it exists: the clear must land on every
		// exit — success, failure, interruption, panic (DESIGN.md §4.7).
		defer func() { _ = osc.Close() }()
		h := r.Handle
		sink = func(ev events.Event) {
			osc.Handle(ev)
			h(ev)
		}
	}

	eng := engine.New(sink)
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
	render.ReplayFailures(pw, eng.StepCount(), eng.InterruptedCount(), replays, theme)

	// An interrupted run exits with the conventional 128+signal code; the
	// aggregate text still names any step that genuinely failed before the
	// signal landed. Everything else comes back untouched (DESIGN.md §6).
	if root.Outcome() == events.OutcomeInterrupted {
		code := 130 // SIGINT: mage canceled the context
		if sigterm() {
			code = 143
		}
		return &interruptError{err: err, code: code}
	}
	return err
}

// interruptError carries the interrupted run's exit code through mage's
// interface{ ExitStatus() int } convention — the same one engine's
// subprocess errors use — without magetui importing mg.
type interruptError struct {
	err  error
	code int
}

func (e *interruptError) Error() string   { return e.err.Error() }
func (e *interruptError) Unwrap() error   { return e.err }
func (e *interruptError) ExitStatus() int { return e.code }

// oscMode decides the OSC 9;4 gate and presentation (DESIGN.md §4.7): the
// person running the build outranks detection — off disables, on enables,
// percent enables with the determinate bar — then default-on (pulsing
// indeterminate) only for terminals known to render it — Ghostty, Windows
// Terminal, ConEmu — with stderr (the emitter's channel) actually on a
// terminal.
func oscMode(getenv func(string) string, tty bool) (on, percent bool) {
	switch getenv("MAGETUI_OSC_PROGRESS") {
	case "on":
		return true, false
	case "percent":
		return true, true
	case "off":
		return false, false
	}
	if !tty {
		return false, false
	}
	on = getenv("TERM_PROGRAM") == "ghostty" || getenv("WT_SESSION") != "" || getenv("ConEmuANSI") == "ON"
	return on, false
}

// stderrIsTerminal reports whether the OSC channel reaches a real
// terminal. Stderr, not the render output: progress sequences must work
// exactly when the build's output is piped but the window is still a
// terminal — and must stay out of CI logs.
func stderrIsTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// renderer is what Target needs from a progress front-end: events in,
// orderly teardown out. The failure replay is deliberately not here — it
// is one shared function fed engine facts (DESIGN.md §3.3).
type renderer interface {
	Handle(events.Event)
	Close() error
}

// newRenderer picks the renderer for the resolved mode: the TUI on the
// raw writer, plain behind the colorprofile writer pw.
func newRenderer(mode ProgressMode, w, pw io.Writer, theme render.Theme) renderer {
	if mode == ProgressTTY {
		return render.NewTUI(w, theme)
	}
	return render.NewPlain(pw, theme)
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

// resolveTheme applies the same precedence as resolveMode: MAGETUI_THEME
// over code over the ThemeColor default. Unrecognized values are
// ignored.
func resolveTheme(o targetOptions) Theme {
	switch os.Getenv("MAGETUI_THEME") {
	case "color":
		return ThemeColor
	case "greyscale":
		return ThemeGreyscale
	case "mono":
		return ThemeMono
	case "ascii":
		return ThemeASCII
	}
	return o.theme
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
