package render

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/dmotylev/magetui/internal/events"
)

// unstyled strips a theme's palette: the geometry goldens stay
// ANSI-free — alignment and truncation happen before styling — while
// the paint tests below prove the styling separately.
func unstyled(t Theme) Theme {
	zero := lipgloss.Style{}
	t.Name, t.Duration, t.Status = zero, zero, zero
	t.TailText, t.TailErr, t.TailCmd = zero, zero, zero
	t.Path = zero
	t.SpinnerStyle, t.OKStyle, t.FailureStyle, t.PanicStyle, t.InterruptedStyle = zero, zero, zero, zero, zero
	t.StackStyle = zero
	return t
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestThemeColor_PaintsPlainOutcomes(t *testing.T) {
	var out strings.Builder
	p := NewPlain(&out, ThemeColor)
	p.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"})
	p.Handle(events.OutputLine{ID: 1, Origin: events.Stderr, Text: "uh oh"})
	p.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("exit status 2"), Duration: 9800 * time.Millisecond})
	got := out.String()

	if !strings.Contains(got, "\x1b[31m✗\x1b[m") {
		t.Errorf("failure glyph not painted red:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[2m9.8s\x1b[m") {
		t.Errorf("duration not painted faint:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[31mexit status 2\x1b[m") {
		t.Errorf("cause not painted in the failure style:\n%q", got)
	}
	// The grid's gutter column stays unpainted — `grep '^!'` must keep
	// finding stderr even when color survives the pipe.
	if !strings.Contains(got, "\n! all  \x1b[31muh oh\x1b[m\n") {
		t.Errorf("stderr gutter must stay bare with the text painted after it:\n%q", got)
	}
	// Stripping the paint must yield exactly the unstyled rendering.
	var bare strings.Builder
	p2 := NewPlain(&bare, unstyled(ThemeColor))
	p2.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"})
	p2.Handle(events.OutputLine{ID: 1, Origin: events.Stderr, Text: "uh oh"})
	p2.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("exit status 2"), Duration: 9800 * time.Millisecond})
	if sgr.ReplaceAllString(got, "") != bare.String() {
		t.Errorf("paint changed the geometry:\n--- painted, stripped ---\n%s--- unstyled ---\n%s", sgr.ReplaceAllString(got, ""), bare.String())
	}
}

func TestThemeColor_PaintsTreeRowsAndBlocks(t *testing.T) {
	tr := NewTree(ThemeColor)
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "build"}, at(0))
	tr.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "lint"}, at(0))
	tr.Handle(events.OutputLine{ID: 3, Origin: events.Stderr, Text: "warning: vibes"}, at(0))
	tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: time.Second}, at(time.Second))

	// build is a one-step root-child subtree: it commits as a painted block.
	blocks := tr.TakeBlocks()
	if len(blocks) != 1 || !strings.Contains(blocks[0], "\x1b[32m✓\x1b[m") {
		t.Errorf("committed block must keep the green OK glyph: %q", blocks)
	}
	painted := tr.Frame(80, 24, at(2*time.Second))
	if !strings.Contains(painted, "\x1b[31m┃ warning: vibes\x1b[m") {
		t.Errorf("stderr tail not painted red:\n%q", painted)
	}
	if !strings.Contains(painted, "\x1b[36m") {
		t.Errorf("spinner not painted cyan:\n%q", painted)
	}
}

// ThemeASCII's two-column markers and one-column spinner share a padded
// glyph column, so the name grid holds; icons are dropped; the dash and
// path separator stay ASCII.
func TestThemeASCII_KeepsTheGridWithMixedWidthGlyphs(t *testing.T) {
	tr := NewTree(unstyled(ThemeASCII))
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "group"}, at(0))
	tr.Handle(events.StepStarted{ID: 3, Parent: 2, Name: "build", Icon: "🔨"}, at(0))
	tr.Handle(events.StepStarted{ID: 4, Parent: 2, Name: "test"}, at(0))
	tr.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeFailed, Err: errors.New("boom"), Duration: time.Second}, at(time.Second))

	// At 2.0s the spinner frame is 2000/100 % 4 = 0 → "-", padded to the
	// two-column glyph the XX marker needs. The icon is gone.
	assertFrame(t, tr.Frame(80, 24, at(2*time.Second)), strings.Join([]string{
		"-  group    2.0s",
		"  XX build  1.0s -- boom",
		"  -  test   2.0s",
	}, "\n"))
}

func TestThemeASCII_PlainGridPadsTheGlyphColumn(t *testing.T) {
	var out strings.Builder
	p := NewPlain(&out, unstyled(ThemeASCII))
	p.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	p.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "test"})
	p.Handle(events.OutputLine{ID: 2, Origin: events.Command, Text: "go test ./..."})
	p.Handle(events.OutputLine{ID: 2, Origin: events.Stderr, Text: "FAIL: TestKettle"})
	p.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: 1100 * time.Millisecond})
	p.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "lint", Icon: "🧹"})
	p.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeInterrupted, Duration: 2 * time.Second})
	p.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("exit status 1"), Duration: 3 * time.Second})

	want := `.. ci    started
.. test  started
$  test  go test ./...
!  test  FAIL: TestKettle
OK test  1.1s
.. lint  started
-- lint  2.0s  interrupted
XX ci    3.0s  exit status 1
`
	if got := out.String(); got != want {
		t.Errorf("ascii grid diverges:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestThemeASCII_ReplaySpeaksASCII(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 2, []Replay{{
		Path:     []string{"all", "deep", "thought"},
		Outcome:  events.OutcomeFailed,
		Err:      errors.New("42"),
		Duration: 7500 * time.Millisecond,
		Head:     []events.Line{{Origin: events.Stderr, Text: "question unclear"}},
		Elided:   1204,
	}}, unstyled(ThemeASCII))

	got := out.String()
	for _, want := range []string{
		"XX all > deep > thought  7.5s  42",
		"  ! question unclear",
		"  ... 1,204 lines elided ...",
		strings.Repeat("-", 42),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, banned := range []string{"▸", "…", "─", "│", "┃"} {
		if strings.Contains(got, banned) {
			t.Errorf("non-ASCII %q leaked into the ASCII replay:\n%s", banned, got)
		}
	}
}

// The ASCII ellipsis is three columns; truncation budgets for it by rune
// count, and gives up on it when the line is narrower than the ellipsis
// itself.
func TestThemeASCII_TruncationBudgetsForTheWiderEllipsis(t *testing.T) {
	tr := NewTree(unstyled(ThemeASCII))
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "wordy"}, at(0))
	tr.Handle(events.OutputLine{ID: 2, Origin: events.Stdout, Text: "an unreasonably long line"}, at(0))

	assertFrame(t, tr.Frame(16, 24, at(900*time.Millisecond)), strings.Join([]string{
		"\\  wordy  0.9s",
		"  | an unreas...",
	}, "\n"))
	assertFrame(t, tr.Frame(2, 24, at(900*time.Millisecond)), strings.Join([]string{
		"\\ ",
		"  ",
	}, "\n"))
}

// errors.Join aggregates with newlines; every line-oriented surface
// flattens the cause, or the grid, the live region's row arithmetic,
// and lipgloss's multi-line padding all break at once.
func TestCauses_JoinedErrorsStayOnOneLine(t *testing.T) {
	joined := errors.Join(errors.New("exit status 2"), errors.New("panic: oh no"))

	var out strings.Builder
	p := NewPlain(&out, unstyled(ThemeColor))
	p.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"})
	p.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: joined, Duration: time.Second})
	if want := "✗ all  1.0s  exit status 2; panic: oh no\n"; !strings.Contains(out.String(), want) {
		t.Errorf("plain cause not flattened:\n%q", out.String())
	}

	tr := NewTree(unstyled(ThemeColor))
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "group"}, at(0))
	tr.Handle(events.StepStarted{ID: 3, Parent: 2, Name: "wait"}, at(0))
	tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeFailed, Err: joined, Duration: time.Second}, at(time.Second))
	frame := tr.Frame(80, 24, at(time.Second))
	if strings.Contains(frame, "exit status 2\n") {
		t.Errorf("live-region cause not flattened; row arithmetic is broken:\n%s", frame)
	}
	if !strings.Contains(frame, "exit status 2; panic: oh no") {
		t.Errorf("flattened cause missing from the frame:\n%s", frame)
	}
}

// A zero-value custom theme must never crash the build over cosmetics:
// the spinner falls back, everything else renders as empty glyphs.
func TestTheme_ZeroValueNeverPanics(t *testing.T) {
	tr := NewTree(Theme{})
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "step"}, at(0))
	if frame := tr.Frame(80, 24, at(time.Second)); !strings.Contains(frame, "step") {
		t.Errorf("zero theme lost the tree:\n%q", frame)
	}
	tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: time.Second}, at(time.Second))
	tr.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeOK, Duration: time.Second}, at(time.Second))
	if blocks := tr.TakeBlocks(); len(blocks) != 2 {
		t.Errorf("zero theme broke commits: %q", blocks)
	}

	var out strings.Builder
	p := NewPlain(&out, Theme{})
	p.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"})
	p.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeOK, Duration: time.Second})
	if !strings.Contains(out.String(), "started") {
		t.Errorf("zero theme lost plain output:\n%q", out.String())
	}
}

// The embedded themes keep the lifecycle glyphs at one display width per
// theme — the grid aesthetic; the glyph column pads any remaining mix
// (the ASCII spinner is the classic one-column -\|/ beside two-column
// markers). Gutters stay one column everywhere.
func TestThemes_GlyphWidthsAreUniform(t *testing.T) {
	for name, th := range map[string]Theme{
		"color": ThemeColor, "greyscale": ThemeGreyscale, "mono": ThemeMono, "ascii": ThemeASCII,
	} {
		w := utf8.RuneCountInString(th.OK)
		for _, g := range []string{th.Start, th.Fail, th.Panic, th.Interrupted} {
			if utf8.RuneCountInString(g) != w {
				t.Errorf("%s: glyph %q is not %d columns wide", name, g, w)
			}
		}
		for _, g := range []string{th.GutterOut, th.GutterErr, th.GutterCmd, th.StackGutter} {
			if utf8.RuneCountInString(g) != 1 {
				t.Errorf("%s: gutter %q is not one column wide", name, g)
			}
		}
	}
}

// The invariant sweep holds for the ASCII repertoire too — two-column
// glyphs and a three-column ellipsis must not breach the geometry.
func TestThemeASCII_InvariantSweep(t *testing.T) {
	tr := NewTree(unstyled(ThemeASCII))
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "build"}, at(100*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 3, Parent: 2, Name: "compile-with-a-very-long-step-name"}, at(200*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeFailed, Err: errors.New("a moderately verbose cause"), Duration: time.Second}, at(1200*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 4, Parent: 1, Name: "test"}, at(300*time.Millisecond))
	for i := range 7 {
		tr.Handle(events.OutputLine{ID: 4, Origin: events.Stderr, Text: strings.Repeat("wide output ", i+1)}, at(500*time.Millisecond))
	}
	probe := at(2 * time.Second)

	for width := 1; width <= 100; width += 3 {
		for height := 1; height <= 30; height++ {
			frame := tr.Frame(width, height, probe)
			if frame == "" {
				continue
			}
			lines := strings.Split(frame, "\n")
			if len(lines) > height-1 {
				t.Fatalf("%d×%d: %d rows breach the height-1 ceiling", width, height, len(lines))
			}
			for _, line := range lines {
				if n := utf8.RuneCountInString(line); n > width {
					t.Fatalf("%d×%d: line %q is %d runes wide", width, height, line, n)
				}
			}
		}
	}
}

// Styling must not move anything: a painted frame, stripped of SGR
// sequences, is byte-identical to the unstyled frame at every size.
func TestThemeColor_PaintIsGeometryNeutral(t *testing.T) {
	feed := func(th Theme) *Tree {
		tr := NewTree(th)
		tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
		tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "build"}, at(100*time.Millisecond))
		tr.Handle(events.StatusChanged{ID: 2, Text: "linking"}, at(200*time.Millisecond))
		tr.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "test"}, at(300*time.Millisecond))
		for i := range 4 {
			tr.Handle(events.OutputLine{ID: 3, Origin: events.Origin(i % 3), Text: strings.Repeat("words ", i+2)}, at(400*time.Millisecond))
		}
		tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomePanicked, Err: errors.New("panic: oh no"), Duration: time.Second}, at(1100*time.Millisecond))
		return tr
	}
	probe := at(1500 * time.Millisecond)
	for width := 4; width <= 60; width += 7 {
		for height := 2; height <= 12; height += 2 {
			painted := feed(ThemeColor).Frame(width, height, probe)
			bare := feed(unstyled(ThemeColor)).Frame(width, height, probe)
			if got := sgr.ReplaceAllString(painted, ""); got != bare {
				t.Fatalf("%d×%d: paint moved the geometry:\n--- painted, stripped ---\n%s\n--- unstyled ---\n%s", width, height, got, bare)
			}
		}
	}
}
