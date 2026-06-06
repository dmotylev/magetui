//go:build mage

package main

import (
	"context"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Plain mg/sh for now. Switches to magetui at the Phase 3 dogfood
// checkpoint (see PLAN.md).

var Default = CI

// Test runs the unit tests with the race detector.
func Test(ctx context.Context) error {
	return sh.RunV("go", "test", "-race", "./...")
}

// Vet runs go vet.
func Vet(ctx context.Context) error {
	return sh.RunV("go", "vet", "./...")
}

// Lint runs golangci-lint.
func Lint(ctx context.Context) error {
	return sh.RunV("golangci-lint", "run")
}

// Vuln runs govulncheck against the known-vulnerability database.
func Vuln(ctx context.Context) error {
	return sh.RunV("go", "tool", "govulncheck", "./...")
}

// CI runs what the CI test job runs.
func CI(ctx context.Context) error {
	mg.CtxDeps(ctx, Vet, Test)
	return nil
}
