//go:build mage

package main

import (
	"context"

	"github.com/dmotylev/magetui"
	"github.com/magefile/mage/mg"
)

// The Phase 3 dogfood checkpoint (PLAN.md): targets render through
// magetui. Public targets wrap their body in magetui.Target; the bare
// implementations stay plain functions so CI can Deps on them without
// nesting Target (nested Target detection lands in Phase 6).

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
