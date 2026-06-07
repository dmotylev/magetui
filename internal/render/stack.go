package render

import "strings"

// modulePath identifies magetui's own frames in a panic stack: the
// machinery above the user's code (the engine's recover) and the
// scaffolding below it (engine.run, the Deps goroutine).
const modulePath = "github.com/dmotylev/magetui"

// trimStack reduces a debug.Stack capture to the frames the developer
// wants: the panic site down to the last user frame — magefile.go:42,
// not runtime.gopanic (DESIGN.md §4.5). The goroutine header, the
// capture and panic machinery on top, magetui's scaffolding below, and
// the "created by" trailer all go. An unrecognizable stack trims to
// nothing; the replay prints the full capture below it either way.
func trimStack(stack []byte) []string {
	type frame struct{ lines []string }
	var frames []frame
	lines := strings.Split(strings.TrimRight(string(stack), "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if lines[i] == "" || strings.HasPrefix(lines[i], "goroutine ") && strings.HasSuffix(lines[i], ":") {
			continue
		}
		f := frame{lines: []string{lines[i]}}
		// A frame is a function line plus its tab-indented location line.
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") {
			f.lines = append(f.lines, lines[i+1])
			i++
		}
		frames = append(frames, f)
	}

	machinery := func(fn string) bool {
		return strings.HasPrefix(fn, "panic(") ||
			strings.HasPrefix(fn, "runtime.") ||
			strings.HasPrefix(fn, "runtime/") ||
			strings.HasPrefix(fn, modulePath)
	}
	var out []string
	user := false
	for _, f := range frames {
		fn := f.lines[0]
		if strings.HasPrefix(fn, "created by ") {
			break
		}
		if machinery(fn) {
			if user {
				break // the scaffolding below the user's code
			}
			continue // the capture machinery above it
		}
		user = true
		out = append(out, f.lines...)
	}
	return out
}
