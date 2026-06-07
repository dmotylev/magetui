# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The compatibility surface that semver applies to is defined in
DESIGN.md ("Compatibility promise").

## [Unreleased]

## [0.1.0] - TBD

Initial release.

### Added

- `Target` lifecycle wrapper: renderer boot/teardown, panic recovery,
  failure replay, exit codes; nested `Target` degrades to an ordinary
  step.
- `Deps`/`F`/`Step` with the exact `mg.Deps` contract: parallel,
  once-per-function dedup, siblings run to completion, errors
  aggregated.
- `Run`/`RunWith`/`Output` subprocess helpers: per-step output capture
  with stdout/stderr origin tagging, stdin passthrough.
- `Printf`/`Status` output primitives.
- TUI renderer (Bubble Tea v2 inline): live dependency tree, spinners
  and elapsed timers, output tails, degradation ladder, root-child
  subtree commits to scrollback.
- Plain renderer for CI and pipes: columnar one-line-per-event format
  with a grep-stable gutter (`^!` = stderr, `^$` = command).
- Failure replay after exit: complete captured output of failed steps
  only; panics presented distinctly with magefile-trimmed stacks.
- Themes: `color`, `greyscale`, `mono`, `ascii`; `MAGETUI_THEME`;
  automatic color-profile degradation and `NO_COLOR`.
- OSC 9;4 terminal progress (Ghostty, Windows Terminal, ConEmu):
  indeterminate pulse, opt-in percent, sticky error state,
  clear-on-exit; `MAGETUI_OSC_PROGRESS`.
- Signal handling: SIGINT observed via mage's context, own SIGTERM
  watcher; `⊘` interrupted classification; exit codes 130/143; TUI
  degrades to plain on ^C.
- Environment overrides: `MAGETUI_PROGRESS`, `MAGETUI_THEME`,
  `MAGETUI_OSC_PROGRESS`.
