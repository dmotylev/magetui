package magetui

import (
	"charm.land/lipgloss/v2"

	"github.com/dmotylev/magetui/internal/render"
)

// Theme carries every glyph and style magetui renders, in two flat
// groups: glyphs (strings, not runes — multi-codepoint glyphs and
// ligature pairs like "=>" must work) and a lipgloss palette. Selected
// per Target via WithTheme; the MAGETUI_THEME environment variable
// (color|greyscale|mono|ascii) overrides whatever the code asked for.
//
// Lifecycle glyphs within one theme should share one display width —
// the embedded themes do — for the grid's sake; the plain grid and the
// live tree pad the glyph column to the widest one either way, so a
// narrower spinner (ASCII's -\|/) is fine. Color capability is not a
// theme concern:
// the palette degrades automatically underneath every theme
// (truecolor → 256 → 16 → none; NO_COLOR forces the bottom), so "ASCII
// without color" is ThemeASCII plus NO_COLOR, by composition.
//
// Zero-value fields render as empty glyphs and unstyled text; an empty
// Spinner falls back to the default frames. Plain mode's origin gutters
// (`$`, `|`, `!`) are deliberately not themed — `grep '^!'` finding all
// stderr is a contract — while GutterOut/GutterErr style the TUI tail
// and the failure replay.
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
	Separator                    string   // the failure replay's horizontal rule
	Dash                         string   // cause separator on committed step lines
	Icons                        bool     // render step icons; glyph-poor themes drop them

	// Palette.
	Name, Duration, Status                                            lipgloss.Style
	TailText, TailErr, TailCmd                                        lipgloss.Style
	Path                                                              lipgloss.Style
	SpinnerStyle, OKStyle, FailureStyle, PanicStyle, InterruptedStyle lipgloss.Style
	StackStyle                                                        lipgloss.Style
}

// The embedded themes are design intents, not capability-matrix cells
// (DESIGN.md §5). The conversions double as the compile-time check that
// this struct and its internal twin stay field-for-field identical.
var (
	// ThemeColor is the default: full Unicode glyphs, icons kept, and a
	// basic-ANSI palette — the terminal's own colors follow the user's
	// light/dark scheme by construction.
	ThemeColor = Theme(render.ThemeColor)

	// ThemeGreyscale is intensity without hue: full glyphs and icons
	// over greys and weight.
	ThemeGreyscale = Theme(render.ThemeGreyscale)

	// ThemeMono uses no color at all; Unicode glyphs and icons stay.
	// Answers "I hate colors", not "my terminal is from 1978".
	ThemeMono = Theme(render.ThemeMono)

	// ThemeASCII is the pure-ASCII repertoire for glyph-poor terminals:
	// OK/XX/!! markers, -\|/ spinner, icons dropped. It keeps the color
	// palette — a linux console still has the basic 8.
	ThemeASCII = Theme(render.ThemeASCII)
)

// WithTheme selects the theme for one Target lifecycle. MAGETUI_THEME
// still wins: the person running the build outranks the person who
// wrote it.
func WithTheme(t Theme) TargetOption {
	return func(o *targetOptions) { o.theme = t }
}
