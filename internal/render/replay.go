package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

// Replay is the self-contained record of one failed step: everything the
// exit detail section needs, with no renderer state behind it. Target
// assembles these from the engine's registry and buffers (DESIGN.md §3.3,
// §4.4); the replay renders identically in every mode.
type Replay struct {
	Path     []string // root-anchored name chain
	Outcome  events.Outcome
	Err      error
	Stack    []byte
	Duration time.Duration
	Head     []events.Line
	Elided   int
	Tail     []events.Line
}

// ReplayFailures writes the exit detail section: the complete captured
// output of directly-failed steps, then the failure count. One shared
// implementation serves both modes; in TUI mode Target calls it after the
// program has quit and restored the terminal — plain prose below the
// vanished live region, so a crash here can never leave the cursor
// hidden. total is the number of steps the run started, excluding the
// root; interrupted counts the steps the user's signal stopped — they get
// no replay blocks (their cause is the user, there is nothing to
// diagnose, DESIGN.md §6) but the count line acknowledges them, so the
// scrollback's last word is never silent about why the build stopped.
// With no failures and no interruption it writes nothing. Write errors
// are dropped: a broken progress pipe must never fail a build.
func ReplayFailures(w io.Writer, total, interrupted int, failed []Replay, theme Theme) {
	if len(failed) == 0 && interrupted == 0 {
		return
	}
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	printf("\n%s\n", styled(theme.TailText, theme.Separator))
	count := 0
	for _, r := range failed {
		// The root is not counted among its own steps.
		if len(r.Path) > 1 {
			count++
		}
		printf("%s %s%s\n",
			styled(theme.glyphStyle(r.Outcome), theme.glyph(r.Outcome)),
			styled(theme.Path, strings.Join(r.Path, theme.PathSep)),
			theme.finishSuffix(r.Outcome, r.Err, r.Duration))
		for _, ln := range r.Head {
			printf("  %s\n", styled(theme.gutterStyle(ln.Origin), theme.gutter(ln.Origin)+" "+ln.Text))
		}
		if r.Elided > 0 {
			printf("  %s\n", styled(theme.TailText, fmt.Sprintf("%s %s lines elided %s", theme.Ellipsis, comma(r.Elided), theme.Ellipsis)))
		}
		for _, ln := range r.Tail {
			printf("  %s\n", styled(theme.gutterStyle(ln.Origin), theme.gutter(ln.Origin)+" "+ln.Text))
		}
		// Panics get the stack as a separate styled block (DESIGN.md §4.5):
		// first the frames trimmed to the developer's own code behind the
		// stack gutter, then the full capture, dimmed, for when the trim
		// guessed wrong.
		if r.Outcome == events.OutcomePanicked && len(r.Stack) > 0 {
			if trimmed := trimStack(r.Stack); len(trimmed) > 0 {
				printf("\n")
				for _, line := range trimmed {
					printf("  %s %s\n", styled(theme.PanicStyle, theme.StackGutter), line)
				}
			}
			printf("\n")
			for line := range strings.SplitSeq(strings.TrimRight(string(r.Stack), "\n"), "\n") {
				printf("  %s\n", styled(theme.StackStyle, line))
			}
		}
		printf("\n")
	}
	if count > 0 {
		printf("%s\n", styled(theme.FailureStyle, fmt.Sprintf("%d of %d steps failed.", count, total)))
	}
	if interrupted > 0 {
		printf("%s\n", styled(theme.InterruptedStyle, fmt.Sprintf("interrupted, %d of %d steps did not finish.", interrupted, total)))
	}
}

// comma renders n with thousands separators: the elision marker says
// "1,204 lines elided", not "1204".
func comma(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
