package magetui_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmotylev/magetui"
	"github.com/dmotylev/magetui/internal/engine"
	"github.com/dmotylev/magetui/internal/events"
	"github.com/magefile/mage/mg"
)

// Compile-checked interop (the structural Runnable contract): magetui's
// decorated deps are mage's, and vice versa. Runtime coverage below.
var (
	_ mg.Fn              = magetui.F(shipIt)
	_ magetui.Runnable   = mg.F(shipIt)
	_ magetui.StepOption = magetui.Icon("🚢")
)

func shipIt(context.Context) error { return nil }

// inTarget runs fn inside a Target and returns the rendered output.
func inTarget(t *testing.T, fn func(ctx context.Context) error) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := magetui.Target(context.Background(), fn, magetui.WithOutput(&out))
	return out.String(), err
}

func TestDeps_DedupsAcrossTheWholeTree(t *testing.T) {
	var pours atomic.Int32
	foundation := func(context.Context) error {
		pours.Add(1)
		time.Sleep(10 * time.Millisecond) // let both dependents queue up
		return nil
	}
	walls := func(ctx context.Context) error { magetui.Deps(ctx, foundation); return nil }
	roof := func(ctx context.Context) error { magetui.Deps(ctx, foundation); return nil }

	_, err := inTarget(t, func(ctx context.Context) error {
		magetui.Deps(ctx, walls, roof)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := pours.Load(); got != 1 {
		t.Fatalf("foundation poured %d times; the house has %d foundations too many", got, got-1)
	}
}

func TestDeps_FailuresAggregateAndSiblingsFinish(t *testing.T) {
	errToast := errors.New("burnt the toast")
	errJuice := errors.New("squeezed a lime by mistake")
	var slowDone atomic.Bool
	toast := func(context.Context) error { return errToast }
	juice := func(context.Context) error { return errJuice }
	eggs := func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		slowDone.Store(true)
		return nil
	}

	_, err := inTarget(t, func(ctx context.Context) error {
		magetui.Deps(ctx, toast, eggs, juice)
		return nil
	})
	if !errors.Is(err, errToast) || !errors.Is(err, errJuice) {
		t.Fatalf("aggregate lost a failure: %v", err)
	}
	if !slowDone.Load() {
		t.Fatal("slow sibling cancelled; the mg.Deps contract says it finishes")
	}
}

func TestDeps_InvalidTypeIsAMagefileBugAndRendersAsPanic(t *testing.T) {
	out, err := inTarget(t, func(ctx context.Context) error {
		magetui.Deps(ctx, 42)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "not a valid dep type") {
		t.Fatalf("Deps accepted an int with ambitions: %v", err)
	}
	if !strings.Contains(out, "‼") {
		t.Errorf("magefile bugs wear the panic glyph:\n%s", out)
	}
}

// Icons are not rendered in plain mode (the grid owns the first column),
// so decoration is asserted at the seam: it must ride the event stream for
// the renderers that do draw it.
func TestF_IconRidesTheEventStream(t *testing.T) {
	var icons []string
	e := engine.New(func(ev events.Event) {
		if s, ok := ev.(events.StepStarted); ok && s.Icon != "" {
			icons = append(icons, s.Icon)
		}
	})
	root := e.NewRoot("root", "")
	err := e.RunRoot(context.Background(), root, func(ctx context.Context) error {
		brew := func(context.Context) error { return nil }
		magetui.Deps(ctx, magetui.F(brew, magetui.Icon("☕")))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != 1 || icons[0] != "☕" {
		t.Fatalf("icon fell off the step: %q", icons)
	}
}

func TestF_DecoratedAndBareAreStillOneStep(t *testing.T) {
	var runs atomic.Int32
	lint := func(context.Context) error { runs.Add(1); return nil }

	_, err := inTarget(t, func(ctx context.Context) error {
		magetui.Deps(ctx, magetui.F(lint, magetui.Icon("🔍")), lint)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("decoration changed identity: %d runs, want 1", got)
	}
}

func TestF_PanicsOnCreativeSignatures(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("F accepted a string; standards have slipped")
		}
	}()
	magetui.F("just a string with ambitions")
}

// chore is a foreign Runnable — structurally an mg.Fn, not a magetui.Dep.
type chore struct {
	id   string
	runs *atomic.Int32
}

func (c chore) Name() string                  { return "laundry" }
func (c chore) ID() string                    { return c.id }
func (c chore) Run(ctx context.Context) error { c.runs.Add(1); return nil }

func TestDeps_AcceptsForeignRunnablesAndDedupsOnID(t *testing.T) {
	var runs atomic.Int32
	out, err := inTarget(t, func(ctx context.Context) error {
		magetui.Deps(ctx, chore{id: "chore:laundry", runs: &runs})
		magetui.Deps(ctx, chore{id: "chore:laundry", runs: &runs})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("same ID ran %d times, want 1", got)
	}
	if !strings.Contains(out, "laundry") {
		t.Errorf("Runnable's name not rendered:\n%s", out)
	}
}

func TestDeps_RunsMgFValues(t *testing.T) {
	_, err := inTarget(t, func(ctx context.Context) error {
		magetui.Deps(ctx, mg.F(shipIt))
		return nil
	})
	if err != nil {
		t.Fatalf("mg.F dep should run under magetui: %v", err)
	}
}

func TestStep_AdHocStepsRunEveryTimeAndReportToTheCaller(t *testing.T) {
	errRug := errors.New("swept under the rug")
	var runs atomic.Int32
	out, err := inTarget(t, func(ctx context.Context) error {
		sweep := func(context.Context) error { runs.Add(1); return errRug }
		if err := magetui.Step(ctx, "sweep", sweep); !errors.Is(err, errRug) {
			t.Errorf("Step swallowed the error: %v", err)
		}
		if err := magetui.Step(ctx, "sweep", sweep, magetui.Icon("🧹")); !errors.Is(err, errRug) {
			t.Errorf("Step swallowed the error: %v", err)
		}
		return nil // the caller decides; this caller forgives
	})
	if err != nil {
		t.Fatalf("forgiven steps must not fail the target: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("ad-hoc step ran %d times, want 2: Step is not Deps", got)
	}
	if !strings.Contains(out, "✗ sweep") {
		t.Errorf("failed step not rendered as failed:\n%s", out)
	}
}

func TestPrintf_SplitsMultilineText(t *testing.T) {
	out, err := inTarget(t, func(ctx context.Context) error {
		magetui.Printf(ctx, "first the good news\nthere is no bad news\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "| inTarget  first the good news\n") || !strings.Contains(out, "| inTarget  there is no bad news\n") {
		t.Errorf("multi-line Printf mangled:\n%s", out)
	}
}

func TestStatus_RendersATransientLine(t *testing.T) {
	out, err := inTarget(t, func(ctx context.Context) error {
		magetui.Status(ctx, "3/4 cups")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "○ inTarget  3/4 cups\n") {
		t.Errorf("status text missing:\n%s", out)
	}
}

func TestDeps_OutsideATargetStillHonorsTheContract(t *testing.T) {
	var runs atomic.Int32
	stretch := func(context.Context) error { runs.Add(1); return nil }

	magetui.Deps(context.Background(), stretch)
	magetui.Deps(context.Background(), stretch) // dedup persists across calls
	if got := runs.Load(); got != 1 {
		t.Fatalf("fallback ran the dep %d times, want 1", got)
	}

	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok || !strings.Contains(err.Error(), "espresso machine on fire") {
			t.Fatalf("fallback failure should panic like mg.Deps does, got %v", r)
		}
	}()
	magetui.Deps(context.Background(), func(context.Context) error {
		return errors.New("espresso machine on fire")
	})
}

func TestStep_OutsideATargetJustRunsTheFunction(t *testing.T) {
	ran := false
	err := magetui.Step(context.Background(), "improvise", func(context.Context) error {
		ran = true
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("degraded Step must still run: ran=%v err=%v", ran, err)
	}
}
