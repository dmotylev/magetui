// Package events defines the typed event stream between the execution
// engine and the renderers. The execution layer emits; renderers consume.
// Nothing in this package may depend on engine or rendering code — the seam
// is the point (DESIGN.md §3.2).
package events

import "time"

// StepID identifies a step for the lifetime of a run. IDs are assigned
// monotonically from 1; zero means "no step" (e.g. the root's parent).
type StepID int64

// Origin tells where an output line came from: a subprocess stream, or
// magetui itself echoing the command it is about to run. Renderers style
// each differently (DESIGN.md §4.3, §4.4: `│` stdout, `┃` stderr, `$` for
// the command echo in the failure replay).
type Origin int8

const (
	Stdout Origin = iota
	Stderr
	Command
)

// Outcome is the terminal state of a finished step.
type Outcome int8

const (
	OutcomeOK Outcome = iota
	OutcomeFailed
	OutcomePanicked
	OutcomeInterrupted
)

// Event is implemented by all event types in this package.
type Event interface {
	isEvent()
}

// StepStarted announces a new step. Parent is zero for the root step.
type StepStarted struct {
	ID     StepID
	Parent StepID
	Name   string
	Icon   string
}

// StepFinished carries a step's terminal state.
//
// Err is set for OutcomeFailed and OutcomePanicked (for panics it is a
// synthesized error around the panic value). PanicValue and Stack are set
// only for OutcomePanicked.
type StepFinished struct {
	ID         StepID
	Outcome    Outcome
	Err        error
	PanicValue any
	Stack      []byte
	Duration   time.Duration
}

// OutputLine is one line of step output, already split on newlines.
type OutputLine struct {
	ID     StepID
	Origin Origin
	Text   string
}

// Line is one recorded line of output: the stored form of OutputLine, with
// the step implied by the buffer it sits in. The engine's buffers record
// Lines; renderers consume them in the failure replay (DESIGN.md §4.4).
type Line struct {
	Origin Origin
	Text   string
}

// StatusChanged replaces the transient status text on a step's own line
// (the buildx transfer-counter feel). It is presentation-only and is not
// recorded in output buffers.
type StatusChanged struct {
	ID   StepID
	Text string
}

func (StepStarted) isEvent()   {}
func (StepFinished) isEvent()  {}
func (OutputLine) isEvent()    {}
func (StatusChanged) isEvent() {}
