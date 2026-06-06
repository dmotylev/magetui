# Implementation plan

Working document; delete after v0.1.0. Each phase is one PR, ends green and
mergeable. Design: DESIGN.md.

## Phase 0 — scaffolding

- [x] magefile.go (plain mg/sh): Test, Lint, Vet targets
- [x] .github/workflows/ci.yml — linux: test+lint; windows: test
      (portability tripwire: build tags, exec/CRLF, TTY detection;
      terminal rendering is not CI-testable — delegated to bubbletea CI,
      manual rig, and the Phase 7 VHS smoke)
- [x] golangci-lint config

## Phase 1 — events + engine

- [x] internal/events: StepStarted, StepFinished, OutputLine, StatusChanged
- [x] internal/engine: step registry; fn-identity dedup; Deps semantics
      (parallel, all siblings run to completion, error aggregation, panic
      capture with stack); bounded output buffers (head+tail elision);
      ctx step carrier
- [x] mg.Deps contract tests — the most important code in the project

## Phase 2 — subprocess plumbing

- [ ] Run / RunWith / Output: per-step pipes, stdout/stderr origin tags,
      arrival-order merge, stdin passthrough, exit-code extraction

## Phase 3 — plain renderer, API wired end to end

- [ ] Target lifecycle: TTY detection, MAGETUI_PROGRESS, exit codes,
      failure replay
- [ ] Plain renderer: line-per-event, path prefixes, icons
- [ ] Dogfood checkpoint: this repo's magefile switches to magetui

## Phase 4 — TUI renderer

- [ ] Spike (half-day cap): ~50-line bubbletea v2 prototype proving inline
      mode, Println scrollback commit, input disabled. Fallback: v1
- [ ] Live region: tree layout, degradation ladder, width truncation, timers
- [ ] Subtree-block commits via tea.Println
- [ ] Golden-file tests (events in, frames out)

## Phase 5 — themes + panic presentation

- [ ] Theme struct (glyphs as strings + lipgloss palette)
- [ ] Embedded: color, greyscale, mono, ascii; MAGETUI_THEME
- [ ] Panic styling: glyph/style, magefile-frame stack trimming

## Phase 6 — OSC 9;4, signals, edge cases

- [ ] OSC emitter: indeterminate → percent, error state, clear-on-exit;
      env gating; MAGETUI_OSC_PROGRESS
- [ ] SIGINT/SIGTERM teardown; second-signal immediate exit; build-tagged
      Windows variants
- [ ] Nested Target; terminal restore on panic

## Phase 7 — examples, demo, release

- [ ] examples/ rig: slow, chatty, failing, panicking targets
- [ ] VHS tape: README GIF + real-PTY smoke test in CI
- [ ] README update; tag v0.1.0; delete this file
