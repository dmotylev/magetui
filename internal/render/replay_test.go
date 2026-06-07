package render

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

func TestReplayFailures_FullPathAndElision(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 2, []Replay{{
		Path:     []string{"all", "overthink"},
		Outcome:  events.OutcomeFailed,
		Err:      errors.New("exit status 2"),
		Duration: 9800 * time.Millisecond,
		Head: []events.Line{
			{Origin: events.Command, Text: "overthink --harder"},
			{Origin: events.Stdout, Text: "considering monads"},
		},
		Elided: 1204,
		Tail: []events.Line{
			{Origin: events.Stderr, Text: "decision paralysis"},
		},
	}}, unstyled(ThemeColor))

	// The replay speaks the theme's gutter vocabulary — │/┃, identical to
	// the TUI tail (DESIGN.md §4.4) — not plain's grid gutters.
	want := `
──────────────────────────────────────────
✗ all ▸ overthink  9.8s  exit status 2
  $ overthink --harder
  │ considering monads
  … 1,204 lines elided …
  ┃ decision paralysis

1 of 2 steps failed.
`
	if got := out.String(); got != want {
		t.Errorf("replay diverges:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestReplayFailures_PanicsGetTheStackAsItsOwnBlock(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 1, []Replay{{
		Path:    []string{"all", "dropTable"},
		Outcome: events.OutcomePanicked,
		Err:     errors.New("panic: the intern had prod access"),
		Stack: []byte("goroutine 7 [running]:\n" +
			"runtime/debug.Stack()\n\t/usr/local/go/src/runtime/debug/stack.go:26 +0x64\n" +
			"github.com/dmotylev/magetui/internal/engine.(*Engine).run.func1()\n\t/src/magetui/internal/engine/engine.go:190 +0x9c\n" +
			"panic({0x102f3c1e0?, 0x14000114018?})\n\t/usr/local/go/src/runtime/panic.go:783 +0x124\n" +
			"main.dropTable(...)\n\t/tmp/magefile.go:42\n"),
		Duration: 300 * time.Millisecond,
	}}, unstyled(ThemeColor))

	got := out.String()
	if !strings.Contains(got, "‼ all ▸ dropTable  0.3s  panic: the intern had prod access") {
		t.Errorf("panic header missing:\n%s", got)
	}
	// First the trimmed block behind the stack gutter: the magefile frame,
	// not the capture machinery.
	if !strings.Contains(got, "\n\n  ┆ main.dropTable(...)\n  ┆ \t/tmp/magefile.go:42\n") {
		t.Errorf("trimmed stack block missing or misframed:\n%s", got)
	}
	// Then the full capture, indented, byte-faithful — tabs intact.
	if !strings.Contains(got, "\n\n  goroutine 7 [running]:\n  runtime/debug.Stack()\n  \t/usr/local/go/src/runtime/debug/stack.go:26 +0x64\n") {
		t.Errorf("full stack must follow as its own indented block:\n%s", got)
	}
	if !strings.Contains(got, "1 of 1 steps failed.") {
		t.Errorf("summary missing:\n%s", got)
	}
}

func TestReplayFailures_UnrecognizableStackSkipsTheTrimmedBlock(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 1, []Replay{{
		Path:     []string{"all", "weird"},
		Outcome:  events.OutcomePanicked,
		Err:      errors.New("panic: chaos"),
		Stack:    []byte("goroutine 9 [running]:\nruntime.gopanic(...)\n\t/usr/local/go/src/runtime/panic.go:783\n"),
		Duration: time.Second,
	}}, unstyled(ThemeColor))

	got := out.String()
	if strings.Contains(got, "┆") {
		t.Errorf("nothing user-attributable to trim to, yet a trimmed block rendered:\n%s", got)
	}
	if !strings.Contains(got, "  runtime.gopanic(...)") {
		t.Errorf("the full capture is the fallback and must still print:\n%s", got)
	}
}

func TestReplayFailures_RootOnlyFailureSkipsTheCountLine(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 0, []Replay{{
		Path:     []string{"all"},
		Outcome:  events.OutcomeFailed,
		Err:      errors.New("forgot to plug it in"),
		Duration: time.Second,
		Head:     []events.Line{{Origin: events.Stderr, Text: "is it on?"}},
	}}, unstyled(ThemeColor))

	got := out.String()
	if !strings.Contains(got, "✗ all  1.0s  forgot to plug it in") {
		t.Errorf("root replay block missing:\n%s", got)
	}
	if strings.Contains(got, "steps failed") {
		t.Errorf("the root is not counted among its own steps:\n%s", got)
	}
}

func TestReplayFailures_NoFailuresMeansSilence(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 5, nil, unstyled(ThemeColor))
	if out.String() != "" {
		t.Fatalf("a green build earned silence, got:\n%s", out.String())
	}
}

func TestComma_SeparatesThousands(t *testing.T) {
	for n, want := range map[int]string{7: "7", 999: "999", 1204: "1,204", 1048576: "1,048,576"} {
		if got := comma(n); got != want {
			t.Errorf("comma(%d) = %q, want %q", n, got, want)
		}
	}
}
