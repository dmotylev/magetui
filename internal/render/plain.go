// Package render hosts the consumers of the typed event stream
// (DESIGN.md §3.3): the plain renderer, the TUI layout core and its
// bubbletea adapter, and the shared failure replay. The OSC 9;4 emitter
// arrives in Phase 6. Glyphs are hardcoded until themes land in Phase 5.
package render

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dmotylev/magetui/internal/events"
)

// Phase 5 moves these into Theme. The start glyph is the open-vs-filled
// counterpart of ✓/✗: pending, then resolved. ▸ is reserved for the path
// separator — one meaning per glyph.
const (
	glyphStart       = "○"
	glyphOK          = "✓"
	glyphFail        = "✗"
	glyphPanic       = "‼"
	glyphInterrupted = "⊘"
	gutterOut        = "|"
	gutterErr        = "!"
	gutterCmd        = "$"
	pathSep          = " ▸ "
	separator        = "──────────────────────────────────────────"
)

// startHoldWindow bounds how long a started line waits for its siblings.
// A Deps call announces every sibling within microseconds; 50ms is an
// eternity for the burst and imperceptible for a human.
const startHoldWindow = 50 * time.Millisecond

// Plain renders the event stream as columnar, append-only lines — no
// repainting, safe for CI and log collectors (DESIGN.md §4.6). The first
// column is the lifecycle glyph or, for output lines, the origin gutter;
// the second is the step path, padded to the widest path seen so far; the
// rest is unbounded. Icons are not rendered here: the grid owns the first
// column.
//
// Started lines are held until the next non-start event or the hold window
// elapses, whichever comes first, so a sibling burst — every step a Deps
// call announces — prints at one width. The flush timer runs off the
// engine's goroutines, hence the mutex.
type Plain struct {
	mu    sync.Mutex
	w     io.Writer
	steps map[events.StepID]*plainStep
	root  events.StepID
	width int // widest path seen so far, in runes

	held  []events.StepID // started, not yet printed
	timer *time.Timer
}

// plainStep is the per-step state Plain keeps for path prefixes.
type plainStep struct {
	name   string
	parent events.StepID
}

// NewPlain returns a plain renderer writing to w.
func NewPlain(w io.Writer) *Plain {
	return &Plain{w: w, steps: make(map[events.StepID]*plainStep)}
}

// printf writes one rendered line. Rendering is best-effort by design: a
// broken progress pipe must never fail a build, so write errors are
// dropped here, deliberately and in one place.
func (p *Plain) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(p.w, format, a...)
}

// Handle consumes one event. Started lines are held for the burst; any
// other event flushes them first, so order is preserved.
func (p *Plain) Handle(ev events.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ev, ok := ev.(events.StepStarted); ok {
		p.steps[ev.ID] = &plainStep{name: ev.Name, parent: ev.Parent}
		if ev.Parent == 0 && p.root == 0 {
			p.root = ev.ID
		}
		if w := utf8.RuneCountInString(p.path(ev.ID)); w > p.width {
			p.width = w
		}
		p.held = append(p.held, ev.ID)
		if p.timer == nil {
			p.timer = time.AfterFunc(startHoldWindow, p.flush)
		} else {
			p.timer.Reset(startHoldWindow)
		}
		return
	}
	p.flushLocked()
	switch ev := ev.(type) {
	case events.OutputLine:
		p.printf("%s %s  %s\n", gutter(ev.Origin), p.pad(p.path(ev.ID)), ev.Text)
	case events.StatusChanged:
		p.printf("%s %s  %s\n", glyphStart, p.pad(p.path(ev.ID)), ev.Text)
	case events.StepFinished:
		if p.steps[ev.ID] == nil {
			return
		}
		p.printf("%s %s%s\n", glyph(ev.Outcome), p.pad(p.path(ev.ID)), finishSuffix(ev.Outcome, ev.Err, ev.Duration))
	}
}

// Close flushes any still-held started lines so nothing stays buffered
// past the run — Target calls it before writing the failure replay, which
// must land below every progress line. The error return satisfies the
// renderer interface; plain has nothing that can fail.
func (p *Plain) Close() error {
	p.flush()
	return nil
}

// flush is the hold-window deadline: a started step whose build went quiet
// still announces itself promptly.
func (p *Plain) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushLocked()
}

// flushLocked prints the held started lines, in arrival order, at the
// width the burst reached. Callers hold p.mu.
func (p *Plain) flushLocked() {
	if p.timer != nil {
		p.timer.Stop()
	}
	for _, id := range p.held {
		p.printf("%s %s  started\n", glyphStart, p.pad(p.path(id)))
	}
	p.held = p.held[:0]
}

// pad right-pads path to the widest path seen so far, forming the name
// column. A streaming renderer has no lookahead: the column widens when a
// deeper step first appears, and lines already printed keep their narrower
// padding.
func (p *Plain) pad(path string) string {
	if n := p.width - utf8.RuneCountInString(path); n > 0 {
		return path + strings.Repeat(" ", n)
	}
	return path
}

// path renders a step's ▸-separated location, omitting the root — it is
// the same on every line and says nothing. The failure replay keeps it,
// matching DESIGN.md §4.6 vs §4.4; its paths come from the engine, not
// from here. The root step's own lines always show its name.
func (p *Plain) path(id events.StepID) string {
	var names []string
	for cur := id; cur != 0; {
		s := p.steps[cur]
		if s == nil {
			break
		}
		if cur == p.root && cur != id {
			break
		}
		names = append(names, s.name)
		cur = s.parent
	}
	slices.Reverse(names)
	return strings.Join(names, pathSep)
}

func glyph(o events.Outcome) string {
	switch o {
	case events.OutcomeFailed:
		return glyphFail
	case events.OutcomePanicked:
		return glyphPanic
	case events.OutcomeInterrupted:
		return glyphInterrupted
	case events.OutcomeOK:
	}
	return glyphOK
}

func gutter(o events.Origin) string {
	switch o {
	case events.Stderr:
		return gutterErr
	case events.Command:
		return gutterCmd
	case events.Stdout:
	}
	return gutterOut
}
