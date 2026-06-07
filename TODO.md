# TODO

Unsettled maybes — one line each, no commitment, no design. An idea
graduates out of here the moment design work starts (DESIGN.md diff,
plus `docs/plans/<feature>.md` if multi-PR). If an entry wants more
than a line, that's a brainstorm signal, not a longer entry.

Not to be confused with DESIGN.md §8: that records *decisions not to
do things*, with rationale; this records things nobody has decided on.

- Keyboard navigation / focus panes — input layer mutating the layout
  policy (steals stdin from interactive targets; that trade-off is why
  it sat out v1).
- `magetui.Interactive()` — terminal-release escape hatch for
  interactive steps via `tea.ReleaseTerminal`/`RestoreTerminal`;
  already designed-for in DESIGN.md stdin section.
- `MAGETUI_EVENTS=json` — machine-readable event stream; a fourth
  event consumer, nearly free.
- go-tui as rendering backend — revisit at their 1.0.
- Cross-run width cache for plain-mode column stability — revisit if
  it itches (DESIGN.md §4.6).
- Full-tree recap at exit — revisit with dogfood evidence if
  multi-failure triage itches (DESIGN.md §4.1).
- GitHub release automation — draft release from CHANGELOG.md on tag
  push.
- Per-target prefix styling — full style (bold/italic/fg/bg) beyond
  the icon, plus auto-assignment pools (single-hue gradient, rainbow);
  settle stable assignment (name-hash vs registration order), theme
  interplay, profile degradation.
