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

	mu     sync.Mutex
	nextID events.StepID
	once   map[uintptr]*call
	steps  []*Step
	failed []*Step
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
	return &Engine{sink: sink, once: make(map[uintptr]*call)}
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
	e.mu.Unlock()
	e.emit(events.StepStarted{ID: s.id, Parent: parent, Name: name, Icon: icon})
	return s
}

// NewRoot creates and announces the root step of a Target.
func (e *Engine) NewRoot(name, icon string) *Step {
	return e.newStep(name, icon, 0)
}

// FinishRoot records the root step's terminal state. Non-root steps are
// finished by RunDeps/RunStep; the root belongs to the Target lifecycle.
func (e *Engine) FinishRoot(s *Step, started time.Time, err error) {
	e.finishStep(s, started, err, nil, nil)
}

// Failed returns the steps that finished with OutcomeFailed or
// OutcomePanicked, in finish order. Target's failure replay reads this.
func (e *Engine) Failed() []*Step {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*Step(nil), e.failed...)
}

// failure carries an error through panic without being mistaken for a
// programmer panic: recover classifies it as OutcomeFailed, not
// OutcomePanicked. The public Deps uses it to propagate dependency
// failures the way mg.Deps does.
type failure struct{ err error }

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
	defer func() {
		if r := recover(); r != nil {
			if f, ok := r.(failure); ok {
				err = f.err
			} else {
				pval = r
				stack = debug.Stack()
				err = fmt.Errorf("panic: %v", r)
			}
		}
		e.finishStep(s, started, err, pval, stack)
	}()
	err = fn(WithStep(ctx, s))
	return
}

func (e *Engine) finishStep(s *Step, started time.Time, err error, pval any, stack []byte) {
	outcome := events.OutcomeOK
	switch {
	case pval != nil:
		outcome = events.OutcomePanicked
	case err != nil:
		outcome = events.OutcomeFailed
	}
	if outcome != events.OutcomeOK {
		e.mu.Lock()
		e.failed = append(e.failed, s)
		e.mu.Unlock()
	}
	e.emit(events.StepFinished{
		ID:         s.id,
		Outcome:    outcome,
		Err:        err,
		PanicValue: pval,
		Stack:      stack,
		Duration:   time.Since(started),
	})
}
