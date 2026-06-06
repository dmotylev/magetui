package magetui

import (
	"bytes"
	"testing"
)

func TestResolveMode_EnvOverridesCodeOverridesDetection(t *testing.T) {
	var buf bytes.Buffer // not a terminal, whatever it claims at parties
	for _, tc := range []struct {
		name string
		env  string
		mode ProgressMode
		want ProgressMode
	}{
		{"auto detects non-terminal as plain", "", ProgressAuto, ProgressPlain},
		{"code picks tty", "", ProgressTTY, ProgressTTY},
		{"env plain overrides code tty", "plain", ProgressTTY, ProgressPlain},
		{"env tty overrides code plain", "tty", ProgressPlain, ProgressTTY},
		{"env auto overrides code back to detection", "auto", ProgressTTY, ProgressPlain},
		{"env gibberish is ignored", "disco", ProgressTTY, ProgressTTY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAGETUI_PROGRESS", tc.env)
			if got := resolveMode(targetOptions{out: &buf, mode: tc.mode}); got != tc.want {
				t.Errorf("resolveMode = %v, want %v", got, tc.want)
			}
		})
	}
}
