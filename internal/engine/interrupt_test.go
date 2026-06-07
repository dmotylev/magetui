package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dmotylev/magetui/internal/events"
)

// finishOf digs the StepFinished event for id out of the recorded stream.
func finishOf(t *testing.T, c *collector, id events.StepID) events.StepFinished {
	t.Helper()
	for _, ev := range c.events() {
		if f, ok := ev.(events.StepFinished); ok && f.ID == id {
			return f
		}
	}
	t.Fatalf("step %d never finished", id)
	return events.StepFinished{}
}

func TestInterrupt_CanceledStepIsInterruptedNotFailed(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")
	ctx, cancel := context.WithCancel(context.Background())

	heatDeathWatch := mustFn(t, func(ctx context.Context) error {
		cancel() // the ^C lands mid-step
		<-ctx.Done()
		return ctx.Err()
	})

	err := e.RunRoot(ctx, root, func(ctx context.Context) error {
		// FailPanic, like the public Deps: the root is a casualty of its
		// dependency, not a direct failure.
		if err := e.RunDeps(ctx, StepFrom(ctx), heatDeathWatch); err != nil {
			FailPanic(err)
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled in the chain", err)
	}
	if got := finishOf(t, c, 2).Outcome; got != events.OutcomeInterrupted {
		t.Errorf("step outcome = %v, want OutcomeInterrupted", got)
	}
	if got := finishOf(t, c, 1).Outcome; got != events.OutcomeInterrupted {
		t.Errorf("root outcome = %v, want OutcomeInterrupted", got)
	}
	if failed := e.Failed(); len(failed) != 0 {
		t.Errorf("Failed() lists %d steps, want none — the user is not a bug", len(failed))
	}
	if got := e.InterruptedCount(); got != 1 {
		t.Errorf("InterruptedCount() = %d, want 1 (the root is not counted)", got)
	}
}

func TestInterrupt_GenuineFailureDuringShutdownStaysFailed(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")
	ctx, cancel := context.WithCancel(context.Background())

	obedient := mustFn(t, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	defiant := mustFn(t, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return errors.New("printer on fire, unrelated to your ^C")
	})

	err := e.RunRoot(ctx, root, func(ctx context.Context) error {
		if err := e.RunDeps(ctx, StepFrom(ctx), obedient, defiant); err != nil {
			FailPanic(err)
		}
		return nil
	})
	if err == nil {
		t.Fatal("err = nil, want the aggregate")
	}
	// Step IDs are assigned by racing goroutines; identify the finishes by
	// their errors instead.
	for _, ev := range c.events() {
		f, ok := ev.(events.StepFinished)
		if !ok || f.ID == 1 {
			continue
		}
		switch {
		case errors.Is(f.Err, context.Canceled) && f.Outcome != events.OutcomeInterrupted:
			t.Errorf("obedient outcome = %v, want OutcomeInterrupted", f.Outcome)
		case !errors.Is(f.Err, context.Canceled) && f.Outcome != events.OutcomeFailed:
			t.Errorf("defiant outcome = %v, want OutcomeFailed — its error was its own", f.Outcome)
		}
	}
	failed := e.Failed()
	if len(failed) != 1 || !strings.Contains(failed[0].Err().Error(), "printer on fire") {
		t.Errorf("Failed() lists %d steps, want exactly the defiant one", len(failed))
	}
	// The run as a whole was interrupted: the root aggregate carries the
	// cancellation alongside the real failure (DESIGN.md §6).
	if got := finishOf(t, c, 1).Outcome; got != events.OutcomeInterrupted {
		t.Errorf("root outcome = %v, want OutcomeInterrupted", got)
	}
}

func TestInterrupt_StepsOwnCancellationIsJustAFailure(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")

	moody := mustFn(t, func(context.Context) error {
		// A step canceling itself while the root context is alive is a
		// bug in the step, not an interruption.
		return context.Canceled
	})

	err := e.RunRoot(context.Background(), root, func(ctx context.Context) error {
		if err := e.RunDeps(ctx, StepFrom(ctx), moody); err != nil {
			FailPanic(err)
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the step's own context.Canceled", err)
	}
	if got := finishOf(t, c, 2).Outcome; got != events.OutcomeFailed {
		t.Errorf("step outcome = %v, want OutcomeFailed", got)
	}
	if failed := e.Failed(); len(failed) != 1 {
		t.Errorf("Failed() lists %d steps, want 1", len(failed))
	}
	if got := e.InterruptedCount(); got != 0 {
		t.Errorf("InterruptedCount() = %d, want 0", got)
	}
}
