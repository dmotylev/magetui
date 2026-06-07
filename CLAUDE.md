# magetui

`docker buildx`-style live progress renderer for mage builds. Go library,
not a CLI.

## Source of truth

DESIGN.md. It records every decision *and* the rejected alternatives —
read it before proposing architecture changes; don't relitigate settled
trade-offs without new information.

## Hard constraints

- Renderer, not build system: `magetui.Deps` must reproduce the `mg.Deps`
  contract exactly (parallel, once-per-function dedup, siblings run to
  completion, errors aggregated). Never change mage's observable semantics.
- The execution layer must not import rendering; everything crosses the
  typed event stream (`internal/events`).
- stdin is never read by magetui; it belongs to the user's subprocesses.
- Public API lives in the root package only; implementation under `internal/`.

## Conventions

- Go 1.26+. Deps limited to: charm.land/bubbletea/v2, charm.land/lipgloss/v2,
  github.com/charmbracelet/colorprofile (lipgloss's writer companion),
  golang.org/x/term.
  Adding any other dependency is a design decision, not a convenience.
- Renderer tests are golden-file based (events in, frames out); no PTY
  in tests.
- Humor is acceptable in test fixtures only, never in code, comments,
  or docs.
