package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dmotylev/magetui/internal/events"
)

const (
	oscIndeterminate = "\x1b]9;4;3\x07"
	oscClear         = "\x1b]9;4;0\x07"
)

func oscPct(st, pct int) string { return fmt.Sprintf("\x1b]9;4;%d;%d\x07", st, pct) }

func TestOSC_IndeterminateUntilTheFirstFinish(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, true)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "brew"})
	o.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "sip"})
	o.Handle(events.OutputLine{ID: 2, Origin: events.Stdout, Text: "scalding"})
	if got := out.String(); got != oscIndeterminate {
		t.Errorf("start burst wrote %q, want one indeterminate sequence", got)
	}
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK})
	if got := out.String(); got != oscIndeterminate+oscPct(1, 50) {
		t.Errorf("first finish wrote %q, want determinate 50%%", got)
	}
}

func TestOSC_DenominatorGrowthSlowsTheBarNeverLies(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, true)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "plan"})
	o.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "execute"})
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK})
	o.Handle(events.StepStarted{ID: 4, Parent: 3, Name: "scope creep"})
	want := oscIndeterminate + oscPct(1, 50) + oscPct(1, 33)
	if got := out.String(); got != want {
		t.Errorf("wrote %q, want %q — the discovered step lowers the percent", got, want)
	}
}

func TestOSC_FirstRealFailureTintsAndStays(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, true)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "vet"})
	o.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "test"})
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeFailed})
	o.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeOK})
	want := oscIndeterminate + oscPct(2, 50) + oscPct(2, 100)
	if got := out.String(); got != want {
		t.Errorf("wrote %q, want %q — the error state survives later green", got, want)
	}
}

func TestOSC_InterruptionIsNotAnError(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, true)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "linger"})
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeInterrupted})
	want := oscIndeterminate + oscPct(1, 100)
	if got := out.String(); got != want {
		t.Errorf("wrote %q, want %q — red is reserved for builds that broke themselves", got, want)
	}
}

func TestOSC_RootFinishEmitsNothingAndCloseClears(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, true)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "vet"})
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK})
	o.Handle(events.StepFinished{ID: 1, Outcome: events.OutcomeOK})
	want := oscIndeterminate + oscPct(1, 100)
	if got := out.String(); got != want {
		t.Errorf("root finish wrote extra: %q, want %q", got, want)
	}
	if err := o.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := out.String(); got != want+oscClear {
		t.Errorf("Close wrote %q, want the clear appended", got)
	}
}

func TestOSC_RepeatedStateIsWrittenOnce(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, false)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	for i := events.StepID(2); i < 12; i++ {
		o.Handle(events.StepStarted{ID: i, Parent: 1, Name: "step"})
	}
	if got := out.String(); got != oscIndeterminate {
		t.Errorf("ten starts wrote %q, want one indeterminate — the terminal heard it the first time", got)
	}
}

func TestOSC_DefaultPulsesThroughTheWholeRun(t *testing.T) {
	var out strings.Builder
	o := NewOSC(&out, false)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "vet"})
	o.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "test"})
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeOK})
	o.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeInterrupted})
	if got := out.String(); got != oscIndeterminate {
		t.Errorf("wrote %q, want one indeterminate pulse for the whole run", got)
	}
}

func TestOSC_DefaultFailureStillTintsRed(t *testing.T) {
	// There is no error-indeterminate state in the OSC 9;4 vocabulary:
	// red before you alt-tab outranks the pulse, percent and all.
	var out strings.Builder
	o := NewOSC(&out, false)
	o.Handle(events.StepStarted{ID: 1, Parent: 0, Name: "ci"})
	o.Handle(events.StepStarted{ID: 2, Parent: 1, Name: "vet"})
	o.Handle(events.StepStarted{ID: 3, Parent: 1, Name: "test"})
	o.Handle(events.StepFinished{ID: 2, Outcome: events.OutcomeFailed})
	o.Handle(events.StepFinished{ID: 3, Outcome: events.OutcomeOK})
	want := oscIndeterminate + oscPct(2, 50) + oscPct(2, 100)
	if got := out.String(); got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}
