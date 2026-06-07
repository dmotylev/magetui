//go:build mage

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dmotylev/magetui"
)

// The DESIGN.md §7 demo rig: artificially slow, chatty, failing, and
// panicking targets. Run with `mage -d examples <target>`; Demo is what
// the README tape records, the others exercise the paths a healthy
// build never shows.

var Default = Demo

// Demo is the all-green showcase: nested deps, spinners, transient
// status, a real subprocess, and a scrolling output tail.
func Demo(ctx context.Context) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx,
			magetui.F(brew, magetui.Icon("☕")),
			magetui.F(overthink, magetui.Icon("🤔")),
			magetui.F(ship, magetui.Icon("🚀")),
		)
		return nil
	})
}

// Fail runs the tree with one failing step: siblings run to
// completion, the failed subtree commits with its cause, and the
// replay prints the step's full captured output.
func Fail(ctx context.Context) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx,
			magetui.F(brew, magetui.Icon("☕")),
			dropTable,
			magetui.F(overthink, magetui.Icon("🤔")),
		)
		return nil
	})
}

// Panic runs a tree containing a magefile bug: the panicking step
// renders distinctly from a failure, with the stack trimmed to this
// file in the replay.
func Panic(ctx context.Context) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx,
			magetui.F(brew, magetui.Icon("☕")),
			miscount,
		)
		return nil
	})
}

// Everything runs the whole menagerie at once — slow, chatty, failing,
// and panicking siblings — the manual rig for shutdown ordering,
// aggregation, and signal teardown (^C it).
func Everything(ctx context.Context) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx,
			magetui.F(brew, magetui.Icon("☕")),
			magetui.F(overthink, magetui.Icon("🤔")),
			dropTable,
			miscount,
		)
		return nil
	})
}

// brew is the slow one: nested deps and a transient status line.
func brew(ctx context.Context) error {
	magetui.Deps(ctx, grindBeans, frothMilk)
	for _, s := range []string{"blooming", "pressing", "pouring"} {
		magetui.Status(ctx, s)
		if err := nap(ctx, 800*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

func grindBeans(ctx context.Context) error {
	magetui.Status(ctx, "27g, medium-fine")
	return nap(ctx, 1200*time.Millisecond)
}

func frothMilk(ctx context.Context) error {
	return nap(ctx, 900*time.Millisecond)
}

// overthink is the chatty one: the tail window scrolls line by line.
func overthink(ctx context.Context) error {
	concerns := []string{
		"what if the build finishes too fast to enjoy",
		"renaming the variable again",
		"considering a rewrite in a language that does not exist yet",
		"asking the rubber duck for a second opinion",
		"the duck disagrees",
		"adding an abstraction layer over the abstraction layer",
		"removing both layers",
		"settling for good enough",
	}
	for _, c := range concerns {
		magetui.Printf(ctx, "%s", c)
		if err := nap(ctx, 450*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// ship depends on overthink — already in the tree, so the once-per-
// function dedup makes this edge free — then runs a real subprocess:
// the $ command echo and | stdout arrive through origin-tagged pipes.
func ship(ctx context.Context) error {
	magetui.Deps(ctx, overthink)
	magetui.Status(ctx, "boarding")
	if err := nap(ctx, 600*time.Millisecond); err != nil {
		return err
	}
	return magetui.Run(ctx, "go", "version")
}

// dropTable is the failing one: breadcrumbs in the tail, then an
// error. The replay shows everything it printed.
func dropTable(ctx context.Context) error {
	magetui.Status(ctx, "sanitizing inputs")
	if err := nap(ctx, 1100*time.Millisecond); err != nil {
		return err
	}
	magetui.Printf(ctx, "> SELECT count(*) FROM students")
	magetui.Printf(ctx, "> 42 rows")
	magetui.Printf(ctx, "> DROP TABLE students; -- hi from Bobby")
	return fmt.Errorf("table students lost; backups disagree about having existed")
}

// miscount is the magefile bug: a genuine panic, not a failure.
func miscount(ctx context.Context) error {
	if err := nap(ctx, 700*time.Millisecond); err != nil {
		return err
	}
	deps := []string{"events", "engine", "render"}
	magetui.Printf(ctx, "fourth dependency: %s", deps[3])
	return nil
}

// nap sleeps without outliving a canceled build.
func nap(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
