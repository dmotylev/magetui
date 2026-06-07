package render

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dmotylev/magetui/internal/events"
)

// Tree never reads a clock: every probe happens at a scripted instant
// relative to this arbitrary epoch.
var treeT0 = time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return treeT0.Add(d) }

func assertFrame(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("frame diverges:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The §4.1 mid-run scene: a build subtree with a finished sibling and a
// chatty running leaf, a test subtree mid-flight. Then compile fails,
// build's block commits, test's block commits, the root line ends the
// run — the whole commit choreography in one timeline.
func TestTree_MidRunFrameThenRootChildCommits(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "build"}, at(500*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 3, Parent: 2, Name: "codegen"}, at(500*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeOK, Duration: 1100 * time.Millisecond}, at(1600*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 4, Parent: 2, Name: "compile"}, at(3500*time.Millisecond))
	tr.Handle(events.OutputLine{ID: 4, Origin: events.Command, Text: "go build ./pkg/..."}, at(3600*time.Millisecond))
	tr.Handle(events.OutputLine{ID: 4, Origin: events.Stdout, Text: "linking magetui"}, at(9000*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 5, Parent: 1, Name: "test"}, at(6300*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 6, Parent: 5, Name: "unit"}, at(6300*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 7, Parent: 5, Name: "lint"}, at(6300*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 6, Outcome: events.OutcomeOK, Duration: 3400 * time.Millisecond}, at(9700*time.Millisecond))
	tr.Handle(events.OutputLine{ID: 7, Origin: events.Stdout, Text: "golangci-lint run"}, at(9800*time.Millisecond))

	// Root omitted (it has children, no status); root children at depth 0;
	// durations share one right-aligned column; tails under running leaves.
	assertFrame(t, tr.Frame(80, 24, at(11500*time.Millisecond)), strings.Join([]string{
		"⠋ build      11.0s",
		"  ✓ codegen   1.1s",
		"  ⠋ compile   8.0s",
		"    $ go build ./pkg/...",
		"    │ linking magetui",
		"⠹ test        5.2s",
		"  ✓ unit      3.4s",
		"  ⠹ lint      5.2s",
		"    │ golangci-lint run",
	}, "\n"))

	if blocks := tr.TakeBlocks(); len(blocks) != 0 {
		t.Fatalf("nothing has closed yet, but blocks committed: %q", blocks)
	}

	// compile fails; codegen finished long ago but the build subtree only
	// closes — and commits, atomically, as one block — when build itself
	// finishes.
	tr.Handle(events.StepFinished{ID: 4, Outcome: events.OutcomeFailed, Err: errors.New("exit status 2"), Duration: 9800 * time.Millisecond}, at(13300*time.Millisecond))
	if blocks := tr.TakeBlocks(); len(blocks) != 0 {
		t.Fatalf("compile alone must not commit the build subtree: %q", blocks)
	}
	tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeFailed, Err: errors.New("compile: exit status 2"), Duration: 12900 * time.Millisecond}, at(13400*time.Millisecond))

	wantBlock := strings.Join([]string{
		"✗ build      12.9s — compile: exit status 2",
		"  ✓ codegen   1.1s",
		"  ✗ compile   9.8s — exit status 2",
	}, "\n")
	blocks := tr.TakeBlocks()
	if len(blocks) != 1 || blocks[0] != wantBlock {
		t.Errorf("build block diverges:\n--- got ---\n%q\n--- want ---\n%q", blocks, wantBlock)
	}
	if again := tr.TakeBlocks(); len(again) != 0 {
		t.Fatalf("TakeBlocks must drain: %q", again)
	}

	// The committed subtree left the live region; only test remains.
	assertFrame(t, tr.Frame(80, 24, at(13500*time.Millisecond)), strings.Join([]string{
		"⠹ test    7.2s",
		"  ✓ unit  3.4s",
		"  ⠹ lint  7.2s",
		"    │ golangci-lint run",
	}, "\n"))

	tr.Handle(events.StepFinished{ID: 7, Outcome: events.OutcomeOK, Duration: 7700 * time.Millisecond}, at(14000*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 5, Outcome: events.OutcomeOK, Duration: 7700 * time.Millisecond}, at(14000*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("build: compile: exit status 2"), Duration: 14 * time.Second}, at(14000*time.Millisecond))

	// Blocks in completion order: the test subtree, then the bare root
	// line — the period at the end of the run.
	want := []string{
		strings.Join([]string{
			"✓ test    7.7s",
			"  ✓ unit  3.4s",
			"  ✓ lint  7.7s",
		}, "\n"),
		"✗ all  14.0s — build: compile: exit status 2",
	}
	blocks = tr.TakeBlocks()
	if len(blocks) != 2 || blocks[0] != want[0] || blocks[1] != want[1] {
		t.Errorf("final blocks diverge:\n--- got ---\n%q\n--- want ---\n%q", blocks, want)
	}

	// After the root commits there is nothing live left to draw.
	assertFrame(t, tr.Frame(80, 24, at(14100*time.Millisecond)), "")
}

// The root line shows only before its first child or while it carries
// status text; status rides after the elapsed, is replaced by the next
// StatusChanged, and drops at finish.
func TestTree_RootLineAndStatusLifecycle(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "deploy"}, at(0))

	// Pre-first-child: something must spin.
	assertFrame(t, tr.Frame(80, 24, at(400*time.Millisecond)), "⠼ deploy  0.4s")

	tr.Handle(events.StatusChanged{ID: 1, Text: "connecting"}, at(400*time.Millisecond))
	assertFrame(t, tr.Frame(80, 24, at(400*time.Millisecond)), "⠼ deploy  0.4s  connecting")

	// A child arrives but the root still carries status: both render, the
	// child at depth 0 regardless.
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "push"}, at(1000*time.Millisecond))
	assertFrame(t, tr.Frame(80, 24, at(1200*time.Millisecond)), strings.Join([]string{
		"⠹ deploy  1.2s  connecting",
		"⠹ push    0.2s",
	}, "\n"))

	// Status cleared: the root line says nothing again and disappears.
	tr.Handle(events.StatusChanged{ID: 1, Text: ""}, at(1200*time.Millisecond))
	assertFrame(t, tr.Frame(80, 24, at(1200*time.Millisecond)), "⠹ push  0.2s")

	tr.Handle(events.StatusChanged{ID: 2, Text: "uploading 12MB"}, at(4500*time.Millisecond))
	assertFrame(t, tr.Frame(80, 24, at(4600*time.Millisecond)), "⠦ push  3.6s  uploading 12MB")

	tr.Handle(events.StatusChanged{ID: 2, Text: "verifying"}, at(4650*time.Millisecond))
	assertFrame(t, tr.Frame(80, 24, at(4600*time.Millisecond)), "⠦ push  3.6s  verifying")

	// Finish drops the status; push is a one-step root-child subtree, so
	// it commits immediately — and the childless root line returns.
	tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: 3700 * time.Millisecond}, at(4700*time.Millisecond))
	blocks := tr.TakeBlocks()
	if len(blocks) != 1 || blocks[0] != "✓ push  3.7s" {
		t.Errorf("push block diverges: %q", blocks)
	}
	assertFrame(t, tr.Frame(80, 24, at(4700*time.Millisecond)), "⠧ deploy  4.7s")
}

// Icons ride the step through the live line and the committed block —
// decoration between glyph and name, never an alignment anchor beyond
// their own runes.
func TestTree_IconsRideTheStep(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "build", Icon: "🔨"}, at(0))
	assertFrame(t, tr.Frame(80, 24, at(1*time.Second)), "⠋ 🔨 build  1.0s")

	tr.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: 2 * time.Second}, at(2*time.Second))
	if blocks := tr.TakeBlocks(); len(blocks) != 1 || blocks[0] != "✓ 🔨 build  2.0s" {
		t.Errorf("icon lost on commit: %q", blocks)
	}
}

// The ladder: tiers 5 → 3 → 1 → 0, first fit wins; tails always show the
// most recent lines.
func TestTree_DegradationLadderTiers(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "chatty"}, at(100*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "noisy"}, at(200*time.Millisecond))
	for i := 1; i <= 6; i++ {
		tr.Handle(events.OutputLine{ID: 2, Origin: events.Stdout, Text: fmt.Sprintf("chatty line %d", i)}, at(300*time.Millisecond))
		tr.Handle(events.OutputLine{ID: 3, Origin: events.Stderr, Text: fmt.Sprintf("noisy line %d", i)}, at(300*time.Millisecond))
	}
	probe := at(1000 * time.Millisecond)

	steps := func(tails ...string) string {
		rows := []string{"⠏ chatty  0.9s"}
		for _, l := range tails {
			rows = append(rows, "  │ "+l)
		}
		rows = append(rows, "⠇ noisy   0.8s")
		for _, l := range tails {
			rows = append(rows, "  ┃ "+strings.Replace(l, "chatty", "noisy", 1))
		}
		return strings.Join(rows, "\n")
	}

	// Tier 5 — but only the last 5 lines are retained at all.
	assertFrame(t, tr.Frame(80, 24, probe),
		steps("chatty line 2", "chatty line 3", "chatty line 4", "chatty line 5", "chatty line 6"))
	// 2 step rows + 2×3 tails = 8 ≤ 10.
	assertFrame(t, tr.Frame(80, 11, probe),
		steps("chatty line 4", "chatty line 5", "chatty line 6"))
	// 2 + 2×1 = 4 ≤ 4.
	assertFrame(t, tr.Frame(80, 5, probe), steps("chatty line 6"))
	// Tier 0: bare step rows.
	assertFrame(t, tr.Frame(80, 4, probe), steps())
	// Even bare rows overflow: everything cut behind one marker row.
	assertFrame(t, tr.Frame(80, 2, probe), "… +2 more")
}

// Row cuts at tier 0: completed-waiting go first (youngest first), then
// running shortest-first — the longest-running survive to the last row.
func TestTree_CutOrderKeepsTheLongestRunning(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "alpha"}, at(100*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "beta"}, at(200*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 4, Parent: 1, Name: "gamma"}, at(300*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 5, Parent: 4, Name: "delta"}, at(350*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 5, Outcome: events.OutcomeOK, Duration: 100 * time.Millisecond}, at(450*time.Millisecond))

	// Four rows into three: completed-waiting delta is cut first, then
	// gamma (the youngest running); alpha and beta outlast them.
	assertFrame(t, tr.Frame(80, 4, at(1000*time.Millisecond)), strings.Join([]string{
		"⠏ alpha  0.9s",
		"⠇ beta   0.8s",
		"… +2 more",
	}, "\n"))
}

// Live lines hard-truncate to width by rune count with a trailing
// ellipsis — multibyte text must not split.
func TestTree_TruncationIsRuneCorrect(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "naïve"}, at(100*time.Millisecond))
	tr.Handle(events.OutputLine{ID: 2, Origin: events.Stdout, Text: "ünïcödé everywhere all at once"}, at(200*time.Millisecond))

	assertFrame(t, tr.Frame(16, 24, at(1000*time.Millisecond)), strings.Join([]string{
		"⠏ naïve  0.9s",
		"  │ ünïcödé eve…",
	}, "\n"))
}

// Interrupted steps carry their cause like any other bad outcome. soak
// sits below a root child: a finished root child would commit straight
// to scrollback, but a deeper step waits in the live region — visibly.
func TestTree_InterruptedStepsSayWhy(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "group"}, at(0))
	tr.Handle(events.StepStarted{ID: 3, Parent: 2, Name: "soak"}, at(0))
	tr.Handle(events.StepStarted{ID: 4, Parent: 2, Name: "wait"}, at(0))
	tr.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeInterrupted, Duration: 2 * time.Second}, at(2*time.Second))

	assertFrame(t, tr.Frame(80, 24, at(2*time.Second)), strings.Join([]string{
		"⠋ group   2.0s",
		"  ⊘ soak  2.0s — interrupted",
		"  ⠋ wait  2.0s",
	}, "\n"))
}

// Zero-size frames render empty, not garbage; so does the time before
// the root exists. Height 1 leaves no room under the reserved row.
func TestTree_ZeroSizeFramesRenderEmpty(t *testing.T) {
	tr := NewTree()
	assertFrame(t, tr.Frame(80, 24, at(0)), "")

	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	for _, dim := range [][2]int{{0, 24}, {-3, 24}, {80, 0}, {80, 1}, {80, -1}} {
		assertFrame(t, tr.Frame(dim[0], dim[1], at(time.Second)), "")
	}
}

// Resize is just another frame: shrink tightens the ladder, growth
// relaxes it, and nothing is lost in between — only unwindowed.
func TestTree_ResizeRoundTripsLosslessly(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "chatty"}, at(100*time.Millisecond))
	for i := 1; i <= 5; i++ {
		tr.Handle(events.OutputLine{ID: 2, Origin: events.Stdout, Text: fmt.Sprintf("a rather long line of output number %d", i)}, at(200*time.Millisecond))
	}
	probe := at(time.Second)

	wide := tr.Frame(80, 24, probe)
	if narrow := tr.Frame(20, 3, probe); narrow == wide {
		t.Fatal("shrinking changed nothing; the ladder never engaged")
	}
	if again := tr.Frame(80, 24, probe); again != wide {
		t.Errorf("resize round-trip lost content:\n--- before ---\n%s\n--- after ---\n%s", wide, again)
	}
}

// The two promises repaint arithmetic rests on: rows ≤ height and
// runewidth ≤ width, at every size, for a busy tree.
func TestTree_InvariantSweep(t *testing.T) {
	tr := NewTree()
	tr.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "all"}, at(0))
	tr.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "build", Icon: "🔨"}, at(100*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 3, Parent: 2, Name: "codegen"}, at(150*time.Millisecond))
	tr.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeOK, Duration: time.Second}, at(1150*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 4, Parent: 2, Name: "compile-with-a-very-long-step-name"}, at(200*time.Millisecond))
	tr.Handle(events.StepStarted{ID: 5, Parent: 1, Name: "test"}, at(300*time.Millisecond))
	tr.Handle(events.StatusChanged{ID: 5, Text: "running 1,234 of 5,678 cases with great determination"}, at(400*time.Millisecond))
	for i := range 7 {
		tr.Handle(events.OutputLine{ID: 4, Origin: events.Stdout, Text: strings.Repeat("very wide output ", i+1)}, at(500*time.Millisecond))
		tr.Handle(events.OutputLine{ID: 5, Origin: events.Stderr, Text: "ünïcödé wärnïng — something is slightly off"}, at(500*time.Millisecond))
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
