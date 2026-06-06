package magetui_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/dmotylev/magetui"
)

// Helpers named like magefile targets, so callerName has something honest
// to derive the root step from.

func deployCarrierPigeons(out io.Writer, fn func(context.Context) error) error {
	return magetui.Target(context.Background(), fn, magetui.WithOutput(out))
}

func nightlyBuild(out io.Writer, fn func(context.Context) error) error {
	return magetui.Target(context.Background(), fn, magetui.WithOutput(out))
}

func TestTarget_NamesTheRootAfterTheCallingTarget(t *testing.T) {
	var out bytes.Buffer
	if err := deployCarrierPigeons(&out, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "○ deployCarrierPigeons  started") {
		t.Errorf("root step not named after the caller:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "✓ deployCarrierPigeons") {
		t.Errorf("root step never finished green:\n%s", out.String())
	}
}

func TestTarget_ReturnsTheErrorUntouched(t *testing.T) {
	errGravity := errors.New("gravity still on")
	err := deployCarrierPigeons(io.Discard, func(context.Context) error { return errGravity })
	if !errors.Is(err, errGravity) {
		t.Fatalf("Target rewrote the error: %v", err)
	}
}

func TestTarget_ExitStatusSurvivesToTheCaller(t *testing.T) {
	t.Setenv("MAGETUI_EXEC_HELPER", "fail")
	err := deployCarrierPigeons(io.Discard, func(ctx context.Context) error {
		return magetui.Run(ctx, os.Args[0])
	})
	var es interface{ ExitStatus() int }
	if !errors.As(err, &es) || es.ExitStatus() != 3 {
		t.Fatalf("exit status lost on the way to mage: %v", err)
	}
}

func TestTarget_ReplaysTheOutputOfFailedStepsOnly(t *testing.T) {
	t.Setenv("MAGETUI_EXEC_HELPER", "fail")
	var out bytes.Buffer
	innocent := func(ctx context.Context) error {
		magetui.Printf(ctx, "minding my own business")
		return nil
	}
	sacrificeIntern := func(ctx context.Context) error {
		magetui.Printf(ctx, "fetching coffee")
		return magetui.Run(ctx, os.Args[0])
	}
	err := nightlyBuild(&out, func(ctx context.Context) error {
		magetui.Deps(ctx, innocent, sacrificeIntern)
		return nil
	})
	if err == nil {
		t.Fatal("the intern's exit status vanished")
	}

	got := out.String()
	if !strings.Contains(got, "──────") {
		t.Fatalf("no replay section:\n%s", got)
	}
	replay := got[strings.Index(got, "──────"):]
	for _, want := range []string{
		"nightlyBuild ▸ ", // replay paths include the root
		"| fetching coffee",
		"! computer says no",
		"exit status 3",
		"1 of 2 steps failed.", // the dep, not its root casualty
	} {
		if !strings.Contains(replay, want) {
			t.Errorf("replay lacks %q:\n%s", want, replay)
		}
	}
	if strings.Contains(replay, "minding my own business") {
		t.Errorf("replay leaked a green step's output:\n%s", replay)
	}
}

func TestTarget_PanickingStepRendersDistinctlyWithAStack(t *testing.T) {
	var out bytes.Buffer
	dropTable := func(context.Context) error {
		panic("the intern had prod access")
	}
	err := nightlyBuild(&out, func(ctx context.Context) error {
		magetui.Deps(ctx, dropTable)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "the intern had prod access") {
		t.Fatalf("panic should surface as an error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "‼") {
		t.Errorf("panic glyph missing; a panic is not a mere failure:\n%s", got)
	}
	if !strings.Contains(got, "goroutine") {
		t.Errorf("replay lacks the stack trace:\n%s", got)
	}
}
