package magetui_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/dmotylev/magetui"
	"github.com/dmotylev/magetui/internal/engine"
	"github.com/dmotylev/magetui/internal/events"
)

// TestMain doubles as the subprocess under test, same pattern as the
// engine package's exec tests.
func TestMain(m *testing.M) {
	if scenario := os.Getenv("MAGETUI_EXEC_HELPER"); scenario != "" {
		switch scenario {
		case "echo":
			fmt.Println(strings.Join(os.Args[1:], " "))
		case "fail":
			fmt.Fprintln(os.Stderr, "computer says no")
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// inStep runs fn inside a step the way Target will wire it in Phase 3,
// and returns that step's recorded lines.
func inStep(t *testing.T, fn func(ctx context.Context) error) []engine.Line {
	t.Helper()
	e := engine.New(nil)
	root := e.NewRoot("root", "")
	var lines []engine.Line
	err := e.RunStep(context.Background(), root, "step", "", func(ctx context.Context) error {
		err := fn(ctx)
		head, _, tail := engine.StepFrom(ctx).Lines()
		lines = append(head, tail...)
		return err
	})
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	return lines
}

func TestRunWith_AttributesOutputToTheCurrentStep(t *testing.T) {
	lines := inStep(t, func(ctx context.Context) error {
		return magetui.RunWith(ctx,
			map[string]string{"MAGETUI_EXEC_HELPER": "echo"},
			os.Args[0], "carrier", "pigeon", "deployed")
	})
	if len(lines) != 2 {
		t.Fatalf("recorded %d lines, want command echo + stdout: %+v", len(lines), lines)
	}
	if lines[0].Origin != events.Command {
		t.Fatalf("first line origin = %v, want the command echo", lines[0].Origin)
	}
	if lines[1].Origin != events.Stdout || lines[1].Text != "carrier pigeon deployed" {
		t.Fatalf("stdout line = %+v", lines[1])
	}
}

func TestRun_UsesTheProcessEnvironment(t *testing.T) {
	t.Setenv("MAGETUI_EXEC_HELPER", "echo")
	lines := inStep(t, func(ctx context.Context) error {
		return magetui.Run(ctx, os.Args[0], "no", "overlay", "needed")
	})
	if got := lines[len(lines)-1].Text; got != "no overlay needed" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestOutput_ReturnsCapturedStdout(t *testing.T) {
	t.Setenv("MAGETUI_EXEC_HELPER", "echo")
	var out string
	inStep(t, func(ctx context.Context) (err error) {
		out, err = magetui.Output(ctx, os.Args[0], "tip", "your", "barista")
		return err
	})
	if out != "tip your barista" {
		t.Fatalf("Output = %q (trailing newline must be trimmed)", out)
	}
}

func TestRun_SurfacesTheExitCode(t *testing.T) {
	t.Setenv("MAGETUI_EXEC_HELPER", "fail")
	var runErr error
	inStep(t, func(ctx context.Context) error {
		runErr = magetui.Run(ctx, os.Args[0])
		return nil // keep the step green; we inspect runErr ourselves
	})
	if runErr == nil {
		t.Fatal("exit 3 must surface as an error")
	}
	var es interface{ ExitStatus() int }
	if !errors.As(runErr, &es) || es.ExitStatus() != 3 {
		t.Fatalf("error %v should carry ExitStatus() == 3", runErr)
	}
}

// The only test that calls Output without a step: the no-step warning is
// once per primitive per process, so each primitive's misuse gets exactly
// one test that may observe it.
func TestOutput_WithoutAStepDegradesWithAWarning(t *testing.T) {
	t.Setenv("MAGETUI_EXEC_HELPER", "echo")

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	out, err := magetui.Output(context.Background(), os.Args[0], "still", "works")
	if cerr := w.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	os.Stderr = old

	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if out != "still works" {
		t.Fatalf("Output = %q; degradation must not lose the result", out)
	}
	warning, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(warning), "magetui: Output called with no step") {
		t.Fatalf("expected a one-time misuse warning, got %q", warning)
	}
}
