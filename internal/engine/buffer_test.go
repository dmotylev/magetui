package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dmotylev/magetui/internal/events"
)

func TestBuffer_HeadFillsThenTailSlides(t *testing.T) {
	// Budget 100: head keeps 50 bytes, tail windows the last 50.
	// Lines cost len+1 = 10 bytes each.
	b := newBuffer(100)
	for i := range 20 {
		b.append(events.Stdout, fmt.Sprintf("line %04d", i))
	}

	head, elided, tail := b.lines()
	if len(head) != 5 {
		t.Fatalf("head = %d lines, want 5", len(head))
	}
	if len(tail) != 5 {
		t.Fatalf("tail = %d lines, want 5", len(tail))
	}
	if elided != 10 {
		t.Fatalf("elided = %d, want 10", elided)
	}
	if head[0].Text != "line 0000" {
		t.Fatalf("head starts at %q; the beginning matters in a postmortem", head[0].Text)
	}
	if tail[len(tail)-1].Text != "line 0019" {
		t.Fatalf("tail ends at %q; the last words matter more", tail[len(tail)-1].Text)
	}
}

func TestBuffer_KeepsTheLastLineEvenWhenItIsAMonologue(t *testing.T) {
	b := newBuffer(20)
	b.append(events.Stdout, "ok")
	monologue := strings.Repeat("the build is fine, probably, ", 10)
	b.append(events.Stderr, monologue)

	_, _, tail := b.lines()
	if len(tail) != 1 || tail[0].Text != monologue {
		t.Fatal("a line larger than the whole budget must still survive as the newest tail line")
	}

	// The next line evicts the monologue: window slides on.
	b.append(events.Stdout, "narrator: it was not")
	_, elided, tail := b.lines()
	if len(tail) != 1 || tail[0].Text != "narrator: it was not" {
		t.Fatalf("tail = %+v, want only the newest line", tail)
	}
	if elided != 1 {
		t.Fatalf("elided = %d, want 1", elided)
	}
}

func TestBuffer_LinesReturnsCopies(t *testing.T) {
	b := newBuffer(100)
	b.append(events.Stdout, "original")
	head, _, _ := b.lines()
	head[0].Text = "tampered"
	again, _, _ := b.lines()
	if again[0].Text != "original" {
		t.Fatal("lines() leaked internal state to a caller with a marker pen")
	}
}
