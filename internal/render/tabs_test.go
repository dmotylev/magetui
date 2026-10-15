package render

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dmotylev/magetui/internal/events"
)

func TestOutputTabsHaveTheSameColumnsInEveryRenderer(t *testing.T) {
	const text = "ok\tpackage\t1s"
	theme := unstyled(ThemeColor)
	tr := NewTree(theme)
	tr.Handle(events.StepStarted{ID: 1, Name: "race"}, at(0))
	tr.Handle(events.OutputLine{ID: 1, Origin: events.Stdout, Text: text}, at(0))
	var plain strings.Builder
	p := NewPlain(&plain, theme)
	p.Handle(events.StepStarted{ID: 1, Name: "race"})
	p.Handle(events.OutputLine{ID: 1, Origin: events.Stdout, Text: text})
	_ = p.Close()
	var replay strings.Builder
	ReplayFailures(&replay, 0, 0, []Replay{{Path: []string{"race"}, Outcome: events.OutcomeFailed, Err: errors.New("failed"), Head: []events.Line{{Origin: events.Stdout, Text: text}}, Tail: []events.Line{{Origin: events.Stderr, Text: text}}}}, theme)
	assertFrame(t, tr.Frame(80, 24, at(time.Second)), "⠋ race  1.0s\n  │ ok      package 1s")
	assertFrame(t, plain.String(), "○ race  started\n| race  ok      package 1s\n")
	assertFrame(t, replay.String(), "\n──────────────────────────────────────────\n✗ race  0.0s  failed\n  │ ok      package 1s\n  ┃ ok      package 1s\n\n")
	assertFrame(t, tr.Frame(12, 24, at(time.Second)), "⠋ race  1.0s\n  │ ok     …")
}

func TestOutputTabsFollowDisplayWidth(t *testing.T) {
	for _, text := range []string{"界\tend", "\x1b[31m界\x1b[0m\tend"} {
		var out strings.Builder
		p := NewPlain(&out, unstyled(ThemeColor))
		p.Handle(events.StepStarted{ID: 1, Name: "race"})
		p.Handle(events.OutputLine{ID: 1, Origin: events.Stdout, Text: text})
		_ = p.Close()
		want := strings.ReplaceAll(text, "\t", "      ")
		assertFrame(t, out.String(), "○ race  started\n| race  "+want+"\n")
		tr := NewTree(unstyled(ThemeColor))
		tr.Handle(events.StepStarted{ID: 1, Name: "race"}, at(0))
		tr.Handle(events.OutputLine{ID: 1, Origin: events.Stdout, Text: text}, at(0))
		assertFrame(t, tr.Frame(14, 24, at(time.Second)), "⠋ race  1.0s\n  │ "+strings.TrimSuffix(want, "end")+"e…")
	}
}

func TestOutputTabsSeparateGoTestColumns(t *testing.T) {
	tr := NewTree(unstyled(ThemeColor))
	tr.Handle(events.StepStarted{ID: 1, Name: "race"}, at(0))
	tr.Handle(events.OutputLine{ID: 1, Origin: events.Stdout, Text: "?\tgithub.com/dmotylev/vu1-dials/internal/buildpack\t[no test files]"}, at(0))
	tr.Handle(events.OutputLine{ID: 1, Origin: events.Stdout, Text: "ok\tgithub.com/dmotylev/vu1-dials/internal/clock\t1.213s"}, at(0))
	assertFrame(t, tr.Frame(100, 24, at(time.Second)), "⠋ race  1.0s\n  │ ?       github.com/dmotylev/vu1-dials/internal/buildpack        [no test files]\n  │ ok      github.com/dmotylev/vu1-dials/internal/clock    1.213s")
}
