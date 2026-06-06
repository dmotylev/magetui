package magetui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dmotylev/magetui/internal/engine"
	"github.com/dmotylev/magetui/internal/events"
)

// Printf appends a formatted log line to the current step's scrolling tail
// — the replacement for stray fmt.Printf in a target body. Multi-line text
// is recorded line by line; a trailing newline is implied.
func Printf(ctx context.Context, format string, a ...any) {
	s := engine.StepFrom(ctx)
	text := fmt.Sprintf(format, a...)
	if s == nil {
		warnNoStep("Printf")
		fmt.Fprintln(os.Stderr, text)
		return
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		s.Output(events.Stdout, line)
	}
}

// Status replaces the transient status text on the step's own line — the
// buildx transfer-counter feel. Presentation only: not recorded in the
// output buffer, absent from the failure replay.
func Status(ctx context.Context, text string) {
	s := engine.StepFrom(ctx)
	if s == nil {
		warnNoStep("Status")
		fmt.Fprintln(os.Stderr, text)
		return
	}
	s.SetStatus(text)
}
