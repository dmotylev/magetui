package render

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

// feed runs a scripted event sequence through a fresh Plain renderer and
// returns it with its captured output (events in, frames out — the
// renderer-test contract).
func feed(evs ...events.Event) (*Plain, *strings.Builder) {
	var out strings.Builder
	p := NewPlain(&out)
	for _, ev := range evs {
		p.Handle(ev)
	}
	return p, &out
}

func TestPlain_RendersColumnarLinePerEvent(t *testing.T) {
	_, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepStarted{ID: 2, Parent: 1, Name: "brew", Icon: "☕"},
		events.OutputLine{ID: 2, Origin: events.Command, Text: "grind --fine beans"},
		events.OutputLine{ID: 2, Origin: events.Stdout, Text: "grinding"},
		events.StatusChanged{ID: 2, Text: "3/4 cups"},
		events.OutputLine{ID: 2, Origin: events.Stderr, Text: "kettle whistling ominously"},
		events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: 1100 * time.Millisecond},
		events.StepStarted{ID: 3, Parent: 1, Name: "overthink"},
		events.OutputLine{ID: 3, Origin: events.Stdout, Text: "what if tabs were the answer all along"},
		events.StepFinished{ID: 3, Outcome: events.OutcomeFailed, Err: errors.New("exit status 2"), Duration: 9800 * time.Millisecond},
		events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("overthink: exit status 2"), Duration: 12400 * time.Millisecond},
	)

	// The name column pads to the widest path seen so far: the held start
	// burst (all+brew) prints at the burst's width, then the column widens
	// when "overthink" appears. The icon stays off the grid.
	want := `○ all   started
○ brew  started
$ brew  grind --fine beans
| brew  grinding
○ brew  3/4 cups
! brew  kettle whistling ominously
✓ brew  1.1s
○ overthink  started
| overthink  what if tabs were the answer all along
✗ overthink  9.8s  exit status 2
✗ all        12.4s  overthink: exit status 2
`
	if got := out.String(); got != want {
		t.Errorf("frames diverge:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestPlain_StartBurstPrintsAtOneWidth(t *testing.T) {
	_, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "ci"},
		events.StepStarted{ID: 2, Parent: 1, Name: "test"},
		events.StepStarted{ID: 3, Parent: 1, Name: "vet"},
		events.OutputLine{ID: 2, Origin: events.Command, Text: "go test ./..."},
	)
	want := `○ ci    started
○ test  started
○ vet   started
$ test  go test ./...
`
	if got := out.String(); got != want {
		t.Errorf("burst not absorbed:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// syncBuffer is an io.Writer safe to read while the hold timer's goroutine
// may still write.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestPlain_HeldStartFlushesOnItsOwnWhenTheBuildGoesQuiet(t *testing.T) {
	var out syncBuffer
	p := NewPlain(&out)
	p.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "meditate"})

	deadline := time.Now().Add(2 * time.Second)
	for out.String() == "" {
		if time.Now().After(deadline) {
			t.Fatal("held started line never flushed; the build looks frozen")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := out.String(); got != "○ meditate  started\n" {
		t.Fatalf("flushed line = %q", got)
	}
}

func TestPlain_PathOmitsTheRootButKeepsAncestors(t *testing.T) {
	_, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepStarted{ID: 2, Parent: 1, Name: "build"},
		events.StepStarted{ID: 3, Parent: 2, Name: "compile"},
		events.OutputLine{ID: 3, Origin: events.Stdout, Text: "linking magetui"},
	)
	if !strings.Contains(out.String(), "| build ▸ compile  linking magetui") {
		t.Fatalf("output line lost its ancestor path:\n%s", out.String())
	}
	if strings.Contains(out.String(), "all ▸ build ▸ compile") {
		t.Fatalf("live lines must omit the root; it says nothing new:\n%s", out.String())
	}
}

func TestPlain_RendersInterruptedSteps(t *testing.T) {
	_, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepFinished{ID: 1, Outcome: events.OutcomeInterrupted, Duration: 2 * time.Second},
	)
	if !strings.Contains(out.String(), "⊘ all  2.0s  interrupted") {
		t.Fatalf("interruption not rendered:\n%s", out.String())
	}
}

func TestPlain_ReplaysFailuresWithFullPathAndElision(t *testing.T) {
	p, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepStarted{ID: 2, Parent: 1, Name: "brew"},
		events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: 1100 * time.Millisecond},
		events.StepStarted{ID: 3, Parent: 1, Name: "overthink"},
		events.StepFinished{ID: 3, Outcome: events.OutcomeFailed, Err: errors.New("exit status 2"), Duration: 9800 * time.Millisecond},
		events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("overthink: exit status 2"), Duration: 12400 * time.Millisecond},
	)
	out.Reset()

	p.ReplayFailures([]Replay{{
		ID: 3,
		Head: []events.Line{
			{Origin: events.Command, Text: "overthink --harder"},
			{Origin: events.Stdout, Text: "considering monads"},
		},
		Elided: 1204,
		Tail: []events.Line{
			{Origin: events.Stderr, Text: "decision paralysis"},
		},
	}})

	want := `
──────────────────────────────────────────
✗ all ▸ overthink  9.8s  exit status 2
  $ overthink --harder
  | considering monads
  … 1,204 lines elided …
  ! decision paralysis

1 of 2 steps failed.
`
	if got := out.String(); got != want {
		t.Errorf("replay diverges:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestPlain_ReplaysPanicsWithTheStackAsItsOwnBlock(t *testing.T) {
	p, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepStarted{ID: 2, Parent: 1, Name: "dropTable"},
		events.StepFinished{
			ID:         2,
			Outcome:    events.OutcomePanicked,
			Err:        errors.New("panic: the intern had prod access"),
			PanicValue: "the intern had prod access",
			Stack:      []byte("goroutine 7 [running]:\nmain.dropTable(...)\n\t/tmp/magefile.go:42\n"),
			Duration:   300 * time.Millisecond,
		},
		events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("dropTable: panic"), Duration: time.Second},
	)
	out.Reset()

	p.ReplayFailures([]Replay{{ID: 2}})

	got := out.String()
	if !strings.Contains(got, "‼ all ▸ dropTable  0.3s  panic: the intern had prod access") {
		t.Errorf("panic header missing:\n%s", got)
	}
	if !strings.Contains(got, "\n\n  goroutine 7 [running]:\n  main.dropTable(...)\n") {
		t.Errorf("stack must be its own indented block:\n%s", got)
	}
	if !strings.Contains(got, "1 of 1 steps failed.") {
		t.Errorf("summary missing:\n%s", got)
	}
}

func TestPlain_RootOnlyFailureReplaysWithoutACountLine(t *testing.T) {
	p, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepFinished{ID: 1, Outcome: events.OutcomeFailed, Err: errors.New("forgot to plug it in"), Duration: time.Second},
	)
	out.Reset()

	p.ReplayFailures([]Replay{{ID: 1, Head: []events.Line{{Origin: events.Stderr, Text: "is it on?"}}}})

	got := out.String()
	if !strings.Contains(got, "✗ all  1.0s  forgot to plug it in") {
		t.Errorf("root replay block missing:\n%s", got)
	}
	if strings.Contains(got, "steps failed") {
		t.Errorf("the root is not counted among its own steps:\n%s", got)
	}
}

func TestPlain_NoFailuresMeansNoReplaySection(t *testing.T) {
	p, out := feed(
		events.StepStarted{ID: 1, Parent: 0, Name: "all"},
		events.StepFinished{ID: 1, Outcome: events.OutcomeOK, Duration: time.Second},
	)
	out.Reset()
	p.ReplayFailures(nil)
	if out.String() != "" {
		t.Fatalf("a green build earned silence, got:\n%s", out.String())
	}
}

func TestComma_SeparatesThousands(t *testing.T) {
	for n, want := range map[int]string{7: "7", 999: "999", 1204: "1,204", 1048576: "1,048,576"} {
		if got := comma(n); got != want {
			t.Errorf("comma(%d) = %q, want %q", n, got, want)
		}
	}
}
