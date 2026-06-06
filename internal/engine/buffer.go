package engine

import (
	"sync"

	"github.com/dmotylev/magetui/internal/events"
)

// Line is one recorded line of step output.
type Line struct {
	Origin events.Origin
	Text   string
}

// buffer records a step's output within a byte budget. The first half of
// the budget keeps the head of the output; the second half is a sliding
// window over the tail, evicting oldest lines and counting what was
// dropped. Failure replay then shows head, an elision marker, and tail
// instead of OOMing on a chatty step (DESIGN.md §3.1).
type buffer struct {
	mu        sync.Mutex
	headMax   int
	tailMax   int
	headBytes int
	tailBytes int
	head      []Line
	tail      []Line
	elided    int
}

func newBuffer(budget int) *buffer {
	return &buffer{headMax: budget / 2, tailMax: budget - budget/2}
}

func cost(text string) int { return len(text) + 1 }

func (b *buffer) append(origin events.Origin, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := cost(text)
	if b.headBytes+c <= b.headMax {
		b.head = append(b.head, Line{origin, text})
		b.headBytes += c
		return
	}
	b.tail = append(b.tail, Line{origin, text})
	b.tailBytes += c
	// Evict oldest tail lines over budget, but always keep the newest line
	// even if it alone exceeds the budget — replay must show the last thing
	// a step said.
	for b.tailBytes > b.tailMax && len(b.tail) > 1 {
		b.tailBytes -= cost(b.tail[0].Text)
		b.tail = b.tail[1:]
		b.elided++
	}
}

// lines returns copies of the recorded head and tail, and the number of
// lines elided between them.
func (b *buffer) lines() (head []Line, elided int, tail []Line) {
	b.mu.Lock()
	defer b.mu.Unlock()
	head = append([]Line(nil), b.head...)
	tail = append([]Line(nil), b.tail...)
	return head, b.elided, tail
}
