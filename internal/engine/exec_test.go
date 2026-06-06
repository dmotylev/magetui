package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

// TestMain doubles as the subprocess under test: when MAGETUI_EXEC_HELPER
// names a scenario, the test binary acts it out and exits instead of
// running tests. Keeps the suite free of shell commands — the windows CI
// job runs these too.
func TestMain(m *testing.M) {
	if scenario := os.Getenv("MAGETUI_EXEC_HELPER"); scenario != "" {
		helperMain(scenario, os.Args[1:])
	}
	os.Exit(m.Run())
}

func helperMain(scenario string, args []string) {
	switch scenario {
	case "brew":
		fmt.Println("grinding beans")
		fmt.Println("water at 93C, as the gods intended")
		fmt.Fprintln(os.Stderr, "warning: kettle left unsupervised")
	case "echo":
		fmt.Println(strings.Join(args, " "))
	case "fail":
		fmt.Fprintln(os.Stderr, "it worked on my machine")
		os.Exit(3)
	case "crlf":
		fmt.Print("tea\r\nbiscuits\r\n")
	case "noeol":
		fmt.Print("rude exit, no newline")
	case "stall":
		time.Sleep(time.Minute)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper scenario %q\n", scenario)
		os.Exit(2)
	}
	os.Exit(0)
}

// helperSpec returns an ExecSpec that re-runs this test binary as the
// named scenario; the env overlay is load-bearing — it is how the child
// learns its role, so every test exercises overlay delivery for free.
func helperSpec(scenario string, args ...string) ExecSpec {
	return ExecSpec{
		Env:  map[string]string{"MAGETUI_EXEC_HELPER": scenario},
		Name: os.Args[0],
		Args: args,
	}
}

// recorded returns the step's full recorded output, head and tail joined.
func recorded(t *testing.T, s *Step) []Line {
	t.Helper()
	head, elided, tail := s.Lines()
	if elided != 0 {
		t.Fatalf("unexpected elision: %d lines", elided)
	}
	return append(head, tail...)
}

func byOrigin(lines []Line, origin events.Origin) []string {
	var out []string
	for _, l := range lines {
		if l.Origin == origin {
			out = append(out, l.Text)
		}
	}
	return out
}

func TestExec_TagsOriginsAndEchoesTheCommandFirst(t *testing.T) {
	e, c := newTestEngine()
	root := e.NewRoot("root", "")

	if _, err := Exec(context.Background(), root, helperSpec("brew")); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	lines := recorded(t, root)
	if len(lines) == 0 || lines[0].Origin != events.Command {
		t.Fatalf("first recorded line must be the command echo, got %+v", lines)
	}
	if !strings.Contains(lines[0].Text, os.Args[0]) {
		t.Fatalf("command echo %q does not name the binary", lines[0].Text)
	}

	wantOut := []string{"grinding beans", "water at 93C, as the gods intended"}
	if got := byOrigin(lines, events.Stdout); !slices.Equal(got, wantOut) {
		t.Fatalf("stdout lines = %q, want %q", got, wantOut)
	}
	wantErr := []string{"warning: kettle left unsupervised"}
	if got := byOrigin(lines, events.Stderr); !slices.Equal(got, wantErr) {
		t.Fatalf("stderr lines = %q, want %q", got, wantErr)
	}

	var emitted int
	for _, ev := range c.events() {
		if _, ok := ev.(events.OutputLine); ok {
			emitted++
		}
	}
	if emitted != len(lines) {
		t.Fatalf("buffer holds %d lines but %d OutputLine events emitted", len(lines), emitted)
	}
}

func TestExec_CaptureReturnsStdoutAndStillRecordsStderr(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	spec := helperSpec("brew")
	spec.Capture = true
	out, err := Exec(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want := "grinding beans\nwater at 93C, as the gods intended"
	if out != want {
		t.Fatalf("captured = %q, want %q", out, want)
	}

	lines := recorded(t, root)
	if got := byOrigin(lines, events.Stdout); len(got) != 0 {
		t.Fatalf("captured stdout leaked into the buffer: %q", got)
	}
	if got := byOrigin(lines, events.Stderr); len(got) != 1 {
		t.Fatalf("stderr lines = %q, want exactly one", got)
	}
	if lines[0].Origin != events.Command {
		t.Fatal("command echo missing in capture mode; failure replay needs it")
	}
}

func TestExec_ExtractsTheExitCode(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	_, err := Exec(context.Background(), root, helperSpec("fail"))
	if err == nil {
		t.Fatal("exit 3 must surface as an error")
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("error %v does not unwrap to *exec.ExitError", err)
	}
	var es interface{ ExitStatus() int }
	if !errors.As(err, &es) {
		t.Fatalf("error %v lacks ExitStatus() int; mage cannot honor the code", err)
	}
	if got := es.ExitStatus(); got != 3 {
		t.Fatalf("ExitStatus() = %d, want 3", got)
	}
	if err.Error() != "exit status 3" {
		t.Fatalf("cause suffix = %q, want the familiar \"exit status 3\"", err.Error())
	}
}

func TestExec_TrimsCRLF(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	if _, err := Exec(context.Background(), root, helperSpec("crlf")); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want := []string{"tea", "biscuits"}
	if got := byOrigin(recorded(t, root), events.Stdout); !slices.Equal(got, want) {
		t.Fatalf("stdout lines = %q, want %q (carriage returns must not survive)", got, want)
	}
}

func TestExec_FlushesATrailingUnterminatedLine(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	if _, err := Exec(context.Background(), root, helperSpec("noeol")); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want := []string{"rude exit, no newline"}
	if got := byOrigin(recorded(t, root), events.Stdout); !slices.Equal(got, want) {
		t.Fatalf("stdout lines = %q, want %q", got, want)
	}
}

func TestExec_ExpandsEnvOverlayBeforeProcessEnv(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	t.Setenv("MAGETUI_TEST_DRINK", "instant coffee") // process env: the sad fallback
	t.Setenv("MAGETUI_TEST_SIZE", "mug")             // process env: no overlay shadows it

	spec := helperSpec("echo", "$MAGETUI_TEST_DRINK", "of", "$MAGETUI_TEST_SIZE")
	spec.Env["MAGETUI_TEST_DRINK"] = "yorkshire tea" // overlay wins
	_, err := Exec(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	lines := recorded(t, root)
	want := []string{"yorkshire tea of mug"}
	if got := byOrigin(lines, events.Stdout); !slices.Equal(got, want) {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(lines[0].Text, "yorkshire tea") {
		t.Fatalf("command echo %q should show expanded args", lines[0].Text)
	}
}

func TestExec_ContextCancelKillsTheChild(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Exec(ctx, root, helperSpec("stall")); err == nil {
		t.Fatal("a killed child must report an error")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("teardown took %v; the child outlived its context", elapsed)
	}
}

func TestExec_StartFailureIsAnErrorNotAPanic(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")

	_, err := Exec(context.Background(), root, ExecSpec{Name: "magetui-no-such-binary"})
	if err == nil {
		t.Fatal("running a nonexistent binary must fail")
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		t.Fatal("a process that never started has no exit status")
	}
}

func TestExec_NilStepRunsUnattributed(t *testing.T) {
	spec := helperSpec("echo", "shouting", "into", "the", "void")
	spec.Capture = true
	out, err := Exec(context.Background(), nil, spec)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if out != "shouting into the void" {
		t.Fatalf("captured = %q", out)
	}
}

func TestLineWriter_SplitsAcrossWriteBoundaries(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	w := &lineWriter{s: root, origin: events.Stdout}

	for _, chunk := range []string{"half a li", "ne\nand another\ntrail", "ing"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.flush()

	want := []string{"half a line", "and another", "trailing"}
	if got := byOrigin(recorded(t, root), events.Stdout); !slices.Equal(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestLineWriter_CapsARunawayLine(t *testing.T) {
	e, _ := newTestEngine()
	root := e.NewRoot("root", "")
	w := &lineWriter{s: root, origin: events.Stdout}

	if _, err := w.Write(bytes.Repeat([]byte("x"), maxLineBytes+5)); err != nil {
		t.Fatal(err)
	}
	w.flush()

	got := byOrigin(recorded(t, root), events.Stdout)
	if len(got) != 2 || len(got[0]) != maxLineBytes || got[1] != "xxxxx" {
		lens := make([]int, len(got))
		for i, l := range got {
			lens[i] = len(l)
		}
		t.Fatalf("line lengths = %v, want [%d 5]", lens, maxLineBytes)
	}
}
