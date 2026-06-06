# magetui — Design

A `docker buildx`-style live progress display for [mage](https://magefile.org) builds.

Status: design accepted, pre-implementation.
Module: `github.com/dmotylev/magetui` · Go 1.26+ · deps:
`charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `golang.org/x/term`.

Bubble Tea **v2** (stable since 2026-02-23) is pinned for the rebuilt
cell-diffing renderer and synchronized-output support — both directly relevant
to our ~100ms-tick live region. Caveat: the TUI phase starts with a half-day
**spike** — a ~50-line prototype proving our three load-bearing primitives in
v2 (inline mode, `Println`-style scrollback commit, input disabled) before the
real renderer is built on it. Fallback if the spike sours: bubbletea v1.

## 1. What magetui is

magetui is a **Go library** — not a CLI, not a mage fork or wrapper. A magefile
imports it and opts in per target; mage itself is untouched and unaware.

The core bargain: magetui **reproduces** mage's execution semantics (the
`mg.Deps` contract: parallel execution, once-per-target dedup, all siblings run
to completion, errors aggregated) while **observing** everything — so it can
render a live, tree-shaped view of the run. It is a renderer with perfect
information, never a build system with opinions. magefile core behaviour is
never changed.

A target opts in explicitly:

```go
func All(ctx context.Context) error {
    return magetui.Target(ctx, func(ctx context.Context) error {
        magetui.Deps(ctx, Build, Test)
        return nil
    })
}
```

`magetui.Target` is the lifecycle boundary: it boots the renderer (Bubble Tea,
inline mode, `WithInput(nil)` — stdin stays free for targets), establishes the
root step in the context, recovers panics into rendered failures, and on exit
tears the live region down cleanly and replays failures. Targets that don't
wrap stay plain mage targets; migration is per-target, at the developer's
pace. No global init, no magic, no drop-in compatibility chase — clean
architecture and useful primitives over sed-ability.

When stdout is not a TTY, or when explicitly requested
(`MAGETUI_PROGRESS=plain`), the same event stream renders as prefixed
line-by-line output — the `--progress=plain` equivalent, safe for CI and log
collectors.

### Decisions that got us here

| Decision | Choice | Rejected alternatives |
|---|---|---|
| Product shape | Non-interactive progress renderer (run target → render tree → exit) | Interactive target browser/runner |
| Structure source | Library imported by the magefile | Wrapper CLI parsing `mage -v` output (fragile attribution); wrapper CLI using mage-as-library with injected instrumentation (compiler machinery) |
| API style | Mirror mage's shapes (`Deps`, `Run`) + output primitives (`Printf`, `Status`) keyed off `context.Context` | Step-handle object API; dual API |
| Lifecycle | Explicit `magetui.Target` wrapper per opted-in target | Implicit lazy singleton on first call |
| Visual model | Tree-shaped buildx: dependency tree visible, collapse/tail/duration mechanics | Faithful flat buildx list; minimal one-line-per-step |
| Space allocation | Auto-split among active steps; no keyboard | Focus pane + j/k navigation (steals stdin from interactive targets); deferred, architecture permits later |
| Scrollback model | Subtree-block commit (see §4) | Completion-order commit of individual lines with path prefixes |
| Rendering machinery | Bubble Tea inline + lipgloss | Hand-rolled ANSI (owns every cursor/Windows edge case); BuildKit `progressui` import (wrong data model, heavy deps); tview/gocui (altscreen, input-centric); go-tui (pre-1.0, revisit at 1.0) |

## 2. Public API

```go
// Lifecycle — the only entry point that boots/stops the renderer.
func Target(ctx context.Context, fn func(context.Context) error, opts ...TargetOption) error

// Dependencies — mg.Deps semantics: parallel, deduped, all run to completion.
func Deps(ctx context.Context, deps ...any) // accepts funcs and F(...) values
func F(fn any, opts ...StepOption) Dep      // attach metadata to a dep

// Ad-hoc sub-steps within a target body.
func Step(ctx context.Context, name string, fn func(context.Context) error, opts ...StepOption) error

// Subprocess execution — the sh replacement; output captured per-step.
func Run(ctx context.Context, cmd string, args ...string) error
func RunWith(ctx context.Context, env map[string]string, cmd string, args ...string) error
func Output(ctx context.Context, cmd string, args ...string) (string, error)

// Output primitives — replace stray fmt.Println.
func Printf(ctx context.Context, format string, a ...any) // append a log line to the step's tail
func Status(ctx context.Context, text string)             // transient text on the step's own line
```

- Accepted dep types mirror mage: `func()`, `func() error`,
  `func(context.Context)`, `func(context.Context) error`, plus `Dep` values
  from `F`. Step names derive from function names via reflection.
- Dedup is keyed on underlying function identity
  (`reflect.ValueOf(fn).Pointer()`), so a target decorated at one callsite and
  bare at another is still one step; first registration's decoration wins.
- `Status` *replaces* the step's status text (buildx's transfer-counter feel);
  `Printf` *appends* to the scrolling tail.
- `Output` mirrors `sh.Output`: stdout captured and returned to the caller
  (not rendered); stderr goes to the tail.
- Misuse safety: calling any primitive with a context that carries no step
  degrades to plain stderr output with a one-time warning — never crash a
  build over cosmetics.

### Step decoration

`F` mirrors the `mg.F` idiom:

```go
magetui.Deps(ctx,
    magetui.F(Build, magetui.Icon("🔨")),
    magetui.F(Test,  magetui.Icon("🧪")),
    Lint, // bare functions still fine
)
```

Icons ride the step through all renderers (live line, committed scrollback
line, plain-mode prefix, failure replay). Icons are *decoration*, never
identity or alignment anchors: width-aware optional prefixes (emoji are
double-width; lipgloss measures correctly). `ThemeASCII` drops them.

### Options & environment

`TargetOption`: `WithTheme(Theme)`, `WithOutput(io.Writer)`,
`WithProgressMode(Auto|TTY|Plain)`.

Env overrides code: `MAGETUI_THEME` (`color|mono|greyscale`),
`MAGETUI_PROGRESS` (`auto|tty|plain`), `MAGETUI_OSC_PROGRESS` (`on|off`).
`NO_COLOR` respected (via termenv).

### stdin

Subprocesses get the magefile process's own `os.Stdin`, untouched —
`WithInput(nil)` means nobody competes for it. Interactive prompts inside a
step work mechanically but look awkward under the live region (prompt text is
captured into the tail; typed characters echo at the cursor). Documented wart;
remedy: run interactive steps in plain mode. A `magetui.Interactive(ctx, ...)`
escape hatch using `tea.ReleaseTerminal`/`RestoreTerminal` is a designed-for
but post-v1 addition.

## 3. Internal architecture

Three layers, strictly separated. The seam is the BuildKit lesson: buildx
ships `--progress=tty|plain|rawjson` as interchangeable consumers of one
`SolveStatus` event stream, and that separation is why each front-end is
simple. We steal the seam, not the architecture.

### 3.1 Execution layer (`Deps`, `Step`, `Run`)

Owns goroutines and the **step registry**: a mutex-guarded map keyed by
function identity (same dedup contract as mage's name-keyed once-map) holding
each step's state: name, icon, parent edges, status
(pending/running/ok/failed/panicked/interrupted), timestamps, and its output
buffer.

Output buffers are bounded (default ~1 MiB per step): overflow keeps head +
tail and records an elision marker — failure replay says
`… 1,204 lines elided …` rather than OOMing on a chatty step. The live tail
is just a window over this buffer; nothing is lost up to the cap.

`Deps` cannot delegate to `mg.CtxDeps` internally: wrapping targets in
instrumentation closures would break mage's once-per-function dedup. The same
semantics are reimplemented, keyed by function identity.

### 3.2 Event stream

The execution layer never touches rendering; it emits typed events into a
channel:

- `StepStarted{id, name, icon, parent}`
- `StepFinished{id, status, err, panicValue, stack, duration}`
- `OutputLine{id, origin /* stdout|stderr */, text}`
- `StatusChanged{id, text}`

Every front-end is just a consumer. A future `MAGETUI_EVENTS=json`
machine-readable dump is a fourth consumer, nearly free. (Post-v1.)

### 3.3 Renderers

Three consumers behind one interface:

- **TUI** — the Bubble Tea program (inline mode, `WithInput(nil)`). Its model
  *is* the step-tree snapshot; `Update` consumes events; `View` renders the
  live region (tree layout + degradation ladder); `tea.Println` commits closed
  subtree blocks to scrollback.
- **Plain** — stateless line-per-event printer with step prefixes.
- **OSC 9;4** — tiny stateful emitter (percent, error state, clear-on-exit).

`Target` wires the three together, selects TUI vs plain (TTY detection +
overrides), and owns shutdown ordering:
drain events → final tree commit → failure replay → OSC clear → restore terminal.

## 4. Rendering

### 4.1 Commit-and-scroll, subtree-block model

The screen splits into two zones:

- **Scrollback (permanent):** committed blocks, printed above the live region
  via `tea.Println`, never repainted. Terminal scrollback is the archive.
- **Live region (redrawn):** the currently open tree, anchored at the bottom;
  the only repainted area.

A finished step stays in the live tree until its **whole subtree closes**; the
subtree then commits to scrollback as one indented block (final glyphs,
durations, cause suffixes). Scrollback reads as proper trees, chunk by chunk.
The root commits at `Target` exit.

```text
MID-RUN                                      LATER — `build` subtree committed
─────────────────────────────                ✓ build              11.0s
⠋ all                 12.1s                    ✓ codegen           1.1s
  ⠼ build             11.0s                    ✓ compile           9.8s
    ✓ codegen          1.1s                  ─────────────────────────────
    ⠙ compile          8.0s                  ⠋ all                 12.4s
      │ go build ./pkg/...                     ⠹ test               5.5s
      │ linking magetui                          ✓ unit             3.4s
  ⠹ test               5.2s                      ⠸ lint             5.5s
    ✓ unit             3.4s                        │ golangci-lint run
    ⠸ lint             5.2s
      │ golangci-lint run
```

### 4.2 Live-region layout

Each frame (~100ms tick, plus on every event and resize):

1. Walk the open tree depth-first; one row per open step line is mandatory.
2. Remaining rows distribute as output tails to *running leaf* steps,
   deepest-first, capped at `TailLines` (default 5).
3. Degradation ladder as active count grows: 5-line tails → 3 → 1 → 0;
   if one-row-per-step still overflows: running steps win over
   completed-waiting-for-siblings, longest-running win among running (they're
   what you're waiting on), and the cut is summarized as `… +N more`.
4. SIGWINCH → recompute.

Tail lines are truncated to terminal width — no wrapping in the live region
(wrapped lines break repaint row arithmetic); full lines live in the buffer
for replay. Elapsed timers tick per frame for running steps; committed
durations are final and exact.

### 4.3 stdout vs stderr

`Run` wires separate pipes feeding one per-step tail (arrival order
preserved), each line tagged with origin. Theme renders them distinguishably:
normal gutter `│` for stdout, error-styled `┃` for stderr (ASCII: `|` vs `!`).
The tagging carries into plain mode and the failure replay.

### 4.4 Failures and replay

- Committed line keeps the ✗ glyph in failure style **plus** a cause suffix:
  `✗ compile 9.8s — exit status 2`. A failed subtree block commits with its
  ✗ path intact: scrollback alone tells you where it died.
- At exit, after the final tree commit, a detail section replays the
  **complete** captured output of failed steps only — same rendering path as
  the live tail, just unwindowed: same gutters, `$` prefix for the command.

```text
✗ all                 14.0s
  ✗ build             11.0s
    ✓ codegen          1.1s
    ✗ compile          9.8s
  ✓ test               7.7s
    ✓ unit             3.4s
    ✓ lint              7.7s

──────────────────────────────────────────
✗ all ▸ build ▸ compile   9.8s   exit status 2
  $ go build ./...
  │ pkg/foo/bar.go:12:6: undefined: Frobnicate
  │ pkg/foo/baz.go:33:2: imported and not used: "fmt"
  │ ... (all lines, nothing elided up to the buffer cap)

1 of 6 steps failed.
exit status 1
```

### 4.5 Panics are not failures

`✗` means "your code/tests are bad"; a panic means "your *magefile* is bad."
Distinct triage paths get distinct visuals:

- Dedicated `Panic` glyph (`‼`; ASCII: `!!`) with its own,
  more alarming style.
- Committed line carries the panic value:
  `‼ compile 0.3s — panic: runtime error: index out of range [3]`.
- Replay: captured output as usual, then the **stack trace as a separate
  styled block** — own gutter, trimmed to start at the magefile frame
  (`magefile.go:42` is what the developer wants); full untrimmed stack below,
  dimmed.
- Propagation unchanged: recovered panics aggregate into the error flow
  exactly as mage does, exit code included. Only presentation knows.

### 4.6 Plain mode

Stateless, append-only, log-collector safe. Active when stdout is not a TTY
or on explicit request:

```text
▸ build ▸ compile  started
🔨 build ▸ compile | go build ./...
🔨 build ▸ compile ! pkg/foo/bar.go:12:6: undefined: Frobnicate
✗ build ▸ compile  9.8s  exit status 2
```

No timers, no repainting. Failure replay identical to TUI's.

### 4.7 OSC 9;4 terminal progress

Third output channel alongside live region and scrollback. Renders as a
progress bar in Ghostty (≥1.2), Windows Terminal (taskbar), ConEmu. Works
while the window is unfocused — which is when builds run.

- Run start → indeterminate (`9;4;3`); once the deduped step set stabilizes →
  determinate (`9;4;1;<pct>`), `pct = completed/known`. The denominator may
  grow as `Deps` discovers targets: the bar occasionally slows, never lies.
- First failure → error state (`9;4;2;<pct>`) — red tint before you alt-tab.
- Exit → clear (`9;4;0`), **always**, including on panic.
- Gating: default-on only for known emitters (`TERM_PROGRAM=ghostty`,
  `WT_SESSION`, ConEmu env); `MAGETUI_OSC_PROGRESS=on|off` overrides. Also
  active in plain mode when running in a real terminal.

## 5. Themes

`Theme` carries two flat groups — **palette** (lipgloss styles) and **glyphs**
(strings, not runes: multi-codepoint glyphs and ligature pairs like `=>` must
work):

```go
type Theme struct {
    // Glyphs
    Spinner   []string // animation frames
    OK, Fail, Skip, Panic    string
    GutterOut, GutterErr     string // │ vs ┃ ; ASCII: | vs !
    Branch, Elbow            string // tree connectors
    StackGutter              string

    // Palette
    Name, Duration, Status, TailText, TailErr,
    Path, FailureStyle, PanicStyle, StackStyle lipgloss.Style
}
```

Color capability and glyph repertoire are **orthogonal axes** — a `NO_COLOR`
purist on a modern terminal renders emoji fine, while a glyph-poor terminal
(linux console, serial) often still has the 8 basic colors. The embedded
themes reflect that:

- `ThemeColor` — adaptive default (`lipgloss.AdaptiveColor` per light/dark
  background), full Unicode glyphs, icons kept.
- `ThemeGreyscale` — intensity without hue, full glyphs, icons kept.
- `ThemeMono` — no color at all; Unicode glyphs and icons kept. Answers
  "I hate colors," not "my terminal is from 1978."
- `ThemeASCII` — pure ASCII repertoire: `|`/`!` gutters, `+--` connectors,
  `OK`/`XX`/`!!` markers, `-\|/` spinner, icons dropped. Answers the
  tofu-box case; natural base for `TERM=dumb`.

Selected via `WithTheme` or `MAGETUI_THEME` (`color|greyscale|mono|ascii`).

Two principles keep the theme count at four:

- **Glyph loudness is inversely proportional to the color budget.** Where
  color carries the stdout/stderr signal, gutter glyphs stay subtle
  (`│` vs `┃`); where it can't, glyphs get louder (`ThemeMono`: `│` vs `║`
  + bold; `ThemeASCII`: `|` vs `!`). Same rule applies to status markers.
- **Themes are design intents, not capability-matrix cells.** The color axis
  degrades automatically underneath every theme (termenv: truecolor → 256 →
  16 → none; `NO_COLOR` forces the bottom). "ASCII without color" is
  `ThemeASCII` + `NO_COLOR` by composition — no fifth theme.

Color degradation (truecolor → 256 → 16 → none) and `NO_COLOR` come free via
termenv.

## 6. Edge cases & fidelity

- **`mg.Fatal` exit codes** honored: `Target` returns errors untouched to
  mage's machinery.
- **Nested `Target`** (target A wraps; deps on B which also wraps): detected
  via context — the inner becomes an ordinary step instead of booting a second
  renderer. Library-style shared targets can wrap defensively.
- **Deduped failure**: a step that failed reports the same error to every
  dependent.
- **Signals**: SIGINT/SIGTERM → cancel root context, mark interrupted steps
  (`⊘`, "interrupted"), run full teardown (commit, replay, OSC clear, restore
  terminal), exit with conventional code. Second SIGINT during teardown exits
  immediately — the user outranks the renderer.
- **Terminal restore** also runs on panic via the `Target` defer. A build tool
  that leaves the terminal raw is unforgivable.

## 7. Testing

The event seam pays off:

- **Execution layer**: tested against the `mg.Deps` contract with no terminal —
  events in, assertions out (dedup, parallelism, error aggregation, panic
  capture, buffer bounding).
- **Renderers**: golden-file tests — scripted event sequences in, frame
  strings out. Bubble Tea's `View()` is a pure function: no PTY gymnastics
  for layout tests. Covers the degradation ladder, subtree commits, width
  truncation, themes.
- **Manual rig**: a demo magefile with artificially slow, chatty, and failing
  targets (`Brew`, `Overthink`, `DropTable`).

## 8. v1 non-goals

Explicitly out, architecture-permitting later:

- Keyboard navigation / focus panes (renderer consumes a layout policy; an
  input layer can mutate it later).
- `magetui.Interactive()` terminal-release escape hatch.
- `MAGETUI_EVENTS=json` machine-readable event stream.
- go-tui as rendering backend (revisit at their 1.0).

## 9. Layout

Single module `github.com/dmotylev/magetui`:

```text
magetui/
├── DESIGN.md
├── go.mod
├── *.go               # public API (root package)
├── internal/
│   ├── engine/        # step registry, Deps semantics, subprocess plumbing
│   ├── events/        # event types, stream
│   └── render/        # tui, plain, osc renderers; themes
└── examples/
    └── magefile.go    # demo / manual test rig
```
