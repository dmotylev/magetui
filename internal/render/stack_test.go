package render

import (
	"slices"
	"strings"
	"testing"
)

// A faithful debug.Stack capture from inside the engine's recover: the
// capture machinery and a runtime bounds-check helper above the user's
// frames, the engine scaffolding and the created-by trailer below.
var capturedStack = strings.Join([]string{
	"goroutine 19 [running]:",
	"runtime/debug.Stack()",
	"\t/usr/local/go/src/runtime/debug/stack.go:26 +0x64",
	"github.com/dmotylev/magetui/internal/engine.(*Engine).run.func1()",
	"\t/src/magetui/internal/engine/engine.go:190 +0x9c",
	"panic({0x102f3c1e0?, 0x14000114018?})",
	"\t/usr/local/go/src/runtime/panic.go:783 +0x124",
	"runtime.goPanicIndex(0x3, 0x3)",
	"\t/usr/local/go/src/runtime/panic.go:115 +0x38",
	"main.DropTable({0x14000110000, 0x3})",
	"\t/home/dev/proj/magefile.go:42 +0x1c",
	"main.Nightly.func2({0x102f51a20?, 0x14000114030?})",
	"\t/home/dev/proj/magefile.go:30 +0x20",
	"github.com/dmotylev/magetui/internal/engine.(*Engine).run(0x1400011c000, ...)",
	"\t/src/magetui/internal/engine/engine.go:197 +0x158",
	"github.com/dmotylev/magetui/internal/engine.(*Engine).getOrRun.func1()",
	"\t/src/magetui/internal/engine/engine.go:170 +0x94",
	"created by github.com/dmotylev/magetui/internal/engine.(*Engine).getOrRun in goroutine 1",
	"\t/src/magetui/internal/engine/engine.go:167 +0x1c8",
	"",
}, "\n")

func TestTrimStack_KeepsExactlyTheUserFrames(t *testing.T) {
	want := []string{
		"main.DropTable({0x14000110000, 0x3})",
		"\t/home/dev/proj/magefile.go:42 +0x1c",
		"main.Nightly.func2({0x102f51a20?, 0x14000114030?})",
		"\t/home/dev/proj/magefile.go:30 +0x20",
	}
	if got := trimStack([]byte(capturedStack)); !slices.Equal(got, want) {
		t.Errorf("trim diverges:\n--- got ---\n%s\n--- want ---\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A panic deep inside a third-party library keeps the library frames —
// the panic site matters more than whose package it is in; only known
// machinery is trimmed.
func TestTrimStack_KeepsLibraryFramesBelowThePanicSite(t *testing.T) {
	stack := strings.Join([]string{
		"goroutine 7 [running]:",
		"runtime/debug.Stack()",
		"\t/usr/local/go/src/runtime/debug/stack.go:26 +0x64",
		"github.com/dmotylev/magetui/internal/engine.(*Engine).run.func1()",
		"\t/src/magetui/internal/engine/engine.go:190 +0x9c",
		"panic({0x1, 0x2})",
		"\t/usr/local/go/src/runtime/panic.go:783 +0x124",
		"github.com/somelib/yaml.(*Decoder).Decode(0x0)",
		"\t/home/dev/go/pkg/mod/github.com/somelib/yaml@v1.0.0/decode.go:77 +0x10",
		"main.Configure({0x3, 0x4})",
		"\t/home/dev/proj/magefile.go:12 +0x1c",
		"github.com/dmotylev/magetui/internal/engine.(*Engine).run(0x0, ...)",
		"\t/src/magetui/internal/engine/engine.go:197 +0x158",
	}, "\n")
	got := trimStack([]byte(stack))
	want := []string{
		"github.com/somelib/yaml.(*Decoder).Decode(0x0)",
		"\t/home/dev/go/pkg/mod/github.com/somelib/yaml@v1.0.0/decode.go:77 +0x10",
		"main.Configure({0x3, 0x4})",
		"\t/home/dev/proj/magefile.go:12 +0x1c",
	}
	if !slices.Equal(got, want) {
		t.Errorf("trim diverges:\n--- got ---\n%s\n--- want ---\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTrimStack_AllMachineryTrimsToNothing(t *testing.T) {
	stack := strings.Join([]string{
		"goroutine 3 [running]:",
		"runtime/debug.Stack()",
		"\t/usr/local/go/src/runtime/debug/stack.go:26 +0x64",
		"runtime.gopanic({0x0, 0x0})",
		"\t/usr/local/go/src/runtime/panic.go:783 +0x124",
	}, "\n")
	if got := trimStack([]byte(stack)); got != nil {
		t.Errorf("nothing user-attributable, want nil, got:\n%s", strings.Join(got, "\n"))
	}
	if got := trimStack(nil); got != nil {
		t.Errorf("empty capture must trim to nil, got %q", got)
	}
}
