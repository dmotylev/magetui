package render

import (
	"context"
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
	p := NewPlain(&out, unstyled(ThemeColor))
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
	p := NewPlain(&out, unstyled(ThemeColor))
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

func TestPlain_CloseFlushesHeldStartedLines(t *testing.T) {
	p, out := feed(events.StepStarted{ID: 1, Parent: 0, Name: "procrastinate"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if got := out.String(); got != "○ procrastinate  started\n" {
		t.Fatalf("held line must not outlive Close — the replay prints next:\n%q", got)
	}
}

func TestPlainOver_StreamsStragglersAtTheTreesWidth(t *testing.T) {
	// The TUI's ^C degradation: the tree saw the whole run so far; the
	// takeover plain renderer prints nothing for it, but resolves the
	// stragglers' paths and opens the name column at the width the run
	// had already reached.
	tree := NewTree(unstyled(ThemeColor))
	at := time.Unix(0, 0)
	tree.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"}, at)
	tree.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "test"}, at)
	tree.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "procrastinate"}, at)

	var out strings.Builder
	p := NewPlainOver(&out, tree)
	if out.Len() != 0 {
		t.Fatalf("takeover printed on boot: %q", out.String())
	}
	p.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeInterrupted, Err: context.Canceled, Duration: 1500 * time.Millisecond})
	p.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK, Duration: 2000 * time.Millisecond})
	p.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeInterrupted, Err: context.Canceled, Duration: 2100 * time.Millisecond})

	want := `⊘ procrastinate  1.5s  interrupted
✓ test           2.0s
⊘ ci             2.1s  interrupted
`
	if got := out.String(); got != want {
		t.Errorf("frames diverge:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}
