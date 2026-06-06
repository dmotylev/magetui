package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

// collector is a test sink. Engine serializes emission, so a plain slice
// behind a mutex records global event order faithfully.
type collector struct {
	mu  sync.Mutex
	evs []events.Event
}

func (c *collector) sink(ev events.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evs = append(c.evs, ev)
}

func (c *collector) events() []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]events.Event(nil), c.evs...)
}

func newTestEngine() (*Engine, *collector) {
	c := &collector{}
	return New(c.sink), c
}

func mustFn(t *testing.T, v any) Fn {
	t.Helper()
	fn, err := Normalize(v)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return fn
}

func TestDeps_EachTargetRunsOnceDespiteTenImpatientDependents(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	var brews atomic.Int32
	brewCoffee := mustFn(t, func(context.Context) error {
		brews.Add(1)
		time.Sleep(20 * time.Millisecond) // long enough for everyone to queue up
		return nil
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := e.RunDeps(context.Background(), root, brewCoffee); err != nil {
				t.Errorf("RunDeps: %v", err)
			}
		})
	}
	wg.Wait()

	if got := brews.Load(); got != 1 {
		t.Fatalf("coffee brewed %d times, want exactly 1", got)
	}
}

func TestDeps_SiblingsRunInParallel(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	grinderOn := make(chan struct{})
	kettleOn := make(chan struct{})
	rendezvous := func(mine, theirs chan struct{}) error {
		close(mine)
		select {
		case <-theirs:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("waited at the rendezvous alone")
		}
	}
	grind := mustFn(t, func(context.Context) error { return rendezvous(grinderOn, kettleOn) })
	boil := mustFn(t, func(context.Context) error { return rendezvous(kettleOn, grinderOn) })

	if err := e.RunDeps(context.Background(), root, grind, boil); err != nil {
		t.Fatalf("deps did not overlap: %v", err)
	}
}

func TestDeps_SiblingsRunToCompletionWhenOneFails(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	var slowFinished atomic.Bool
	failFast := mustFn(t, func(context.Context) error {
		return errors.New("burnt the toast immediately")
	})
	slowAndSteady := mustFn(t, func(context.Context) error {
		time.Sleep(30 * time.Millisecond)
		slowFinished.Store(true)
		return nil
	})

	err := e.RunDeps(context.Background(), root, failFast, slowAndSteady)
	if err == nil {
		t.Fatal("want an error from the toast incident")
	}
	if !slowFinished.Load() {
		t.Fatal("slow sibling was cancelled; mg.Deps contract says it runs to completion")
	}
}

func TestDeps_ErrorsAggregateInArgumentOrder(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	errEspresso := errors.New("espresso machine on fire")
	errMilk := errors.New("milk expired in 2019")
	pull := mustFn(t, func(context.Context) error { return errEspresso })
	steam := mustFn(t, func(context.Context) error { return errMilk })
	fine := mustFn(t, func(context.Context) error { return nil })

	err := e.RunDeps(context.Background(), root, pull, fine, steam)
	if !errors.Is(err, errEspresso) || !errors.Is(err, errMilk) {
		t.Fatalf("aggregate %v should contain both failures", err)
	}
	msg := err.Error()
	if strings.Index(msg, "espresso") > strings.Index(msg, "milk") {
		t.Fatalf("errors out of argument order: %q", msg)
	}
}

func TestDeps_DedupedFailureReportsToEveryDependent(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	errCursed := errors.New("works on my machine")
	var runs atomic.Int32
	cursed := mustFn(t, func(context.Context) error {
		runs.Add(1)
		return errCursed
	})

	err1 := e.RunDeps(context.Background(), root, cursed)
	err2 := e.RunDeps(context.Background(), root, cursed)
	if !errors.Is(err1, errCursed) || !errors.Is(err2, errCursed) {
		t.Fatalf("both dependents must see the original error; got %v / %v", err1, err2)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("failed dep re-ran %d times; failure is also memoized", got)
	}
}

func TestDeps_PanicIsCapturedWithStack(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")

	dropTable := mustFn(t, func(context.Context) error {
		panic("the intern had prod access")
	})

	err := e.RunDeps(context.Background(), root, dropTable)
	if err == nil || !strings.Contains(err.Error(), "the intern had prod access") {
		t.Fatalf("panic should surface as an error, got: %v", err)
	}

	fin := finishedOf(t, c, "dropTable")
	if fin.Outcome != events.OutcomePanicked {
		t.Fatalf("outcome = %v, want OutcomePanicked", fin.Outcome)
	}
	if fin.PanicValue != "the intern had prod access" {
		t.Fatalf("PanicValue = %v", fin.PanicValue)
	}
	if len(fin.Stack) == 0 {
		t.Fatal("stack trace missing; the postmortem will be vague")
	}
	if len(e.Failed()) != 1 {
		t.Fatalf("Failed() = %d steps, want 1", len(e.Failed()))
	}
}

func TestDeps_FailPanicClassifiesAsFailureNotPanic(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")

	errInner := errors.New("dependency had a bad day")
	giveUp := mustFn(t, func(context.Context) error {
		FailPanic(errInner)
		return nil
	})

	err := e.RunDeps(context.Background(), root, giveUp)
	if !errors.Is(err, errInner) {
		t.Fatalf("FailPanic error lost: %v", err)
	}
	fin := finishedOf(t, c, "giveUp")
	if fin.Outcome != events.OutcomeFailed {
		t.Fatalf("outcome = %v, want OutcomeFailed: FailPanic is control flow, not a bug", fin.Outcome)
	}
	if fin.Stack != nil {
		t.Fatal("FailPanic must not be presented with a stack trace")
	}
}

func TestEvents_StartedPrecedesFinishedAndParentsAreRight(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("all", "")

	unit := mustFn(t, func(context.Context) error { return nil })
	if err := e.RunDeps(context.Background(), root, unit); err != nil {
		t.Fatal(err)
	}

	var started events.StepStarted
	startIdx, finishIdx := -1, -1
	for i, ev := range c.events() {
		switch v := ev.(type) {
		case events.StepStarted:
			if v.Name == "unit" || strings.Contains(v.Name, "func") {
				started, startIdx = v, i
			}
		case events.StepFinished:
			if startIdx >= 0 && v.ID == started.ID {
				finishIdx = i
			}
		}
	}
	if startIdx < 0 || finishIdx < 0 || finishIdx < startIdx {
		t.Fatalf("event order broken: started@%d finished@%d", startIdx, finishIdx)
	}
	if started.Parent != root.ID() {
		t.Fatalf("dep's parent = %d, want root %d", started.Parent, root.ID())
	}
}

func TestRunStep_AdHocStepsAreNotDeduped(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	var runs atomic.Int32
	fn := func(context.Context) error { runs.Add(1); return nil }
	ctx := context.Background()
	if err := e.RunStep(ctx, root, "overthink", "", fn); err != nil {
		t.Fatal(err)
	}
	if err := e.RunStep(ctx, root, "overthink", "", fn); err != nil {
		t.Fatal(err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("ad-hoc step ran %d times, want 2: Step is not Deps", got)
	}
}

func TestStepFrom_CarriesTheStepIntoTheDep(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	var inside *Step
	dep := mustFn(t, func(ctx context.Context) error {
		inside = StepFrom(ctx)
		return nil
	})
	if err := e.RunDeps(context.Background(), root, dep); err != nil {
		t.Fatal(err)
	}
	if inside == nil {
		t.Fatal("StepFrom returned nil inside a dep")
	}
	if inside.ID() == root.ID() {
		t.Fatal("dep saw the root step, not its own")
	}
	if StepFrom(context.Background()) != nil {
		t.Fatal("StepFrom on a bare context must be nil, not inventive")
	}
}

func TestStep_OutputIsBufferedAndEmitted(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")

	root.Output(events.Stdout, "brewing")
	root.Output(events.Stderr, "kettle whistling ominously")
	root.SetStatus("3/4 cups")

	head, elided, tail := root.Lines()
	if len(head) != 2 || elided != 0 || len(tail) != 0 {
		t.Fatalf("Lines() = %d head, %d elided, %d tail", len(head), elided, len(tail))
	}
	if head[1].Origin != events.Stderr {
		t.Fatal("stderr origin lost in the buffer")
	}

	var outs, statuses int
	for _, ev := range c.events() {
		switch ev.(type) {
		case events.OutputLine:
			outs++
		case events.StatusChanged:
			statuses++
		}
	}
	if outs != 2 || statuses != 1 {
		t.Fatalf("emitted %d OutputLine + %d StatusChanged, want 2 + 1", outs, statuses)
	}
}

// finishedOf returns the StepFinished event of the step named name.
func finishedOf(t *testing.T, c *collector, name string) events.StepFinished {
	t.Helper()
	byID := map[events.StepID]string{}
	for _, ev := range c.events() {
		if s, ok := ev.(events.StepStarted); ok {
			byID[s.ID] = s.Name
		}
	}
	for _, ev := range c.events() {
		if f, ok := ev.(events.StepFinished); ok {
			if strings.Contains(byID[f.ID], "func") || byID[f.ID] == name {
				return f
			}
		}
	}
	t.Fatalf("no StepFinished for %q", name)
	return events.StepFinished{}
}
