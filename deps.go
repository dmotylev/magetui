package magetui

import (
	"context"
	"fmt"

	"github.com/dmotylev/magetui/internal/engine"
)

// Runnable is the structural shape of a decorated dependency — the same
// method set as mage's mg.Fn, so the two libraries' decorated deps are
// mutually acceptable: Deps runs mg.F values, mg.CtxDeps runs F values.
// Runnable deps dedup on ID.
type Runnable interface {
	Name() string
	ID() string
	Run(ctx context.Context) error
}

// Dep is a dependency decorated by F. It implements Runnable (and thereby
// mg.Fn).
type Dep struct {
	fn engine.Fn
}

// F attaches step metadata to a dependency, mirroring the mg.F idiom:
//
//	magetui.Deps(ctx,
//	    magetui.F(Build, magetui.Icon("🔨")),
//	    Lint, // bare functions still fine
//	)
//
// F panics if target is not one of the accepted function shapes — a
// malformed magefile, not a build failure. Decoration never changes
// identity: a target decorated at one callsite and bare at another is
// still one step, and the first registration's decoration wins.
func F(target any, opts ...StepOption) Dep {
	fn, err := engine.Normalize(target)
	if err != nil {
		panic(err)
	}
	o := applyStepOptions(opts)
	fn.Icon = o.icon
	return Dep{fn: fn}
}

// Name returns the step's display name, derived from the function name.
func (d Dep) Name() string { return d.fn.Name }

// ID identifies the underlying function instance: stable for a top-level
// function across F calls, distinct for distinct closure instances — the
// same identity Deps dedups on.
func (d Dep) ID() string { return fmt.Sprintf("%s#%v", d.fn.Name, d.fn.Key) }

// Run executes the dependency. It exists for the Runnable/mg.Fn contract
// (mg.CtxDeps calls it); inside a magetui run, Deps schedules the
// underlying function itself.
func (d Dep) Run(ctx context.Context) error { return d.fn.Call(ctx) }

// StepOption decorates a step created by F or Step.
type StepOption func(*stepOptions)

type stepOptions struct {
	icon string
}

func applyStepOptions(opts []StepOption) stepOptions {
	var o stepOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Icon sets a decorative glyph that rides the step through all renderers.
// Decoration only — never identity or alignment (DESIGN.md §2).
func Icon(icon string) StepOption {
	return func(o *stepOptions) { o.icon = icon }
}

// Deps runs the dependencies with mg.Deps semantics: in parallel, each
// exactly once per Target however many dependents request it, all siblings
// run to completion when some fail, errors aggregated in argument order.
// Accepted dep types mirror mage — func(), func() error,
// func(context.Context), func(context.Context) error — plus F values and
// any Runnable. On failure Deps panics the aggregate up the step tree the
// way mg.Deps does; the enclosing step records it and Target returns it.
// An invalid dep type panics too: that is a magefile bug, and it renders
// as one.
func Deps(ctx context.Context, deps ...any) {
	fns := make([]engine.Fn, len(deps))
	for i, dep := range deps {
		fn, err := depFn(dep)
		if err != nil {
			panic(err)
		}
		fns[i] = fn
	}
	s := engine.StepFrom(ctx)
	if s == nil {
		depsFallback(ctx, fns)
		return
	}
	if err := s.Engine().RunDeps(ctx, s, fns...); err != nil {
		engine.FailPanic(err)
	}
}

// depFn normalizes one Deps argument. Dep is matched before Runnable so
// F-decorated functions keep identity-keyed dedup — F(Build) and bare
// Build are one step. Foreign Runnables dedup on their ID.
func depFn(v any) (engine.Fn, error) {
	switch d := v.(type) {
	case Dep:
		return d.fn, nil
	case Runnable:
		return engine.Fn{Name: d.Name(), Key: d.ID(), Call: d.Run}, nil
	default:
		return engine.Normalize(v)
	}
}

// fallbackEngine serves Deps calls outside any Target: semantics intact,
// rendering absent. Process-global so dedup persists across calls, like
// mage's own once-map.
var fallbackEngine = engine.New(nil)

func depsFallback(ctx context.Context, fns []engine.Fn) {
	warnNoStep("Deps")
	// Strip the step the engine injects, so primitives inside the deps also
	// degrade to the process streams instead of buffering invisibly.
	for i := range fns {
		call := fns[i].Call
		fns[i].Call = func(ctx context.Context) error {
			return call(engine.WithStep(ctx, nil))
		}
	}
	if err := fallbackEngine.RunDeps(ctx, nil, fns...); err != nil {
		engine.FailPanic(err)
	}
}

// Step runs fn as an ad-hoc named sub-step of the current step. Unlike
// Deps, steps are never deduped: every call runs. The error comes back to
// the caller — whether it fails the enclosing target is the caller's
// decision.
func Step(ctx context.Context, name string, fn func(context.Context) error, opts ...StepOption) error {
	s := engine.StepFrom(ctx)
	if s == nil {
		warnNoStep("Step")
		return fn(ctx)
	}
	o := applyStepOptions(opts)
	return s.Engine().RunStep(ctx, s, name, o.icon, fn)
}
