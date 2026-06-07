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
	}})

	want := `
──────────────────────────────────────────
✗ all ▸ overthink  9.8s  exit status 2
  $ overthink --harder
  | considering monads
  … 1,204 lines elided …
  ! decision paralysis

1 of 2 steps failed.
`
	if got := out.String(); got != want {
		t.Errorf("replay diverges:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestReplayFailures_PanicsGetTheStackAsItsOwnBlock(t *testing.T) {
	var out strings.Builder
	ReplayFailures(&out, 1, []Replay{{
		Path:     []string{"all", "dropTable"},
		Outcome:  events.OutcomePanicked,
		Err:      errors.New("panic: the intern had prod access"),
		Stack:    []byte("goroutine 7 [running]:\nmain.dropTable(...)\n\t/tmp/magefile.go:42\n"),
		Duration: 300 * time.Millisecond,
	}})

	got := out.String()
	if !strings.Contains(got, "‼ all ▸ dropTable  0.3s  panic: the intern had prod access") {
		t.Errorf("panic header missing:\n%s", got)
	}
	if !strings.Contains(got, "\n\n  goroutine 7 [running]:\n  main.dropTable(...)\n") {
		t.Errorf("stack must be its own indented block:\n%s", got)
	}
	if !strings.Contains(got, "1 of 1 steps failed.") {
		t.Errorf("summary missing:\n%s", got)
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
	}})

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
	ReplayFailures(&out, 5, nil)
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
