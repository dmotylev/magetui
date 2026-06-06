// Package magetui renders mage builds as a live, tree-shaped progress
// display in the style of docker buildx: spinners and elapsed timers on
// running steps, scrolling output tails, completed subtrees committed to
// terminal scrollback, and full output replay for failed steps only.
//
// magetui is a renderer, not a build system: Deps reproduces the mg.Deps
// contract exactly (parallel execution, once-per-target dedup, all siblings
// run to completion, errors aggregated) while observing the run. Targets opt
// in explicitly by wrapping their body in Target; unwrapped targets remain
// plain mage targets.
//
// See DESIGN.md in the repository for the full design.
package magetui
