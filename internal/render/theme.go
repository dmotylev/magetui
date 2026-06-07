package render

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/dmotylev/magetui/internal/events"
)

// Theme carries two flat groups — glyphs (strings, not runes: multi-
// codepoint glyphs and ligature pairs must work) and a lipgloss palette
// (DESIGN.md §5). The public magetui.Theme mirrors this struct field for
// field; the conversions in the root package are the compile-time check
// that the twins stay identical.
//
// Lifecycle glyphs within one theme should share one display width —
// the embedded themes do — for the grid's sake; plain's first column
// and the live tree pad to the widest glyph either way, so a narrower
// spinner (ASCII's -\|/) is fine. Color capability is not a theme
// concern:
// the palette degrades automatically underneath every theme
// (truecolor → 256 → 16 → none, NO_COLOR forces the bottom).
type Theme struct {
	// Glyphs.
	Spinner                      []string // animation frames for running steps
	Start                        string   // plain mode: started and status lines
	OK, Fail, Panic, Interrupted string   // final outcome glyphs
	GutterOut, GutterErr         string   // output-line origin markers (stdout, stderr)
	GutterCmd                    string   // the command echo
	StackGutter                  string   // the trimmed panic-stack block
	PathSep                      string   // between step names in paths
	Ellipsis                     string   // truncation and elision marker
	Separator                    string   // the replay's horizontal rule
	Dash                         string   // cause separator on committed step lines
	Icons                        bool     // render step icons; glyph-poor themes drop them

	// Palette.
	Name, Duration, Status                                            lipgloss.Style
	TailText, TailErr, TailCmd                                        lipgloss.Style
	Path                                                              lipgloss.Style
	SpinnerStyle, OKStyle, FailureStyle, PanicStyle, InterruptedStyle lipgloss.Style
	StackStyle                                                        lipgloss.Style
}

// The themes are design intents, not capability-matrix cells (DESIGN.md
// §5), so each is written out whole. Two principles keep them at four:
// glyph loudness is inversely proportional to the color budget, and the
// color axis degrades automatically underneath every theme.

// ThemeColor is the default: full Unicode glyphs, icons kept, and a
// basic-ANSI palette. The terminal's own palette is what "adaptive"
// means here — the renderer cannot read the background color back
// (WithInput(nil), §3.3), but ANSI red/green/yellow already follow the
// user's light/dark scheme by construction.
var ThemeColor = Theme{
	Spinner:     []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Start:       "○",
	OK:          "✓",
	Fail:        "✗",
	Panic:       "‼",
	Interrupted: "⊘",
	GutterOut:   "│",
	GutterErr:   "┃",
	GutterCmd:   "$",
	StackGutter: "┆",
	PathSep:     " ▸ ",
	Ellipsis:    "…",
	Separator:   strings.Repeat("─", 42),
	Dash:        "—",
	Icons:       true,

	Duration:         lipgloss.NewStyle().Faint(true),
	Status:           lipgloss.NewStyle().Foreground(lipgloss.Cyan),
	TailErr:          lipgloss.NewStyle().Foreground(lipgloss.Red),
	TailCmd:          lipgloss.NewStyle().Bold(true),
	Path:             lipgloss.NewStyle().Bold(true),
	SpinnerStyle:     lipgloss.NewStyle().Foreground(lipgloss.Cyan),
	OKStyle:          lipgloss.NewStyle().Foreground(lipgloss.Green),
	FailureStyle:     lipgloss.NewStyle().Foreground(lipgloss.Red),
	PanicStyle:       lipgloss.NewStyle().Foreground(lipgloss.BrightRed).Bold(true),
	InterruptedStyle: lipgloss.NewStyle().Foreground(lipgloss.Yellow),
	StackStyle:       lipgloss.NewStyle().Faint(true),
}

// ThemeGreyscale is intensity without hue: ThemeColor's glyphs over
// ANSI-256 greys and weight. Stderr pops by brightness, not by red.
var ThemeGreyscale = Theme{
	Spinner:     ThemeColor.Spinner,
	Start:       ThemeColor.Start,
	OK:          ThemeColor.OK,
	Fail:        ThemeColor.Fail,
	Panic:       ThemeColor.Panic,
	Interrupted: ThemeColor.Interrupted,
	GutterOut:   ThemeColor.GutterOut,
	GutterErr:   ThemeColor.GutterErr,
	GutterCmd:   ThemeColor.GutterCmd,
	StackGutter: ThemeColor.StackGutter,
	PathSep:     ThemeColor.PathSep,
	Ellipsis:    ThemeColor.Ellipsis,
	Separator:   ThemeColor.Separator,
	Dash:        ThemeColor.Dash,
	Icons:       true,

	Duration:         lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
	Status:           lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
	TailErr:          lipgloss.NewStyle().Bold(true),
	TailCmd:          lipgloss.NewStyle().Bold(true),
	Path:             lipgloss.NewStyle().Bold(true),
	SpinnerStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
	OKStyle:          lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
	FailureStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true),
	PanicStyle:       lipgloss.NewStyle().Bold(true).Reverse(true),
	InterruptedStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	StackStyle:       lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
}

// ThemeMono uses no color at all — weight and the glyph repertoire do
// all the work, which is why the stderr gutter gets louder (║ + bold
// where color themes whisper ┃). Answers "I hate colors", not "my
// terminal is from 1978".
var ThemeMono = Theme{
	Spinner:     ThemeColor.Spinner,
	Start:       ThemeColor.Start,
	OK:          ThemeColor.OK,
	Fail:        ThemeColor.Fail,
	Panic:       ThemeColor.Panic,
	Interrupted: ThemeColor.Interrupted,
	GutterOut:   "│",
	GutterErr:   "║",
	GutterCmd:   ThemeColor.GutterCmd,
	StackGutter: ThemeColor.StackGutter,
	PathSep:     ThemeColor.PathSep,
	Ellipsis:    ThemeColor.Ellipsis,
	Separator:   ThemeColor.Separator,
	Dash:        ThemeColor.Dash,
	Icons:       true,

	Duration:     lipgloss.NewStyle().Faint(true),
	TailErr:      lipgloss.NewStyle().Bold(true),
	TailCmd:      lipgloss.NewStyle().Bold(true),
	Path:         lipgloss.NewStyle().Bold(true),
	FailureStyle: lipgloss.NewStyle().Bold(true),
	PanicStyle:   lipgloss.NewStyle().Bold(true).Reverse(true),
	StackStyle:   lipgloss.NewStyle().Faint(true),
}

// ThemeASCII is the pure-ASCII repertoire for the tofu-box case (linux
// console, serial, TERM=dumb) — which often still has the 8 basic
// colors, so it keeps ThemeColor's palette. Icons are dropped. "ASCII
// without color" is ThemeASCII + NO_COLOR by composition; no fifth
// theme.
var ThemeASCII = Theme{
	Spinner:     []string{"-", "\\", "|", "/"},
	Start:       "..",
	OK:          "OK",
	Fail:        "XX",
	Panic:       "!!",
	Interrupted: "--",
	GutterOut:   "|",
	GutterErr:   "!",
	GutterCmd:   "$",
	StackGutter: ":",
	PathSep:     " > ",
	Ellipsis:    "...",
	Separator:   strings.Repeat("-", 42),
	Dash:        "--",
	Icons:       false,

	Duration:         ThemeColor.Duration,
	Status:           ThemeColor.Status,
	TailErr:          ThemeColor.TailErr,
	TailCmd:          ThemeColor.TailCmd,
	Path:             ThemeColor.Path,
	SpinnerStyle:     ThemeColor.SpinnerStyle,
	OKStyle:          ThemeColor.OKStyle,
	FailureStyle:     ThemeColor.FailureStyle,
	PanicStyle:       ThemeColor.PanicStyle,
	InterruptedStyle: ThemeColor.InterruptedStyle,
	StackStyle:       ThemeColor.StackStyle,
}

// glyph returns the final glyph for an outcome.
func (t Theme) glyph(o events.Outcome) string {
	switch o {
	case events.OutcomeFailed:
		return t.Fail
	case events.OutcomePanicked:
		return t.Panic
	case events.OutcomeInterrupted:
		return t.Interrupted
	case events.OutcomeOK:
	}
	return t.OK
}

// glyphStyle returns the style riding an outcome's glyph and cause.
func (t Theme) glyphStyle(o events.Outcome) lipgloss.Style {
	switch o {
	case events.OutcomeFailed:
		return t.FailureStyle
	case events.OutcomePanicked:
		return t.PanicStyle
	case events.OutcomeInterrupted:
		return t.InterruptedStyle
	case events.OutcomeOK:
	}
	return t.OKStyle
}

// gutter returns the origin marker for output lines — the vocabulary of
// the TUI tail and the failure replay. Plain's grid keeps its own
// hardcoded gutters: `grep '^!'` is a §4.6 contract, not a fashion.
func (t Theme) gutter(o events.Origin) string {
	switch o {
	case events.Stderr:
		return t.GutterErr
	case events.Command:
		return t.GutterCmd
	case events.Stdout:
	}
	return t.GutterOut
}

// gutterStyle returns the style for an output line of the given origin.
func (t Theme) gutterStyle(o events.Origin) lipgloss.Style {
	switch o {
	case events.Stderr:
		return t.TailErr
	case events.Command:
		return t.TailCmd
	case events.Stdout:
	}
	return t.TailText
}

// glyphWidth is the display width of the widest lifecycle glyph or
// spinner frame — the column plain's grid and the live tree pad to.
// Never below 1: plain's own gutters occupy the same column.
func (t Theme) glyphWidth() int {
	w := 1
	for _, g := range t.Spinner {
		w = max(w, utf8.RuneCountInString(g))
	}
	for _, g := range []string{t.Start, t.OK, t.Fail, t.Panic, t.Interrupted} {
		w = max(w, utf8.RuneCountInString(g))
	}
	return w
}

// finishSuffix renders the duration and, for bad outcomes, the cause:
// "  9.8s  exit status 2". Shared by plain finish lines and the replay
// headers; the tree renders its own dash-separated form.
func (t Theme) finishSuffix(o events.Outcome, err error, d time.Duration) string {
	suffix := "  " + styled(t.Duration, fmt.Sprintf("%.1fs", d.Seconds()))
	switch o {
	case events.OutcomeFailed, events.OutcomePanicked:
		if err != nil {
			suffix += "  " + styled(t.glyphStyle(o), oneLine(err.Error()))
		}
	case events.OutcomeInterrupted:
		suffix += "  " + styled(t.InterruptedStyle, "interrupted")
	case events.OutcomeOK:
	}
	return suffix
}

// oneLine flattens a cause for line-oriented surfaces: errors.Join
// aggregates with newlines, which would break the plain grid, the live
// region's row arithmetic, and lipgloss's multi-line padding.
func oneLine(s string) string {
	return strings.ReplaceAll(s, "\n", "; ")
}

// styled renders s in st with tab conversion off: output and stack
// lines pass through byte-faithful, and a zero style is the identity —
// unstyled themes render exactly what Phase 4 rendered.
func styled(st lipgloss.Style, s string) string {
	return st.TabWidth(lipgloss.NoTabConversion).Render(s)
}

// padTo right-pads s to w display columns (rune count, the project-wide
// measure). Strings already that wide pass through.
func padTo(s string, w int) string {
	if n := w - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
