package render

import (
	"fmt"
	"io"
	"sync"

	"github.com/dmotylev/magetui/internal/events"
)

// OSC emits OSC 9;4 terminal progress (DESIGN.md §4.7) — the third output
// channel beside the live region and scrollback, rendered as a progress
// bar by Ghostty, Windows Terminal, and ConEmu while the window is
// unfocused, which is when builds run. It is a sibling of the renderers:
// Target tees every event into it ahead of whichever renderer is active.
//
// Every emission is one complete escape sequence in a single Write. The
// kernel serializes writes to a tty, so sequences sent to stderr can never
// splice into the middle of a bubbletea frame on stdout. Sequences are
// written only when the encoded state actually changes — events arrive
// microseconds apart, and the terminal does not need the reruns.
type OSC struct {
	mu          sync.Mutex
	w           io.Writer
	percent     bool // determinate presentation; default is the pulse
	root        events.StepID
	known       int // non-root steps started; the percent's denominator
	finished    int
	determinate bool
	failed      bool
	last        string
}

// NewOSC returns an emitter writing to w — os.Stderr in production, a
// buffer in tests. By default the bar pulses indeterminate for the whole
// run: a denominator that grows as Deps discovers targets makes a filling
// bar stutter, and the pulse reads better. percent opts into the
// determinate presentation.
func NewOSC(w io.Writer, percent bool) *OSC {
	return &OSC{w: w, percent: percent}
}

// Handle folds one event into the progress state and emits the sequence
// if it changed. In percent mode the bar starts indeterminate and turns
// determinate on the first StepFinished: by the first completion the
// initial Deps burst has registered the whole first wave, so the
// denominator is honest — no timers, no tuning constant. The denominator
// may still grow as Deps discovers targets: the bar occasionally slows,
// never lies. In either mode the first real failure switches to the
// error state — red before you alt-tab — and stays there; an
// interruption does not — red is reserved for builds that broke
// themselves (DESIGN.md §6).
func (o *OSC) Handle(ev events.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch ev := ev.(type) {
	case events.StepStarted:
		if ev.Parent == 0 && o.root == 0 {
			o.root = ev.ID
		} else {
			o.known++
		}
	case events.StepFinished:
		if ev.ID == o.root {
			return // the run is over; Close clears the bar
		}
		o.finished++
		o.determinate = true
		if ev.Outcome == events.OutcomeFailed || ev.Outcome == events.OutcomePanicked {
			o.failed = true
		}
	default:
		return
	}
	o.emit()
}

// Close clears the terminal's progress state. Target defers it the moment
// the emitter exists: the clear must land on every exit — success,
// failure, interruption, panic. Idempotent and infallible by design; the
// error return satisfies the renderer's Close shape.
func (o *OSC) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.write("\x1b]9;4;0\x07")
	return nil
}

// emit writes the current state if it differs from the last one sent.
// There is no error-indeterminate state in the OSC 9;4 vocabulary, so a
// failure carries the percent even when the run pulses. Callers hold o.mu.
func (o *OSC) emit() {
	var seq string
	switch {
	case o.failed && o.known > 0:
		seq = fmt.Sprintf("\x1b]9;4;2;%d\x07", 100*o.finished/o.known)
	case o.percent && o.determinate && o.known > 0:
		seq = fmt.Sprintf("\x1b]9;4;1;%d\x07", 100*o.finished/o.known)
	default:
		seq = "\x1b]9;4;3\x07" // indeterminate
	}
	if seq == o.last {
		return
	}
	o.last = seq
	o.write(seq)
}

// write sends one complete sequence in one Write. Best-effort like every
// renderer: a terminal that hates progress reports must not fail a build.
func (o *OSC) write(seq string) {
	_, _ = io.WriteString(o.w, seq)
}
