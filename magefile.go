//go:build mage

package main

import (
	"context"

	"github.com/dmotylev/magetui"
	"github.com/magefile/mage/mg"
)

// The Phase 3 dogfood checkpoint (PLAN.md): targets render through
// magetui. Public targets wrap their body in magetui.Target; the bare
// implementations stay plain functions. Nesting Target is safe since
// Phase 6 — the inner one degrades to an ordinary step — but Deps on a
// wrapped target would still render two levels (the Deps step plus the
// inner Target's), so the flat impls remain the tidier tree.

var Default = CI

// Test runs the unit tests with the race detector, bypassing the test
// cache: mage test asks about now, not about the last identical run.
// mage -v test runs them verbosely; anything else (e.g. -run, -count)
// goes through GOFLAGS, which the go tool reads natively.
func Test(ctx context.Context) error { return magetui.Target(ctx, test) }

// Vet runs go vet.
func Vet(ctx context.Context) error { return magetui.Target(ctx, vet) }

// Lint runs golangci-lint.
func Lint(ctx context.Context) error { return magetui.Target(ctx, lint) }

// Vuln runs govulncheck against the known-vulnerability database.
func Vuln(ctx context.Context) error { return magetui.Target(ctx, vuln) }

// CI runs what the CI test job runs.
func CI(ctx context.Context) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx, vet, test)
		return nil
	})
}

// Linger blocks until interrupted — the manual rig for signal teardown
// (DESIGN.md §6). Try ^C against the TUI, ^C^C, and kill -TERM — aimed
// at the magefile binary, not the mage wrapper, which forwards nothing.
// The comprehensive demo rig is Phase 7.
func Linger(ctx context.Context) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Status(ctx, "waiting for your signal")
		<-ctx.Done()
		return ctx.Err()
	})
}

func test(ctx context.Context) error {
	args := []string{"test", "-race", "-count=1"}
	if mg.Verbose() {
		args = append(args, "-v")
	}
	return magetui.Run(ctx, "go", append(args, "./...")...)
}

func vet(ctx context.Context) error {
	return magetui.Run(ctx, "go", "vet", "./...")
}

func lint(ctx context.Context) error {
	return magetui.Run(ctx, "golangci-lint", "run")
}

func vuln(ctx context.Context) error {
	return magetui.Run(ctx, "go", "tool", "govulncheck", "./...")
}
