package render

import (
	"io"
	"strings"
	"testing"
)

// The adapter's tea.Model is deliberately untested (DESIGN.md §3.3); the
// query stripper is a pure writer and gets the full treatment — it
// guards the screen of every kitty-capable terminal.

func TestStripWriter_RemovesEachQueryWherverItSits(t *testing.T) {
	for _, q := range queries {
		var out strings.Builder
		w := &stripWriter{w: &out}
		if _, err := w.Write([]byte("before" + string(q) + "after")); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != "beforeafter" {
			t.Errorf("query %q survived: %q", q, got)
		}
	}
}

func TestStripWriter_RemovesQueriesSplitAtEveryBoundary(t *testing.T) {
	for _, q := range queries {
		for cut := 1; cut < len(q); cut++ {
			var out strings.Builder
			w := &stripWriter{w: &out}
			_, _ = w.Write([]byte("live region " + string(q[:cut])))
			_, _ = w.Write(q[cut:])
			_, _ = w.Write([]byte("more frames"))
			if got := out.String(); got != "live region more frames" {
				t.Errorf("query %q split at %d survived: %q", q, cut, got)
			}
		}
	}
}

func TestStripWriter_LeavesInnocentLookalikesAlone(t *testing.T) {
	// Cursor hide, bracketed paste, a DECRQM for a mode we don't strip,
	// and a near-miss final byte — all share prefixes with the queries.
	for _, s := range []string{
		"\x1b[?25l",
		"\x1b[?2004h",
		"\x1b[?2028$p",
		"\x1b[?2026$q",
		"\x1b[?u" + "", // the query itself, as a control
	} {
		var out strings.Builder
		w := &stripWriter{w: &out}
		_, _ = w.Write([]byte(s))
		_, _ = w.Write([]byte("|flush"))
		want := s + "|flush"
		if s == "\x1b[?u" {
			want = "|flush"
		}
		if got := out.String(); got != want {
			t.Errorf("sequence %q mangled: got %q, want %q", s, got, want)
		}
	}
}

func TestStripWriter_HoldsAtMostAPartialQueryAcrossWrites(t *testing.T) {
	var out strings.Builder
	w := &stripWriter{w: &out}
	// A trailing ESC[?2 could open ESC[?2026$p — held back...
	_, _ = w.Write([]byte("frame\x1b[?2"))
	if got := out.String(); got != "frame" {
		t.Fatalf("partial query leaked: %q", got)
	}
	// ...until the next write reveals an innocent cursor-mode sequence.
	_, _ = w.Write([]byte("5l rest"))
	if got := out.String(); got != "frame\x1b[?25l rest" {
		t.Errorf("held bytes lost or reordered: %q", got)
	}
}

func TestStripWriter_StripsAdjacentQueriesInOneChunk(t *testing.T) {
	var out strings.Builder
	w := &stripWriter{w: &out}
	_, _ = w.Write([]byte("\x1b[?2026$p\x1b[?2027$p\x1b[?ureal output"))
	if got := out.String(); got != "real output" {
		t.Errorf("startup query burst survived: %q", got)
	}
}

// fakeTTY stands in for os.Stdout: the wrapper must keep the full
// term.File surface or bubbletea stops treating the output as a TTY.
type fakeTTY struct{ strings.Builder }

func (*fakeTTY) Read([]byte) (int, error) { return 0, io.EOF }
func (*fakeTTY) Close() error             { return nil }
func (*fakeTTY) Fd() uintptr              { return 42 }

func TestTUIWriter_PreservesTheFileSurfaceOfTerminals(t *testing.T) {
	f := &fakeTTY{}
	w := tuiWriter(f)
	file, ok := w.(interface {
		io.ReadWriteCloser
		Fd() uintptr
	})
	if !ok {
		t.Fatal("terminal output lost its file surface; bubbletea would drop resize handling")
	}
	if file.Fd() != 42 {
		t.Errorf("Fd() not delegated: %d", file.Fd())
	}
	if _, err := file.Write([]byte("\x1b[?upayload")); err != nil {
		t.Fatal(err)
	}
	if got := f.String(); got != "payload" {
		t.Errorf("file writes must still strip queries: %q", got)
	}
}

func TestTUIWriter_PlainWritersGetTheStripperToo(t *testing.T) {
	var out strings.Builder
	w := tuiWriter(&out)
	if _, ok := w.(*stripWriter); !ok {
		t.Fatalf("non-file output should be a bare stripWriter, got %T", w)
	}
}
