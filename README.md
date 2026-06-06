# magetui

A `docker buildx`-style live progress display for [mage](https://magefile.org) builds.

> **Status: design phase.** The design is settled ([DESIGN.md](DESIGN.md)); implementation has not started.

magetui is a Go library — not a CLI, not a mage fork. Import it in your
magefile, wrap the targets you care about, and your build renders as a live
dependency tree: spinners and elapsed timers on running steps, scrolling
output tails, completed subtrees committed to terminal scrollback, and full
output replay for failed steps only.

```go
func All(ctx context.Context) error {
    return magetui.Target(ctx, func(ctx context.Context) error {
        magetui.Deps(ctx,
            magetui.F(Build, magetui.Icon("🔨")),
            magetui.F(Test,  magetui.Icon("🧪")),
        )
        return nil
    })
}
```

```text
✓ 🔨 build            11.0s
  ✓ codegen            1.1s
  ✓ compile            9.8s
─────────────────────────────
⠋ all                 12.4s
  ⠹ 🧪 test            5.5s
    ✓ unit             3.4s
    ⠸ lint             5.5s
      │ golangci-lint run
```

## Principles

- **Renderer, not build system.** `magetui.Deps` reproduces the `mg.Deps`
  contract exactly — parallel, deduped, all siblings run to completion,
  errors aggregated. Only the presentation changes.
- **Explicit opt-in.** Targets migrate one at a time via `magetui.Target`;
  unwrapped targets stay plain mage targets.
- **stdin untouched.** No keyboard handling; interactive subprocesses keep
  working.
- **Degrades honestly.** Non-TTY or `MAGETUI_PROGRESS=plain` gives prefixed
  line-by-line output for CI. `NO_COLOR` and color-profile degradation are
  automatic. Terminal progress (OSC 9;4 — Ghostty, Windows Terminal) is
  emitted where supported.

## Themes

Four embedded design intents — `color` (adaptive light/dark), `greyscale`,
`mono` (no color, full glyphs), `ascii` (glyph-poor terminals) — selectable
via `WithTheme(...)` or `MAGETUI_THEME`. Custom themes are a struct of glyph
strings and lipgloss styles.

## License

[MIT](LICENSE)
