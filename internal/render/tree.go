package render

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/dmotylev/magetui/internal/events"
)

const (
	spinnerInterval = 100 * time.Millisecond
	treeIndent      = "  "
)

// ladder is the degradation ladder of tail tiers: the first tier where
// everything fits wins (DESIGN.md §4.2). tailKeep bounds the per-step
// line window Tree retains — the deepest tier never shows more, and the
// engine's buffers hold the rest for replay.
var ladder = [...]int{5, 3, 1, 0}

const tailKeep = 5

// Tree is the TUI layout core: the step-tree snapshot behind the live
// region (DESIGN.md §3.3). Events in via Handle; frames out via Frame,
// committed scrollback blocks out via TakeBlocks. It imports no bubbletea,
// runs no goroutines, and reads no clock — `now` is always a parameter,
// so tests replay scripted timelines. The adapter serializes calls; Tree
// itself is not safe for concurrent use.
//
// Geometry never counts ANSI: rows are built as styled spans, alignment
// and truncation work on the spans' rune counts, and the palette is
// applied only when a finished line renders to a string (DESIGN.md §4.2).
type Tree struct {
	theme  Theme
	gw     int // the glyph column: widest lifecycle glyph or spinner frame
	steps  map[events.StepID]*treeStep
	root   *treeStep
	seq    int
	blocks []string
}

// treeStep is one node of the open tree. Committed subtrees are detached
// from the root's children; their nodes stay in the map only to swallow
// stray late events.
type treeStep struct {
	id       events.StepID
	parent   *treeStep
	children []*treeStep
	name     string
	icon     string
	seq      int // start order; the cut order's notion of age
	started  time.Time
	status   string
	done     bool
	outcome  events.Outcome
	err      error
	duration time.Duration
	tail     []events.Line // last tailKeep lines only
}

// NewTree returns an empty layout core rendering in theme. An empty
// spinner falls back to the default frames — a custom theme must not be
// able to crash the build over cosmetics (DESIGN.md §2).
func NewTree(theme Theme) *Tree {
	if len(theme.Spinner) == 0 {
		theme.Spinner = ThemeColor.Spinner
	}
	return &Tree{theme: theme, gw: theme.glyphWidth(), steps: make(map[events.StepID]*treeStep)}
}

// Handle folds one event into the tree. now timestamps StepStarted — the
// reference point live elapsed and the spinner phase are computed from.
func (t *Tree) Handle(ev events.Event, now time.Time) {
	switch ev := ev.(type) {
	case events.StepStarted:
		s := &treeStep{id: ev.ID, name: ev.Name, icon: ev.Icon, seq: t.seq, started: now}
		t.seq++
		t.steps[ev.ID] = s
		if parent := t.steps[ev.Parent]; parent != nil {
			s.parent = parent
			parent.children = append(parent.children, s)
		} else if t.root == nil {
			t.root = s
		}
	case events.OutputLine:
		s := t.steps[ev.ID]
		if s == nil {
			return
		}
		s.tail = append(s.tail, events.Line{Origin: ev.Origin, Text: ev.Text})
		if len(s.tail) > tailKeep {
			s.tail = s.tail[len(s.tail)-tailKeep:]
		}
	case events.StatusChanged:
		s := t.steps[ev.ID]
		if s == nil || s.done {
			return
		}
		s.status = ev.Text
	case events.StepFinished:
		s := t.steps[ev.ID]
		if s == nil || s.done {
			return
		}
		s.done = true
		s.outcome, s.err, s.duration = ev.Outcome, ev.Err, ev.Duration
		s.status = "" // dropped at finish (DESIGN.md §4.2)
		t.commitClosed(s)
	}
}

// commitClosed commits whatever the finish of s closed: the root-child
// subtree containing s once it is fully done, or — when s is the root —
// the bare root line that ends the run (DESIGN.md §4.1). Parents finish
// after their children, so a subtree closes exactly when its root-child
// finishes; the walk up is defense against stranger orderings.
func (t *Tree) commitClosed(s *treeStep) {
	if t.root == nil {
		return
	}
	if s == t.root {
		// Every root-child subtree should already be committed; sweep any
		// stragglers so nothing silently vanishes with the live region.
		for _, c := range t.root.children {
			if closed(c) {
				t.commitSubtree(c)
			}
		}
		t.root.children = nil
		t.blocks = append(t.blocks, renderLines(t.layout([]row{t.stepRow(t.root, 0, time.Time{})})))
		return
	}
	rootChild := s
	for rootChild.parent != nil && rootChild.parent != t.root {
		rootChild = rootChild.parent
	}
	if rootChild.parent != t.root || !closed(rootChild) {
		return
	}
	t.commitSubtree(rootChild)
	t.root.children = slices.DeleteFunc(t.root.children, func(c *treeStep) bool { return c == rootChild })
}

// closed reports whether every step in the subtree is finished.
func closed(s *treeStep) bool {
	if !s.done {
		return false
	}
	for _, c := range s.children {
		if !closed(c) {
			return false
		}
	}
	return true
}

// commitSubtree renders the closed subtree as one scrollback block —
// final glyphs, exact durations, cause suffixes, step rows only (output
// lives in the failure replay). No width truncation: scrollback is never
// repainted, so the terminal's own wrapping is harmless (DESIGN.md §4.2).
func (t *Tree) commitSubtree(s *treeStep) {
	var rows []row
	var walk func(n *treeStep, depth int)
	walk = func(n *treeStep, depth int) {
		rows = append(rows, t.stepRow(n, depth, time.Time{}))
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	walk(s, 0)
	t.blocks = append(t.blocks, renderLines(t.layout(rows)))
}

// TakeBlocks drains the committed scrollback blocks, oldest first. The
// adapter turns each into one tea.Println.
func (t *Tree) TakeBlocks() []string {
	blocks := t.blocks
	t.blocks = nil
	return blocks
}

// Frame renders the live region for a width×height terminal at instant
// now. The ceiling is height-1 — one row stays reserved against
// bottom-line repaint edge cases (DESIGN.md §4.2) — and every line is
// hard-truncated to width runes. A zero-size frame renders empty, as does
// the moment after the root commits.
func (t *Tree) Frame(width, height int, now time.Time) string {
	maxRows := height - 1
	if t.root == nil || t.root.done || width <= 0 || maxRows <= 0 {
		return ""
	}

	type entry struct {
		s     *treeStep
		depth int
	}
	var entries []entry
	var walk func(s *treeStep, depth int)
	walk = func(s *treeStep, depth int) {
		entries = append(entries, entry{s, depth})
		for _, c := range s.children {
			walk(c, depth+1)
		}
	}
	// The root's own line repeats on every frame and says nothing while
	// children carry the action; it shows only before the first child
	// (something must spin) or while it carries status text. Children
	// render at depth 0 either way, matching committed-block indentation.
	if len(t.root.children) == 0 || t.root.status != "" {
		entries = append(entries, entry{t.root, 0})
	}
	for _, c := range t.root.children {
		walk(c, 0)
	}

	// Ladder: the first tail tier where everything fits wins.
	tier := -1
	for _, candidate := range ladder {
		n := len(entries)
		for _, e := range entries {
			if tailable(e.s) {
				n += min(candidate, len(e.s.tail))
			}
		}
		if n <= maxRows {
			tier = candidate
			break
		}
	}

	var rows []row
	if tier >= 0 {
		for _, e := range entries {
			rows = append(rows, t.stepRow(e.s, e.depth, now))
			if tier > 0 && tailable(e.s) {
				tail := e.s.tail[max(0, len(e.s.tail)-tier):]
				for _, ln := range tail {
					rows = append(rows, row{raw: []span{
						{text: strings.Repeat(treeIndent, e.depth+1)},
						{text: t.theme.gutter(ln.Origin) + " " + ln.Text, style: t.theme.gutterStyle(ln.Origin)},
					}})
				}
			}
		}
	} else {
		// Even bare step rows overflow: cut whole rows — completed-waiting
		// first, then running, youngest first within each, so the
		// longest-running survive to the last row. One … +N more row
		// stands in for the cut.
		keep := max(0, maxRows-1)
		victims := make([]*treeStep, 0, len(entries))
		for _, e := range entries {
			victims = append(victims, e.s)
		}
		slices.SortFunc(victims, func(a, b *treeStep) int {
			if a.done != b.done {
				if a.done {
					return -1
				}
				return 1
			}
			return b.seq - a.seq
		})
		cut := make(map[*treeStep]bool, len(entries)-keep)
		for _, s := range victims[:len(entries)-keep] {
			cut[s] = true
		}
		for _, e := range entries {
			if !cut[e.s] {
				rows = append(rows, t.stepRow(e.s, e.depth, now))
			}
		}
		more := fmt.Sprintf("%s +%d more", t.theme.Ellipsis, len(entries)-keep)
		rows = append(rows, row{raw: []span{{text: more, style: t.theme.TailText}}})
	}

	lines := t.layout(rows)
	rendered := make([]string, len(lines))
	for i, line := range lines {
		rendered[i] = renderLine(truncateSpans(line, width, t.theme.Ellipsis))
	}
	return strings.Join(rendered, "\n")
}

// tailable says whether a step gets output tail rows: running leaves only
// (DESIGN.md §4.2) — a step with visible children would interleave its
// tail into their rows.
func tailable(s *treeStep) bool {
	return !s.done && len(s.children) == 0 && len(s.tail) > 0
}

// span is one styled segment of a line. Geometry works on the text's
// rune count; the style is applied only at the final render.
type span struct {
	text  string
	style lipgloss.Style
}

// row is one live-region or block line before alignment: step rows carry
// prefix/dur/extra and participate in the duration column; raw rows
// (tails, the +N more cut marker) pass through untouched.
type row struct {
	prefix []span // indent + glyph + icon? + name
	dur    string // unstyled; the layout styles the whole column
	extra  []span // nothing | status | dash + cause
	raw    []span
}

// stepRow renders one step's own line parts. now is unused for finished
// steps — their glyph and duration are final. The glyph pads to the
// theme's glyph column so mixed-width repertoires (an ASCII OK beside a
// one-column spinner frame) keep the names aligned.
func (t *Tree) stepRow(s *treeStep, depth int, now time.Time) row {
	th := t.theme
	var g string
	var gs lipgloss.Style
	var dur string
	var extra []span
	if s.done {
		g, gs = th.glyph(s.outcome), th.glyphStyle(s.outcome)
		dur = fmt.Sprintf("%.1fs", s.duration.Seconds())
		if cause := causeOf(s); cause != "" {
			extra = []span{{text: " " + th.Dash + " "}, {text: cause, style: gs}}
		}
	} else {
		elapsed := max(now.Sub(s.started), 0)
		g, gs = th.Spinner[int(elapsed/spinnerInterval)%len(th.Spinner)], th.SpinnerStyle
		dur = fmt.Sprintf("%.1fs", elapsed.Seconds())
		if s.status != "" {
			extra = []span{{text: "  "}, {text: s.status, style: th.Status}}
		}
	}
	name := s.name
	if th.Icons && s.icon != "" {
		name = s.icon + " " + name
	}
	prefix := []span{
		{text: strings.Repeat(treeIndent, depth)},
		{text: g, style: gs},
		{text: strings.Repeat(" ", t.gw-utf8.RuneCountInString(g)) + " "},
		{text: name, style: th.Name},
	}
	return row{prefix: prefix, dur: dur, extra: extra}
}

// causeOf renders the failure cause riding a bad final glyph, flattened
// to one line — a joined multi-error must not break row arithmetic.
func causeOf(s *treeStep) string {
	switch s.outcome {
	case events.OutcomeFailed, events.OutcomePanicked:
		if s.err != nil {
			return oneLine(s.err.Error())
		}
	case events.OutcomeInterrupted:
		return "interrupted"
	case events.OutcomeOK:
	}
	return ""
}

// layout aligns step rows on a shared duration column — durations
// right-aligned two spaces past the widest prefix — and passes raw rows
// through. Alignment is per render unit (one frame, one block); widths
// are rune counts, never ANSI.
func (t *Tree) layout(rows []row) [][]span {
	maxPrefix, maxDur := 0, 0
	for _, r := range rows {
		if r.dur == "" {
			continue
		}
		maxPrefix = max(maxPrefix, spanWidth(r.prefix))
		maxDur = max(maxDur, len(r.dur)) // durations are ASCII
	}
	lines := make([][]span, 0, len(rows))
	for _, r := range rows {
		if r.dur == "" {
			lines = append(lines, r.raw)
			continue
		}
		line := slices.Clone(r.prefix)
		line = append(line, span{text: strings.Repeat(" ", maxPrefix-spanWidth(r.prefix)+2+maxDur-len(r.dur))})
		line = append(line, span{text: r.dur, style: t.theme.Duration})
		line = append(line, r.extra...)
		lines = append(lines, line)
	}
	return lines
}

// spanWidth is a line's display width in runes.
func spanWidth(spans []span) int {
	w := 0
	for _, sp := range spans {
		w += utf8.RuneCountInString(sp.text)
	}
	return w
}

// renderLine styles one finished line — the only place the palette
// touches text.
func renderLine(spans []span) string {
	var b strings.Builder
	for _, sp := range spans {
		if sp.text == "" {
			continue
		}
		b.WriteString(styled(sp.style, sp.text))
	}
	return b.String()
}

// renderLines joins a block's lines.
func renderLines(lines [][]span) string {
	rendered := make([]string, len(lines))
	for i, line := range lines {
		rendered[i] = renderLine(line)
	}
	return strings.Join(rendered, "\n")
}

// truncateSpans hard-cuts a line to width runes with a trailing ellipsis
// — live-region only; wrapped lines would break repaint row arithmetic.
// An ellipsis wider than the line's budget is skipped rather than worn.
func truncateSpans(spans []span, width int, ellipsis string) []span {
	if spanWidth(spans) <= width {
		return spans
	}
	keep := width - utf8.RuneCountInString(ellipsis)
	if keep < 0 {
		keep, ellipsis = width, ""
	}
	out := make([]span, 0, len(spans)+1)
	n := 0
	for _, sp := range spans {
		r := utf8.RuneCountInString(sp.text)
		if n+r <= keep {
			out = append(out, sp)
			n += r
			continue
		}
		out = append(out, span{text: string([]rune(sp.text)[:keep-n]), style: sp.style})
		break
	}
	if ellipsis != "" {
		out = append(out, span{text: ellipsis})
	}
	return out
}
