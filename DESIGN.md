# magetui — Design

A `docker buildx`-style live progress display for [mage](https://magefile.org) builds.

Status: design accepted, pre-implementation.
Module: `github.com/dmotylev/magetui` · Go 1.26+ · deps:
`charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `golang.org/x/term`,
plus `github.com/charmbracelet/colorprofile` (lipgloss's companion
writer, already in bubbletea's graph) so plain mode and the post-exit
replay degrade colors without booting a program.

Bubble Tea **v2** (stable since 2026-02-23) is pinned for the rebuilt
cell-diffing renderer and synchronized-output support — both directly relevant
to our ~100ms-tick live region. The Phase 4 **spike** (2026-06-07) proved the
three load-bearing primitives in v2.0.7: inline mode is the v2 *default*
(alt-screen is per-`View` opt-in), `tea.Println` commits multi-line blocks
above the live region via insert-lines, and `WithInput(nil)` leaves stdin
untouched without even entering raw mode — subprocesses inherit a normal
terminal. No v1 fallback needed. v2 API deltas: `Init()` returns only `Cmd`;
`View()` returns `tea.View`, not `string`. One scar for test harnesses: a
zero-size PTY (winsize 0×0) truncates every frame to nothing.

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

Icons ride the step through the TUI renderer (live line, committed
scrollback line, failure replay). Icons are *decoration*, never identity or
alignment anchors: width-aware optional prefixes (emoji are double-width;
lipgloss measures correctly). `ThemeASCII` drops them, and **plain mode
drops them too** — its first column belongs to the glyph/gutter grid
(§4.6), and double-width emoji would break it.

### Options & environment

`TargetOption`: `WithTheme(Theme)`, `WithOutput(io.Writer)`,
`WithProgressMode(Auto|TTY|Plain)`.

Env overrides code: `MAGETUI_THEME` (`color|mono|greyscale`),
`MAGETUI_PROGRESS` (`auto|tty|plain`), `MAGETUI_OSC_PROGRESS` (`on|off|percent`).
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

- **TUI** — two pieces with very different testability. The **layout core**
  (`render.Tree`) owns the step-tree snapshot: events in, `Frame(width,
  height, now) string` and `TakeBlocks() []string` out — no bubbletea import,
  no goroutines, no clock (`now` is a parameter so goldens replay scripted
  timelines). The **adapter** wraps it for bubbletea (inline mode,
  `WithInput(nil)`): `Handle` folds events into the tree under a small mutex
  and delivers freshly closed blocks through `Program.Println`, then nudges a
  repaint via `Program.Send` (documented to block until the program runs and
  no-op after exit — no forwarding goroutine needed); a thin `tea.Model`
  handles the ~100ms tick, resize, and `View` returning the frame. Blocks
  deliberately do *not* ride `tea.Println` commands out of `Update`, which
  was the original plan: v2 runs every command in its own goroutine, so
  commands from different Updates are unordered — commits would shuffle, and
  the root line (always committed last) reliably lost its race against
  `Quit`. `Program.Println` enqueues into the program's FIFO synchronously
  from the caller, and the engine already serializes `Handle`: commit order
  and every-block-before-`Quit` follow. Three v2.0.7 scars the adapter
  absorbs: `Program.Println`, unlike `Send`, has no after-exit guard (a dead
  program would block the engine forever — the delivery wait is paired with
  a program-finished signal); the program writes to its output from two
  goroutines under different locks (kernel-serialized on an `*os.File`, a
  genuine data race on any other writer); and it interrogates the terminal
  even though input is disabled — the kitty-keyboard query on the first
  render plus DECRQM 2026/2027 at startup. Nobody can read those answers
  under `WithInput(nil)`: they land in the cooked-mode stdin buffer, the
  line discipline echoes them mid-frame as `^[[?1u`-style garbage, and the
  next stdin reader inherits them as phantom keystrokes. The cure for the
  last two is one wrapper at the output seam: every writer gets a
  serializing sequence-stripper (the stripped features needed the
  unreadable answers anyway — nothing is lost), and a terminal file keeps
  its `Read`/`Close`/`Fd()` surface through it, since bubbletea finds the
  TTY by type-asserting the output. The keyboard *set* sequences —
  modifyOtherKeys(2) and the kitty disambiguate flag, emitted
  unconditionally at renderer start — are stripped too. Phase 4 left them
  in ("they elicit no responses and are reset at close"), which missed
  that they change ^C semantics: a terminal honoring either protocol
  (Ghostty, kitty, WezTerm, xterm) reports ^C as an escape sequence on
  stdin instead of a `0x03` byte, the line discipline never raises
  SIGINT, and the §6 interrupt handling silently dies — discovered in
  Phase 6 when ^C stopped stopping builds in Ghostty. With input disabled
  nobody reads the enhanced events, so stripping them restores classic ^C
  at zero cost. The `tea.Model` itself is not unit-tested — the spike
  proved its primitives, the Phase 7 VHS rig smoke tests it; the sequence
  stripper, a pure writer, has its own tests.
- **Plain** — stateless line-per-event printer with step prefixes.
- **OSC 9;4** — tiny stateful emitter (pulse by default, opt-in percent, error state, clear-on-exit).

Renderers satisfy a small interface: `Handle(events.Event)` plus
`Close() error` (no-op for plain; for TUI: flush remaining blocks, quit, wait
for terminal restore). The failure replay is *not* on the interface — it is
one shared function both modes use, called by `Target` with self-contained
per-step facts (path, outcome, cause, captured lines, stack), so it needs no
renderer state and renders identically everywhere.

`Target` wires the three together, selects TUI vs plain (TTY detection +
overrides), and owns shutdown ordering: drain events → final tree commit →
quit + restore terminal → failure replay (plain prose below the vanished live
region — a crash in replay code can never leave the cursor hidden) → OSC
clear. If the TUI program itself errors, `Close` reports it and `Target`
still prints the replay from engine buffers: a broken TUI never eats the
build's diagnosis.

## 4. Rendering

### 4.1 Commit-and-scroll, subtree-block model

The screen splits into two zones:

- **Scrollback (permanent):** committed blocks, printed above the live region
  via `tea.Println`, never repainted. Terminal scrollback is the archive.
- **Live region (redrawn):** the currently open tree, anchored at the bottom;
  the only repainted area.

Commit granularity is **root-children**: a finished step waits in the live
tree until the **root-child subtree containing it** fully closes; that
subtree then commits as one indented block (final glyphs, exact durations,
cause suffixes — `✗ compile  9.8s — exit status 2`), atomically, in
completion order. Scrollback reads as proper trees, chunk by chunk. At
`Target` exit the root commits its **own line only** — the period at the end
of the run; it always commits, with `✗`/`‼` and cause if the root itself
died, so scrollback alone says how the run ended.

Rejected granularities: *every-subtree-immediately* (leaves are one-step
subtrees, so every leaf commits alone — scrollback degenerates to a flat
finish-order log and no block ever shows nesting) and *full-tree recap at
exit* (piecewise blocks plus the replay headers — `✗ all ▸ build ▸ compile`
per failure plus the failed-count line — already carry every path and all
green context; a recap re-orders but adds nothing, and duplicates the tree
on every red run. Revisit with dogfood evidence if multi-failure triage
itches). Cost of root-children: long-lived root children keep finished
descendants in the live region; the ladder hides completed-waiting steps
first, so they fold away under pressure.

```text
MID-RUN                                      LATER — `build` subtree committed
─────────────────────────────                ✓ build              11.0s
⠼ build               11.0s                    ✓ codegen           1.1s
  ✓ codegen            1.1s                    ✓ compile           9.8s
  ⠙ compile            8.0s                  ─────────────────────────────
    │ go build ./pkg/...                     ⠹ test                5.5s
    │ linking magetui                          ✓ unit              3.4s
⠹ test                 5.2s                    ⠸ lint              5.5s
  ✓ unit               3.4s                      │ golangci-lint run
  ⠸ lint               5.2s
    │ golangci-lint run
```

### 4.2 Live-region layout

The live region starts at the cursor and is only ever as tall as its
content; its ceiling is the full terminal height minus one row (reserved
against bottom-line repaint edge cases). No artificial quota below the
physical one: the ladder is the pressure valve, so pressure — not a number —
triggers it. Each frame (~100ms tick, plus on every event and resize) is
recomputed fresh from `(open tree, width, height, now)` — no incremental
layout state, so resize is just another frame:

1. Walk the open tree depth-first; one row per open step is mandatory:
   `glyph name elapsed` — spinner and live elapsed for running steps
   (both pure functions of `now`), final glyph and exact duration for
   finished-waiting ones. Transient status text rides after the elapsed
   (`⠙ push  3.6s  uploading 12MB`), replaced by the next `StatusChanged`,
   dropped at finish. Indentation is two spaces per depth, no box-drawing
   connectors — committed blocks stay grep-clean.

   The **root's own line is omitted** while it has children and no status
   text — it repeats on every frame and says nothing, the same reasoning
   that drops the root path segment from plain-mode live lines (§4.6).
   Root children therefore render at depth 0, *matching committed-block
   indentation exactly*: a commit freezes lines in place and scrolls them
   up rather than shifting them left. The root line appears only before
   its first child starts (something must spin) or while it carries
   status text; its committed line at exit reports the total (§4.1).
2. Remaining rows distribute as output tails to *running leaf* steps,
   deepest-first, capped at the ladder's current tier (`TailLines`,
   default 5).
3. Degradation ladder: try tiers 5 → 3 → 1 → 0; the first tier where
   everything fits wins. Still overflowing at 0, cut whole step rows:
   completed-waiting first (youngest first), then running steps
   shortest-running first — the longest-running survive to the last row
   (they're what you're waiting on). The cut is one `… +N more` row at the
   bottom.
4. SIGWINCH → next frame recomputes; shrink tightens the ladder, growth
   relaxes it — nothing was lost, only unwindowed. A zero-size frame
   (before the first `WindowSizeMsg`) renders empty, not garbage.

Live-region lines are hard-truncated to terminal width by rune count with a
trailing `…` — no wrapping (wrapped lines break repaint row arithmetic).
Truncation applies to the live region *only*: committed blocks and the
failure replay are full-fidelity and wrap naturally — scrollback is never
repainted, so the terminal's own wrapping is harmless there. Full lines live
in the engine buffers for replay. Elapsed timers tick per frame for running
steps; committed durations are final and exact. Truncation happens *before*
styling — Phase 5 inherits geometry that never counts ANSI.

### 4.3 stdout vs stderr

`Run` wires separate pipes feeding one per-step tail (arrival order
preserved), each line tagged with origin. Theme renders them distinguishably:
normal gutter `│` for stdout, error-styled `┃` for stderr (ASCII: `|` vs `!`).
The tagging carries into plain mode and the failure replay.

Lines render the moment the child writes them — the tail window scrolls
line-by-line (verified frame-by-frame in a PTY capture). A child that
*withholds* its output looks buffered, and no renderer can fix that: the
bytes don't arrive. `go test ./...` is the canonical offender — it buffers
per-package output and prints results in sorted package order, so a fast
package's `ok` line waits for every slower package sorted before it
(measured: first byte at 5.8s of an 8.2s run, through a bare pipe with no
magetui involved). buildx panes show the same burst for such tools. The
related classic — libc children switching from line- to full-buffering on
a pipe — has a known cure, running children on a PTY, rejected here: it
conflicts with stdin passthrough and is anything but boring. Livelier
`go test` panes are the magefile's choice: `-v` streams the in-order
package live, or one step per package.

### 4.4 Failures and replay

- Committed line keeps the ✗ glyph in failure style **plus** a cause suffix:
  `✗ compile 9.8s — exit status 2`. A failed subtree block commits with its
  ✗ path intact: scrollback alone tells you where it died.
- At exit, after the final tree commit, a detail section replays the
  **complete** captured output of failed steps only — same rendering path as
  the live tail, just unwindowed: same gutters, `$` prefix for the command.
  One shared implementation serves both modes; in TUI mode it is written by
  `Target` after the program has quit and restored the terminal (§3.3). Its
  per-failure header lines double as the failure summary: every dead path,
  root-anchored, adjacent to its evidence — which is why exit needs no
  full-tree recap (§4.1).

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
or on explicit request. Columnar: the first column is the lifecycle glyph
(`○` started/status, `✓`/`✗`/`‼`/`⊘` finished) or, for output lines, the
origin gutter (`$` command, `|` stdout, `!` stderr — so `grep '^!'` finds
all stderr); the second is the step path, right-padded to the widest path
seen so far; the rest is unbounded.

```text
○ build ▸ compile  started
$ build ▸ compile  go build ./...
! build ▸ compile  pkg/foo/bar.go:12:6: undefined: Frobnicate
✗ build ▸ compile  9.8s  exit status 2
```

A streaming renderer has no lookahead, so the name column cannot be
precomputed: dep lists are discovered by executing target bodies (mage's
codegen-time target list is neither exposed at runtime nor the right set —
the column holds unexported dep funcs, ad-hoc steps, and nested paths).
What it does instead is the `flag.Parse` trick at the one point the
information exists: **started lines are held** until the next non-start
event or ~50ms, whichever comes first, so the sibling burst a `Deps` call
announces prints at one width. A step born later and deeper still widens
the column mid-run; lines already printed keep their narrower padding (the
buildx answer is `#N` step numbers; we'd rather keep names). Live lines
omit the root segment — it is the same on every line; the failure replay
keeps it. Icons are not rendered (see §2); rejected alternatives:
icon-in-first-column (double-width emoji break the grid), unpadded path
prefix (ragged, the dogfood verdict), cross-run width cache (state file for
cosmetics, cold in CI — revisit if it itches). `▸` appears only as the path
separator — one meaning per glyph. No elapsed timers, no repainting.
Failure replay identical to TUI's.

### 4.7 OSC 9;4 terminal progress

Third output channel alongside live region and scrollback. Renders as a
progress bar in Ghostty (≥1.2), Windows Terminal (taskbar), ConEmu. Works
while the window is unfocused — which is when builds run.

Structurally a sibling of the renderers — a tiny stateful emitter
consuming the same event stream, composed *around* whichever renderer is
active: `Target` tees every event into it ahead of the renderer's
`Handle`. It writes to **stderr**, not the render output: each emission is
one complete escape sequence in a single `write()`, and the kernel
serializes tty writes, so a sequence on stderr can never splice into the
middle of a bubbletea frame on stdout — and the channel keeps working when
the build's stdout is piped but the window is still a terminal. Sequences
are emitted only when the encoded state actually changes; events arrive
microseconds apart and the terminal does not need the reruns.

- Default presentation: **indeterminate for the whole run** (`9;4;3`) —
  the pulse. A build's denominator grows as `Deps` discovers targets, so a
  filling bar stutters and occasionally walks backwards in spirit; the
  dogfood verdict is that the pulse simply looks better. "Build running,
  build done" is most of what a taskbar can honestly say.
- `MAGETUI_OSC_PROGRESS=percent` opts into the determinate bar: the
  **first `StepFinished`** flips indeterminate to `9;4;1;<pct>`,
  `pct = finished/known` — by the first completion the initial `Deps`
  burst has registered the whole first wave, so the denominator is honest.
  No timers, no tuning constant (rejected: a quiet-period timer —
  burst-hold logic for a cosmetic channel; immediately determinate — the
  bar twitches through 0/1 → 0/14 at startup, which is exactly what the
  indeterminate state is for). The denominator may still grow: the bar
  occasionally slows, never lies.
- First real failure (`✗`/`‼`, not `⊘`) → error state (`9;4;2;<pct>`) — red
  tint before you alt-tab — and it stays tinted. This happens in **both**
  presentations: the OSC 9;4 vocabulary has no error-indeterminate state,
  so a failure under the pulse carries the percent too — red outranks
  aesthetics. Interruption is *not* error state: the bar just clears at
  exit; red is reserved for builds that broke themselves.
- Exit → clear (`9;4;0`), **always** — success, failure, interruption,
  panic: `Target` defers the clear the moment the emitter exists.
- Gating: default-on only for known emitters (`TERM_PROGRAM=ghostty`,
  `WT_SESSION`, `ConEmuANSI=ON`) *and* stderr on a real terminal — so the
  sequences stay out of CI logs even when those variables leak into the
  environment. `MAGETUI_OSC_PROGRESS=on|off|percent` overrides detection
  in both directions (`percent` implies on), the same precedence pattern
  as `MAGETUI_PROGRESS` and `MAGETUI_THEME`. Active in plain mode too:
  piped build output with a live taskbar is the point.

## 5. Themes

`Theme` carries two flat groups — **palette** (lipgloss styles) and **glyphs**
(strings, not runes: multi-codepoint glyphs and ligature pairs like `=>` must
work):

```go
type Theme struct {
    // Glyphs
    Spinner                      []string // animation frames
    Start                        string   // plain mode: started/status lines
    OK, Fail, Panic, Interrupted string
    GutterOut, GutterErr         string // │ vs ┃ ; ASCII: | vs !
    GutterCmd, StackGutter       string
    PathSep, Ellipsis            string
    Separator, Dash              string
    Icons                        bool

    // Palette
    Name, Duration, Status, TailText, TailErr, TailCmd, Path,
    SpinnerStyle, OKStyle, FailureStyle, PanicStyle,
    InterruptedStyle, StackStyle lipgloss.Style
}
```

(The original sketch had `Branch`/`Elbow` tree connectors; §4.2 settled on
two-space indentation with no box-drawing — grep-clean blocks — so the
fields never existed. Everything that varies between Unicode and ASCII
repertoires is a glyph instead: path separator, ellipsis, replay rule,
cause dash. Plain mode's origin gutters `$`/`|`/`!` are deliberately *not*
themed — `grep '^!'` is a §4.6 contract; `GutterOut`/`GutterErr` speak for
the TUI tail and the replay. Lifecycle glyphs share one display width per
theme; the glyph column pads over any remaining mix, so ASCII's classic
one-column `-\|/` spinner sits beside its two-column markers.)

Color capability and glyph repertoire are **orthogonal axes** — a `NO_COLOR`
purist on a modern terminal renders emoji fine, while a glyph-poor terminal
(linux console, serial) often still has the 8 basic colors. The embedded
themes reflect that:

- `ThemeColor` — default: full Unicode glyphs, icons kept, basic-ANSI
  palette. lipgloss v2 dropped `AdaptiveColor`, and reading the background
  color back needs the stdin answers `WithInput(nil)` can never read (the
  §3.3 query scar) — but ANSI red/green/yellow already follow the user's
  light/dark scheme by construction, which is all "adaptive" meant.
- `ThemeGreyscale` — intensity without hue, full glyphs, icons kept.
- `ThemeMono` — no color at all; Unicode glyphs and icons kept. Answers
  "I hate colors," not "my terminal is from 1978."
- `ThemeASCII` — pure ASCII repertoire: `|`/`!` gutters,
  `OK`/`XX`/`!!` markers, `-\|/` spinner, `>` path separator, icons
  dropped; keeps the color palette. Answers the tofu-box case; natural
  base for `TERM=dumb`.

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
  via the context's step carrier — the inner becomes an ordinary step
  instead of booting a second renderer: named by the same caller derivation,
  no engine, no signal watcher, no replay, error returned untouched.
  Library-style shared targets can wrap defensively; the inner's options
  are *silently* ignored — the outer run owns presentation, and a defensive
  wrapper must be invisible, not chatty.
- **Deduped failure**: a step that failed reports the same error to every
  dependent.
- **Signals**: magetui never registers a SIGINT handler. Mage's generated
  mainfile already owns that signal — first ^C cancels the context it
  passed us and waits up to 5s for cleanup, second ^C force-exits — so
  magetui *observes*: `ctx.Err() == context.Canceled` on the root context
  is the interruption fact (rejected: a competing handler — three parties
  racing on one signal, duplicated semantics). SIGTERM is mage's blind
  spot, so `Target` watches it itself with a derived context (build-tagged;
  no-op off unix); after the first SIGTERM the watcher restores the default
  disposition, so a second kills outright — the user outranks the renderer,
  same rule as mage's second ^C.
- **Known wart — `kill` on the mage wrapper orphans the build.** Mage runs
  the compiled magefile as a child and forwards no signals to it; it only
  ignores SIGINT in its own process so the terminal's process-group
  delivery reaches the magefile (`RunCompiled`, mage v1.17.2). SIGTERM to
  the wrapper (or to `go tool mage`) therefore kills the wrapper alone and
  the magefile keeps running, OSC bar pulsing. Not fixable from a library:
  ^C works (the whole foreground group gets SIGINT), or signal the
  magefile binary itself.
- **Interrupted steps** are classified by error, not by clock: `⊘`
  ("interrupted") when the step's error is `context.Canceled` *and* the
  root context is canceled. A genuine failure landing after ^C keeps `✗`;
  a step canceling itself while the root is live is a bug in the step
  (rejected: everything-after-the-cancel — hides real failures that happen
  to land late). `⊘` steps are excluded from the failure replay — their
  cause is the user, there is nothing to diagnose — but the count line
  acknowledges them ("interrupted, 3 of 14 steps did not finish."), so the
  scrollback's last word is never silent about why the build stopped.
- **^C in TUI mode degrades to plain.** bubbletea's v2.0.7 event loop
  intercepts `InterruptMsg` before any `Update` — the model never sees it —
  and `Run` returns `ErrInterrupted` with the terminal restored (SIGTERM
  becomes an internal quit the same way). `Handle` notices the dead program
  and reroutes events to a plain renderer seeded with the tree's steps, so
  the stragglers stream as plain lines below the vanished live region until
  the engine drains, and the replay follows as usual. Mage's "cancelling
  mage targets..." stderr line lands harmlessly in scrollback instead of
  mid-live-region, and a second-^C force-exit always finds the terminal
  already cooked (rejected: keeping the TUI through the cleanup window —
  mage's log corrupts the live region and the force-exit races terminal
  restore; a silent tail — looks hung for up to 5s). The same path absorbs
  *any* premature program death: a broken TUI never eats the build.
- **Exit code**: an interrupted run comes back wrapped in an error carrying
  `ExitStatus()` — 130 for SIGINT, 143 for SIGTERM — which mage's machinery
  honors, the same `interface{ ExitStatus() int }` convention the
  subprocess errors use, without magetui importing mg. The wrapper keeps
  `Unwrap`, and the aggregate text still names any step that genuinely
  failed before the signal landed. The "errors returned untouched" rule
  guards real failures; the interruption aggregate is magetui's own to
  shape (rejected: plain `ctx.Err()` — scripts can't tell interruption from
  failure; re-raising the signal after teardown — fights mage's error path
  mid-`runTarget`).
- **Terminal restore** also runs on panic via the `Target` defer, and the
  OSC clear is deferred the moment the emitter exists. A build tool that
  leaves the terminal raw is unforgivable.

## 7. Testing

The event seam pays off:

- **Execution layer**: tested against the `mg.Deps` contract with no terminal —
  events in, assertions out (dedup, parallelism, error aggregation, panic
  capture, buffer bounding).
- **Renderers**: golden-file tests — scripted event sequences in, frame
  strings out, no PTY, no bubbletea. For the TUI that means probing the
  layout core directly: scenarios interleave events with `Frame(w, h, at)`
  and block probes at scripted instants (time is synthetic — `Tree` never
  reads a clock), expected frames as inline raw-string literals. Covers the
  degradation ladder tiers and cut order, root-child commit timing,
  rune-correct truncation, resize round-trips, status text lifecycle, the
  zero-size frame, themes (Phase 5) — plus an invariant sweep: every probe
  satisfies `rows ≤ height` and `runewidth ≤ width`, the two promises
  repaint arithmetic rests on.
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
