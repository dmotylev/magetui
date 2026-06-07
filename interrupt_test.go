package magetui_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dmotylev/magetui"
)

// procureDoves wraps defensively, the way a shared library target would —
// runnable bare, and as somebody's dependency inside their Target.
func procureDoves(ctx context.Context, inner ...magetui.TargetOption) error {
	return magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Printf(ctx, "doves acquired")
		return nil
	}, inner...)
}

func TestTarget_NestedTargetBecomesAnOrdinaryStep(t *testing.T) {
	var out, hijack bytes.Buffer
	err := deployCarrierPigeons(&out, func(ctx context.Context) error {
		// The inner Target's options must be silently ignored — the outer
		// run owns presentation.
		return procureDoves(ctx, magetui.WithOutput(&hijack))
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "✓ procureDoves") {
		t.Errorf("nested Target did not render as a step of the outer run:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "| procureDoves") || !strings.Contains(out.String(), "doves acquired") {
		t.Errorf("inner step's output not attributed to it:\n%s", out.String())
	}
	if hijack.Len() != 0 {
		t.Errorf("inner WithOutput was honored, want silently ignored: %q", hijack.String())
	}
}

func TestTarget_NestedTargetReturnsTheErrorUntouched(t *testing.T) {
	errStuck := errors.New("dove union on strike")
	err := deployCarrierPigeons(bytes.NewBuffer(nil), func(ctx context.Context) error {
		return magetui.Target(ctx, func(context.Context) error { return errStuck })
	})
	if !errors.Is(err, errStuck) {
		t.Fatalf("nested Target rewrote the error: %v", err)
	}
}

func TestTarget_InterruptedRunCarriesConventionalExitCode(t *testing.T) {
	// Mage's mainfile cancels the target context on the first ^C; Target
	// observes and shapes the aggregate into the conventional 130.
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	err := magetui.Target(ctx, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}, magetui.WithOutput(&out))

	var ec interface{ ExitStatus() int }
	if !errors.As(err, &ec) {
		t.Fatalf("err = %v, want an ExitStatus carrier", err)
	}
	if got := ec.ExitStatus(); got != 130 {
		t.Errorf("ExitStatus() = %d, want 130 (128+SIGINT)", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the cause did not survive the wrapping: %v", err)
	}
	if !strings.Contains(out.String(), "⊘") {
		t.Errorf("no ⊘ in the output:\n%s", out.String())
	}
}

func TestTarget_InterruptedDepsAcknowledgedInTheReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	err := magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx, func(ctx context.Context) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		})
		return nil
	}, magetui.WithOutput(&out))
	if err == nil {
		t.Fatal("err = nil, want the interrupted aggregate")
	}
	if !strings.Contains(out.String(), "interrupted, 1 of 1 steps did not finish.") {
		t.Errorf("the replay's last word is silent about the interruption:\n%s", out.String())
	}
	// And no replay block for the ⊘ step: its cause is the user.
	if strings.Contains(out.String(), "context canceled\n  ") {
		t.Errorf("an interrupted step got a replay block:\n%s", out.String())
	}
}

func TestTarget_RealFailureDuringShutdownStillReplays(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	err := magetui.Target(ctx, func(ctx context.Context) error {
		magetui.Deps(ctx,
			func(ctx context.Context) error {
				cancel()
				<-ctx.Done()
				return ctx.Err()
			},
			func(ctx context.Context) error {
				<-ctx.Done()
				return errors.New("the compiler had opinions")
			},
		)
		return nil
	}, magetui.WithOutput(&out))

	var ec interface{ ExitStatus() int }
	if !errors.As(err, &ec) || ec.ExitStatus() != 130 {
		t.Fatalf("err = %v, want exit status 130 — the run was interrupted", err)
	}
	got := out.String()
	if !strings.Contains(got, "the compiler had opinions") {
		t.Errorf("the genuine failure vanished from the replay:\n%s", got)
	}
	if !strings.Contains(got, "1 of 2 steps failed.") || !strings.Contains(got, "interrupted, 1 of 2 steps did not finish.") {
		t.Errorf("count lines missing:\n%s", got)
	}
}
