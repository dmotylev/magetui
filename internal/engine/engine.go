// Package engine owns goroutines and the step registry, and reproduces the
// mg.Deps contract: parallel execution, once-per-target dedup, all siblings
// run to completion, errors aggregated. It emits typed events and never
// touches rendering (DESIGN.md §3.1).
package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

// DefaultBufferBudget is the per-step output buffer budget in bytes.
const DefaultBufferBudget = 1 << 20

// Engine runs steps and maintains the registry for one Target lifecycle.
type Engine struct {
	// BufferBudget is the per-step output budget in bytes. Set before the
	// first step runs; DefaultBufferBudget if zero.
	BufferBudget int

	sink func(events.Event)

	mu      sync.Mutex
	nextID  events.StepID
	once    map[any]*call
	steps   []*Step
	byID    map[events.StepID]*Step
	failed  []*Step
	rootCtx context.Context
}

// call is one once-per-target execution slot, shared by every dependent
// that requests the same function.
type call struct {
	done chan struct{}
	step *Step
	err  error
}

// New returns an Engine emitting to sink. A nil sink discards events.
// Event emission is serialized; renderers may assume single-threaded
// delivery.
func New(sink func(events.Event)) *Engine {
	if sink == nil {
		sink = func(events.Event) {}
	}
	return &Engine{sink: sink, once: make(map[any]*call), byID: make(map[events.StepID]*Step)}
}

func (e *Engine) emit(ev events.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sink(ev)
}

func (e *Engine) newStep(name, icon string, parent events.StepID) *Step {
	budget := e.BufferBudget
	if budget <= 0 {
		budget = DefaultBufferBudget
	}
	e.mu.Lock()
	e.nextID++
	s := &Step{e: e, id: e.nextID, parent: parent, name: name, icon: icon, buf: newBuffer(budget)}
	e.steps = append(e.steps, s)
	e.byID[s.id] = s
	e.mu.Unlock()
	e.emit(events.StepStarted{ID: s.id, Parent: parent, Name: name, Icon: icon})
	return s
}

// NewRoot creates and announces the root step of a Target.
func (e *Engine) NewRoot(name, icon string) *Step {
	return e.newStep(name, icon, 0)
}

// RunRoot executes fn as the root step s, with the same terminal-state
// classification as any other step. Non-root steps are run by
// RunDeps/RunStep; the root belongs to the Target lifecycle. The context
// is remembered as the run's root context: its cancellation is what makes
// a context.Canceled error count as an interruption (DESIGN.md §6).
func (e *Engine) RunRoot(ctx context.Context, s *Step, fn func(context.Context) error) error {
	e.mu.Lock()
	e.rootCtx = ctx
	e.mu.Unlock()
	return e.run(ctx, s, fn)
}

// rootCanceled reports whether the run's root context is canceled — the
// fact distinguishing "the build was interrupted" from "a step's own
// cancellation machinery fired".
func (e *Engine) rootCanceled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rootCtx != nil && e.rootCtx.Err() != nil
}

// Failed returns the steps that failed directly — returned their own error,
// or panicked — in finish order. Steps that failed only because a
// dependency did (the FailPanic sentinel) are excluded: their failure is
// fully explained by a descendant's entry. Target's failure replay reads
// this (DESIGN.md §4.4 replays the culprit, not its ancestors).
func (e *Engine) Failed() []*Step {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*Step(nil), e.failed...)
}

// StepCount returns the number of steps started so far, excluding the
// root — the denominator of the replay's "N of M steps failed" line.
func (e *Engine) StepCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return max(0, len(e.steps)-1)
}

// InterruptedCount returns the number of non-root steps that finished
// interrupted — the replay's "K of M steps did not finish" line. Their
// entries are deliberately absent from Failed: the cause was the user,
// there is nothing to diagnose (DESIGN.md §6).
func (e *Engine) InterruptedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, s := range e.steps {
		if s.parent != 0 && s.outcome == events.OutcomeInterrupted {
			n++
		}
	}
	return n
}

// failure carries an error through panic without being mistaken for a
// programmer panic: recover classifies it as OutcomeFailed, not
// OutcomePanicked. The public Deps uses it to propagate dependency
// failures the way mg.Deps does.
type failure struct{ err error }

// Error makes an escaped sentinel — Deps called outside any Target, so no
// step recovers it — display as its cause when mage's runtime prints it.
func (f failure) Error() string { return f.err.Error() }

// FailPanic panics with err marked as an ordinary failure.
func FailPanic(err error) {
	panic(failure{err})
}

// RunDeps runs fns in parallel under parent, each exactly once per Engine
// regardless of how many dependents request it. All fns run to completion
// even when some fail. The returned error joins one error per failed fn,
// in argument order; a previously-failed fn reports its original error to
// every dependent.
func (e *Engine) RunDeps(ctx context.Context, parent *Step, fns ...Fn) error {
	calls := make([]*call, len(fns))
	for i, fn := range fns {
		calls[i] = e.getOrRun(ctx, parent, fn)
	}
	var errs []error
	for i, c := range calls {
		<-c.done
		if c.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", fns[i].Name, c.err))
		}
	}
	return errors.Join(errs...)
}

// RunStep runs an ad-hoc named sub-step under parent, undeduped. This backs
// the public Step primitive.
func (e *Engine) RunStep(ctx context.Context, parent *Step, name, icon string, fn func(context.Context) error) error {
	parentID := events.StepID(0)
	if parent != nil {
		parentID = parent.id
	}
	s := e.newStep(name, icon, parentID)
	return e.run(ctx, s, fn)
}

func (e *Engine) getOrRun(ctx context.Context, parent *Step, fn Fn) *call {
	e.mu.Lock()
	if c, ok := e.once[fn.Key]; ok {
		e.mu.Unlock()
		return c
	}
	c := &call{done: make(chan struct{})}
	e.once[fn.Key] = c
	e.mu.Unlock()

	parentID := events.StepID(0)
	if parent != nil {
		parentID = parent.id
	}
	go func() {
		defer close(c.done)
		c.step = e.newStep(fn.Name, fn.Icon, parentID)
		c.err = e.run(ctx, c.step, fn.Call)
	}()
	return c
}

// run executes fn attributed to s, classifying the terminal state:
// ordinary error → OutcomeFailed; FailPanic → OutcomeFailed; any other
// panic → OutcomePanicked with the value and stack captured.
func (e *Engine) run(ctx context.Context, s *Step, fn func(context.Context) error) (err error) {
	started := time.Now()
	var pval any
	var stack []byte
	direct := true
	defer func() {
		if r := recover(); r != nil {
			if f, ok := r.(failure); ok {
				// A dependency failed; this step is a casualty, not a cause.
				err = f.err
				direct = false
			} else {
				pval = r
				stack = debug.Stack()
				err = fmt.Errorf("panic: %v", r)
			}
		}
		e.finishStep(s, started, err, pval, stack, direct)
	}()
	err = fn(WithStep(ctx, s))
	return
}

func (e *Engine) finishStep(s *Step, started time.Time, err error, pval any, stack []byte, direct bool) {
	outcome := events.OutcomeOK
	switch {
	case pval != nil:
		outcome = events.OutcomePanicked
	case err != nil && errors.Is(err, context.Canceled) && e.rootCanceled():
		// Interrupted, by error and not by clock (DESIGN.md §6): the step
		// reported the root context's cancellation. A genuine failure
		// landing after ^C keeps OutcomeFailed; a context.Canceled from a
		// step's own machinery while the root is live does too.
		outcome = events.OutcomeInterrupted
	case err != nil:
		outcome = events.OutcomeFailed
	}
	e.mu.Lock()
	// The terminal facts stay on the step too: Target reads them back
	// after the run to assemble the failure replay (DESIGN.md §4.4).
	s.outcome, s.err, s.stack, s.duration = outcome, err, stack, time.Since(started)
	// Interrupted steps stay out of failed: the replay diagnoses builds
	// that broke themselves, not builds the user stopped.
	if (outcome == events.OutcomeFailed || outcome == events.OutcomePanicked) && direct {
		e.failed = append(e.failed, s)
	}
	e.mu.Unlock()
	e.emit(events.StepFinished{
		ID:         s.id,
		Outcome:    outcome,
		Err:        err,
		PanicValue: pval,
		Stack:      stack,
		Duration:   s.duration,
	})
}
