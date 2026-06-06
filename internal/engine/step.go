package engine

import (
	"context"

	"github.com/dmotylev/magetui/internal/events"
)

// Step is the engine-side state of one node in the run tree. Renderers
// never see it; they see only the events it emits.
type Step struct {
	e      *Engine
	id     events.StepID
	parent events.StepID
	name   string
	icon   string
	buf    *buffer
}

func (s *Step) ID() events.StepID { return s.id }
func (s *Step) Name() string      { return s.name }

// Engine returns the engine that owns the step. The public API uses it to
// reach RunDeps/RunStep from a context-carried step.
func (s *Step) Engine() *Engine { return s.e }

// Output records one line of output (already newline-free) in the step's
// buffer and emits it to the stream.
func (s *Step) Output(origin events.Origin, text string) {
	s.buf.append(origin, text)
	s.e.emit(events.OutputLine{ID: s.id, Origin: origin, Text: text})
}

// SetStatus replaces the step's transient status text. Presentation-only:
// not recorded in the buffer, absent from failure replay.
func (s *Step) SetStatus(text string) {
	s.e.emit(events.StatusChanged{ID: s.id, Text: text})
}

// Lines returns the step's recorded output for replay: head, count of
// elided lines, tail.
func (s *Step) Lines() (head []Line, elided int, tail []Line) {
	return s.buf.lines()
}

type ctxKey struct{}

// WithStep returns a context carrying the step. Deps and the output
// primitives use it to attribute work to the right node.
func WithStep(ctx context.Context, s *Step) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// StepFrom extracts the step carried by ctx, or nil if there is none.
// Callers must treat nil as "not inside a magetui target" and degrade
// gracefully rather than fail (DESIGN.md §2).
func StepFrom(ctx context.Context) *Step {
	s, _ := ctx.Value(ctxKey{}).(*Step)
	return s
}
