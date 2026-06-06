package engine

// Differential tests: the same scenario run through the real mg.CtxDeps and
// through our engine, asserting matching observables (run counts, completion
// flags, failure surfaced or not, error text containment). Never message
// shapes — those legitimately differ.
//
// Purpose: the contract tests in engine_test.go pin our *statement* of the
// mg.Deps contract; these pin it against mage itself, and document the one
// place we diverge on purpose. mage is imported in test files only — the
// library proper never imports it (CLAUDE.md).
//
// mage caveat that shapes these tests: mg's once-map is package-global and
// keyed by function *name*, so every scenario uses closures local to its own
// test function to avoid cross-test contamination.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/magefile/mage/mg"
)

// mage's once-map is process-global, so each differential scenario can run
// only once per test binary: under -count=N the second iteration would
// observe deps that "already ran". Skip repeats rather than report ghosts.
var differentialSeen sync.Map

func onceOnly(t *testing.T) {
	t.Helper()
	if _, repeated := differentialSeen.LoadOrStore(t.Name(), true); repeated {
		t.Skip("mage's once-map is process-global; differential scenarios are single-shot per binary")
	}
}

// mgDeps runs mg.CtxDeps and converts its panic-on-failure convention into
// a return value comparable with our engine's.
func mgDeps(ctx context.Context, fns ...any) (msg string, failed bool) {
	defer func() {
		if r := recover(); r != nil {
			msg, failed = fmt.Sprint(r), true
		}
	}()
	mg.CtxDeps(ctx, fns...)
	return "", false
}

func TestDifferential_EachDepRunsOnceAcrossCalls(t *testing.T) {
	onceOnly(t)
	ctx := context.Background()

	var mgRuns atomic.Int32
	mgDep := func() { mgRuns.Add(1); time.Sleep(10 * time.Millisecond) }
	if _, failed := mgDeps(ctx, mgDep); failed {
		t.Fatal("mage side failed unexpectedly")
	}
	if _, failed := mgDeps(ctx, mgDep); failed {
		t.Fatal("mage side failed on second call")
	}

	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	var ourRuns atomic.Int32
	ourDep := mustFn(t, func() { ourRuns.Add(1); time.Sleep(10 * time.Millisecond) })
	if err := e.RunDeps(ctx, root, ourDep); err != nil {
		t.Fatal(err)
	}
	if err := e.RunDeps(ctx, root, ourDep); err != nil {
		t.Fatal(err)
	}

	if mgRuns.Load() != 1 || ourRuns.Load() != 1 {
		t.Fatalf("run-once divergence: mage ran %d, engine ran %d, both should be 1",
			mgRuns.Load(), ourRuns.Load())
	}
}

func TestDifferential_SiblingsRunToCompletionOnFailure(t *testing.T) {
	onceOnly(t)
	ctx := context.Background()

	var mgSlowDone atomic.Bool
	mgFail := func() error { return errors.New("instant failure") }
	mgSlow := func() { time.Sleep(30 * time.Millisecond); mgSlowDone.Store(true) }
	_, mgFailed := mgDeps(ctx, mgFail, mgSlow)

	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	var ourSlowDone atomic.Bool
	ourFail := mustFn(t, func() error { return errors.New("instant failure") })
	ourSlow := mustFn(t, func() { time.Sleep(30 * time.Millisecond); ourSlowDone.Store(true) })
	ourErr := e.RunDeps(ctx, root, ourFail, ourSlow)

	if !mgFailed || ourErr == nil {
		t.Fatalf("failure surfacing divergence: mage failed=%v, engine err=%v", mgFailed, ourErr)
	}
	if mgSlowDone.Load() != ourSlowDone.Load() {
		t.Fatalf("completion divergence: mage slow sibling done=%v, ours=%v",
			mgSlowDone.Load(), ourSlowDone.Load())
	}
	if !mgSlowDone.Load() {
		t.Fatal("both cancelled the slow sibling; the contract itself was misread")
	}
}

func TestDifferential_SiblingsOverlapInTime(t *testing.T) {
	onceOnly(t)
	ctx := context.Background()

	rendezvous := func(mine, theirs chan struct{}) error {
		close(mine)
		select {
		case <-theirs:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("waited alone")
		}
	}

	mgA, mgB := make(chan struct{}), make(chan struct{})
	mgLeft := func() error { return rendezvous(mgA, mgB) }
	mgRight := func() error { return rendezvous(mgB, mgA) }
	if msg, failed := mgDeps(ctx, mgLeft, mgRight); failed {
		t.Fatalf("mage did not run siblings in parallel: %s", msg)
	}

	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	ourA, ourB := make(chan struct{}), make(chan struct{})
	ourLeft := mustFn(t, func() error { return rendezvous(ourA, ourB) })
	ourRight := mustFn(t, func() error { return rendezvous(ourB, ourA) })
	if err := e.RunDeps(ctx, root, ourLeft, ourRight); err != nil {
		t.Fatalf("engine did not run siblings in parallel: %v", err)
	}
}

func TestDifferential_FailureIsMemoizedNotRetried(t *testing.T) {
	onceOnly(t)
	ctx := context.Background()

	var mgRuns atomic.Int32
	mgCursed := func() error { mgRuns.Add(1); return errors.New("works on my machine") }
	_, failed1 := mgDeps(ctx, mgCursed)
	msg2, failed2 := mgDeps(ctx, mgCursed)
	if !failed1 || !failed2 {
		t.Fatal("mage forgave a failed dep on retry")
	}
	if !strings.Contains(msg2, "works on my machine") {
		t.Fatalf("mage's second failure lost the original error: %q", msg2)
	}

	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	var ourRuns atomic.Int32
	ourCursed := mustFn(t, func() error { ourRuns.Add(1); return errors.New("works on my machine") })
	err1 := e.RunDeps(ctx, root, ourCursed)
	err2 := e.RunDeps(ctx, root, ourCursed)
	if err1 == nil || err2 == nil {
		t.Fatal("engine forgave a failed dep on retry")
	}
	if !strings.Contains(err2.Error(), "works on my machine") {
		t.Fatalf("engine's second failure lost the original error: %v", err2)
	}

	if mgRuns.Load() != 1 || ourRuns.Load() != 1 {
		t.Fatalf("memoization divergence: mage ran %d, engine ran %d, both should be 1",
			mgRuns.Load(), ourRuns.Load())
	}
}

func TestDifferential_DepPanicSurfacesWithItsMessage(t *testing.T) {
	onceOnly(t)
	ctx := context.Background()

	mgKraken := func() { panic("release the kraken") }
	msg, failed := mgDeps(ctx, mgKraken)
	if !failed || !strings.Contains(msg, "release the kraken") {
		t.Fatalf("mage lost the panic: failed=%v msg=%q", failed, msg)
	}

	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	ourKraken := mustFn(t, func() { panic("release the kraken") })
	err := e.RunDeps(ctx, root, ourKraken)
	if err == nil || !strings.Contains(err.Error(), "release the kraken") {
		t.Fatalf("engine lost the panic: %v", err)
	}
}

func TestDifferential_ClosureInstancesAreDistinctSteps(t *testing.T) {
	// Two distinct closures created from the same literal run twice on BOTH
	// sides: mage v1.17.2 keys dedup on the closure instance, and so do we
	// (Go 1.26 funcval identity).
	//
	// Historical note, preserved because it is the differential suite's
	// founding catch: this test originally asserted mage dedups same-literal
	// closures by NAME and ran them once — a confident paraphrase that the
	// first execution against real mg.CtxDeps disproved. If either side
	// moves, this fails and DESIGN.md gets a paragraph.
	onceOnly(t)
	ctx := context.Background()

	var mgRuns atomic.Int32
	mgMk := func(string) func() { return func() { mgRuns.Add(1) } }
	if _, failed := mgDeps(ctx, mgMk("alice"), mgMk("bob")); failed {
		t.Fatal("mage side failed unexpectedly")
	}
	if got := mgRuns.Load(); got != 2 {
		t.Fatalf("mage ran same-literal closures %d times, expected instance identity to run 2", got)
	}

	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	var ourRuns atomic.Int32
	ourMk := func(string) func() { return func() { ourRuns.Add(1) } }
	a := mustFn(t, ourMk("alice"))
	b := mustFn(t, ourMk("bob"))
	if err := e.RunDeps(ctx, root, a, b); err != nil {
		t.Fatal(err)
	}
	if got := ourRuns.Load(); got != 2 {
		t.Fatalf("engine ran same-literal closures %d times, expected instance identity to run 2", got)
	}
}
