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

func TestOSCMode_GatingTable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         map[string]string
		tty         bool
		on, percent bool
	}{
		{"ghostty on a terminal pulses", map[string]string{"TERM_PROGRAM": "ghostty"}, true, true, false},
		{"ghostty piped", map[string]string{"TERM_PROGRAM": "ghostty"}, false, false, false},
		{"windows terminal", map[string]string{"WT_SESSION": "guid-of-some-sort"}, true, true, false},
		{"conemu", map[string]string{"ConEmuANSI": "ON"}, true, true, false},
		{"conemu without ansi", map[string]string{"ConEmuANSI": "OFF"}, true, false, false},
		{"unknown terminal stays dark", nil, true, false, false},
		{"env on outranks everything", map[string]string{"MAGETUI_OSC_PROGRESS": "on"}, false, true, false},
		{"env percent opts into the determinate bar", map[string]string{"MAGETUI_OSC_PROGRESS": "percent"}, false, true, true},
		{"env off outranks ghostty", map[string]string{"MAGETUI_OSC_PROGRESS": "off", "TERM_PROGRAM": "ghostty"}, true, false, false},
		{"env gibberish falls back to detection", map[string]string{"MAGETUI_OSC_PROGRESS": "disco", "TERM_PROGRAM": "ghostty"}, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			on, percent := oscMode(getenv, tc.tty)
			if on != tc.on || percent != tc.percent {
				t.Errorf("oscMode = (%v, %v), want (%v, %v)", on, percent, tc.on, tc.percent)
			}
		})
	}
}
