# Contributing

How changes happen here. DESIGN.md is the source of truth for *what*
magetui is; this file is the source of truth for *how it changes*.

## The design-first rule

Any PR that changes observable behavior, public API, or architecture
must include the corresponding DESIGN.md diff. Design and code are
reviewed together; a behavior change without its DESIGN.md diff is
incomplete by definition.

DESIGN.md is a living document — it always describes the current state,
including the rejected alternatives that justify it. When a decision
changes, don't erase the old rationale: rewrite the section to the new
state and move the superseded approach into the rejected-alternatives
prose ("previously X; changed because Y"). History lives in git blame;
there is no separate decision log. Read DESIGN.md before proposing
architecture changes, and don't relitigate settled trade-offs without
new information.

## Change sizes

1. **Small** — bugfix, internal refactor, doc fix. Just a PR. No
   DESIGN.md diff, because nothing observable changed; if review shows
   it does, the diff gets added.
2. **Feature, one PR** — design first, then a single PR carrying both
   the DESIGN.md diff and the implementation.
3. **Feature, multiple PRs** — the first PR lands the DESIGN.md diff
   plus a checklist at `docs/plans/<feature>.md` (one phase per PR,
   each ends green and mergeable). Subsequent PRs tick boxes; the final
   PR deletes the plan file.

Ideas that aren't being worked on yet live in TODO.md — one line each,
no commitment. An idea graduates out of TODO.md the moment design work
starts. TODO.md records unsettled maybes; DESIGN.md's non-goals section
records *decisions not to do things*, with rationale.

## Hard constraints

- **Renderer, not build system.** `magetui.Deps` must reproduce the
  `mg.Deps` contract exactly (parallel, once-per-function dedup,
  siblings run to completion, errors aggregated). Never change mage's
  observable semantics.
- The execution layer must not import rendering; everything crosses the
  typed event stream (`internal/events`).
- stdin is never read by magetui; it belongs to the user's subprocesses.
- Public API lives in the root package only; implementation under
  `internal/`.

## Dependencies

Go 1.26+. Direct dependencies are limited to `charm.land/bubbletea/v2`,
`charm.land/lipgloss/v2`, `github.com/charmbracelet/colorprofile`, and
`golang.org/x/term`. Adding any other dependency is a design decision —
it goes through DESIGN.md, not go.mod alone.

## Tests

- Execution layer: tested against the `mg.Deps` contract — events in,
  assertions out; no terminal.
- Renderers: golden-file tests (scripted events in, frames out); no PTY
  in tests. The one real-PTY check is the VHS smoke in CI.
- `mage ci` must pass; CI runs linux (test+lint) and windows (test).

## Compatibility

The semver surface is defined in DESIGN.md ("Compatibility promise")
and summarized in README.md. In short: the Go API, the `MAGETUI_*`
environment variables, exit codes, theme names, and plain mode's
structural grep contract are covered; exact rendered output is not.
A PR that touches any covered surface is a breaking change unless it's
purely additive.

## Release checklist

1. `mage ci` green; CI green on the release commit.
2. CHANGELOG.md: move `[Unreleased]` into a new version section with
   the date.
3. If rendered output changed visibly, regenerate the README GIF
   (`vhs examples/demo.tape`).
4. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
