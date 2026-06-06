// Package render hosts the consumers of the typed event stream
// (DESIGN.md §3.3). Phase 3 ships the plain renderer; the TUI arrives in
// Phase 4, the OSC 9;4 emitter in Phase 6. Glyphs are hardcoded until
// themes land in Phase 5.
package render

import (
	"fmt"
	"io"
	"slices"
	"strconv"
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
	total int // steps started, excluding the root
	width int // widest path seen so far, in runes

	held  []events.StepID // started, not yet printed
	timer *time.Timer
}

// plainStep is the per-step state Plain keeps for path prefixes and the
// failure replay.
type plainStep struct {
	name     string
	parent   events.StepID
	outcome  events.Outcome
	err      error
	stack    []byte
	duration time.Duration
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
		} else {
			p.total++
		}
		if w := utf8.RuneCountInString(p.path(ev.ID, false)); w > p.width {
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
		p.printf("%s %s  %s\n", gutter(ev.Origin), p.pad(p.path(ev.ID, false)), ev.Text)
	case events.StatusChanged:
		p.printf("%s %s  %s\n", glyphStart, p.pad(p.path(ev.ID, false)), ev.Text)
	case events.StepFinished:
		s := p.steps[ev.ID]
		if s == nil {
			return
		}
		s.outcome, s.err, s.stack, s.duration = ev.Outcome, ev.Err, ev.Stack, ev.Duration
		p.printf("%s %s%s\n", glyph(ev.Outcome), p.pad(p.path(ev.ID, false)), finishSuffix(s))
	}
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
		p.printf("%s %s  started\n", glyphStart, p.pad(p.path(id, false)))
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

// Replay is the recorded output of one failed step, to be replayed in full
// at exit (DESIGN.md §4.4). Target assembles these from the engine's
// buffers; the renderer supplies path, duration, and cause from its own
// event records.
type Replay struct {
	ID     events.StepID
	Head   []events.Line
	Elided int
	Tail   []events.Line
}

// ReplayFailures prints the exit detail section: the complete captured
// output of directly-failed steps, then the failure count. Call once,
// after the run; with no failures it prints nothing. It also flushes any
// still-held started lines — Target calls it unconditionally, so nothing
// stays buffered past the run.
func (p *Plain) ReplayFailures(failed []Replay) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushLocked()
	if len(failed) == 0 {
		return
	}
	p.printf("\n")
	p.printf("%s\n", separator)
	count := 0
	for _, r := range failed {
		s := p.steps[r.ID]
		if s == nil {
			continue
		}
		if r.ID != p.root {
			count++
		}
		p.printf("%s %s%s\n", glyph(s.outcome), p.path(r.ID, true), finishSuffix(s))
		for _, ln := range r.Head {
			p.printf("  %s %s\n", gutter(ln.Origin), ln.Text)
		}
		if r.Elided > 0 {
			p.printf("  … %s lines elided …\n", comma(r.Elided))
		}
		for _, ln := range r.Tail {
			p.printf("  %s %s\n", gutter(ln.Origin), ln.Text)
		}
		// Panics get their stack as a separate block; Phase 5 styles it and
		// trims it to the magefile frame.
		if s.outcome == events.OutcomePanicked && len(s.stack) > 0 {
			p.printf("\n")
			for line := range strings.SplitSeq(strings.TrimRight(string(s.stack), "\n"), "\n") {
				p.printf("  %s\n", line)
			}
		}
		p.printf("\n")
	}
	if count > 0 {
		p.printf("%d of %d steps failed.\n", count, p.total)
	}
}

// path renders a step's ▸-separated location. Live lines omit the root —
// it is the same on every line and says nothing — while the failure replay
// keeps it, matching DESIGN.md §4.6 vs §4.4. The root step's own lines
// always show its name.
func (p *Plain) path(id events.StepID, withRoot bool) string {
	var names []string
	for cur := id; cur != 0; {
		s := p.steps[cur]
		if s == nil {
			break
		}
		if cur == p.root && cur != id && !withRoot {
			break
		}
		names = append(names, s.name)
		cur = s.parent
	}
	slices.Reverse(names)
	return strings.Join(names, pathSep)
}

// finishSuffix renders the duration and, for bad outcomes, the cause:
// "  9.8s  exit status 2".
func finishSuffix(s *plainStep) string {
	suffix := fmt.Sprintf("  %.1fs", s.duration.Seconds())
	switch s.outcome {
	case events.OutcomeFailed, events.OutcomePanicked:
		if s.err != nil {
			suffix += "  " + s.err.Error()
		}
	case events.OutcomeInterrupted:
		suffix += "  interrupted"
	case events.OutcomeOK:
	}
	return suffix
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

// comma renders n with thousands separators: the elision marker says
// "1,204 lines elided", not "1204".
func comma(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
