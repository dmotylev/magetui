package magetui

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/dmotylev/magetui/internal/engine"
)

// Run executes cmd attributed to the current step: the command line and
// both output streams land in the step's tail, tagged by origin
// (DESIGN.md §4.3). stdin is the magefile process's own. $VAR references
// in cmd and args expand from the process environment — sh.Run's contract.
func Run(ctx context.Context, cmd string, args ...string) error {
	_, err := engine.Exec(ctx, stepFrom(ctx, "Run"), engine.ExecSpec{Name: cmd, Args: args})
	return err
}

// RunWith is Run with env overlaid on the process environment; $VAR
// expansion consults env first — sh.RunWith's contract.
func RunWith(ctx context.Context, env map[string]string, cmd string, args ...string) error {
	_, err := engine.Exec(ctx, stepFrom(ctx, "RunWith"), engine.ExecSpec{Env: env, Name: cmd, Args: args})
	return err
}

// Output is Run with stdout captured and returned to the caller (trailing
// newline trimmed — sh.Output's contract) instead of recorded; stderr
// still goes to the step's tail.
func Output(ctx context.Context, cmd string, args ...string) (string, error) {
	return engine.Exec(ctx, stepFrom(ctx, "Output"), engine.ExecSpec{Capture: true, Name: cmd, Args: args})
}

// stepFrom resolves the step carried by ctx. A context with no step is API
// misuse — typically a primitive called outside magetui.Target — and
// degrades to the process's own streams with a one-time warning rather
// than failing the build (DESIGN.md §2).
func stepFrom(ctx context.Context, fn string) *engine.Step {
	s := engine.StepFrom(ctx)
	if s == nil {
		warnNoStep(fn)
	}
	return s
}

// noStepWarned tracks which primitives have already warned: once per
// primitive per process, not a drumbeat per call.
var noStepWarned sync.Map

func warnNoStep(fn string) {
	if _, loaded := noStepWarned.LoadOrStore(fn, true); loaded {
		return
	}
	fmt.Fprintf(os.Stderr, "magetui: %s called with no step in context (outside magetui.Target?); falling back to the process streams\n", fn)
}
