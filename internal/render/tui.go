package render

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/dmotylev/magetui/internal/events"
)

// tickInterval drives spinner frames and live elapsed timers.
const tickInterval = 100 * time.Millisecond

// TUI is the bubbletea adapter around the Tree layout core (DESIGN.md
// §3.3): inline mode (the v2 default), WithInput(nil) so stdin stays with
// the user's subprocesses, a ~100ms tick. The adapter has no unit tests —
// the Phase 4 spike proved its primitives, the Phase 7 VHS rig
// smoke-tests it; goldens probe Tree directly.
//
// Scrollback blocks go through Program.Println, not through tea.Println
// commands returned from Update: bubbletea v2 runs every command in its
// own goroutine, so commands from different Updates are unordered —
// block commits would shuffle, and the final root line always lost its
// race against Quit. Program.Println enqueues into the program's FIFO
// synchronously from the caller, and the engine already serializes
// Handle, so commit order and println-before-Quit come for free.
type TUI struct {
	prog    *tea.Program
	profile colorprofile.Profile // downsamples scrollback blocks
	done    chan error           // Run's verdict, consumed by Close
	gone    chan struct{}        // closed when Run returns; guards println

	// mu guards tree between Handle (engine goroutines) and View (the
	// program's event loop). Never held across prog calls: the event
	// loop may be waiting on mu inside View while a prog call waits on
	// the event loop.
	mu   sync.Mutex
	tree *Tree
}

// NewTUI boots the bubbletea program writing to w and returns without
// waiting for it. Program.Send blocks until the program actually runs
// and no-ops after it exits, so Handle needs no further synchronization.
// The theme's colors ride the live-region frames as-is — bubbletea
// detects the terminal's profile itself and downsamples what it
// repaints — but scrollback blocks need our own copy of that detection:
// see println.
func NewTUI(w io.Writer, theme Theme) *TUI {
	t := &TUI{
		profile: colorprofile.Detect(w, os.Environ()),
		done:    make(chan error, 1),
		gone:    make(chan struct{}),
		tree:    NewTree(theme),
	}
	t.prog = tea.NewProgram(tuiModel{t: t}, tea.WithOutput(tuiWriter(w)), tea.WithInput(nil))
	go func() {
		_, err := t.prog.Run()
		close(t.gone)
		t.done <- err
	}()
	return t
}

// Handle folds one event into the tree, prints whatever blocks it
// closed, and nudges the program to repaint.
func (t *TUI) Handle(ev events.Event) {
	t.mu.Lock()
	t.tree.Handle(ev, time.Now())
	blocks := t.tree.TakeBlocks()
	t.mu.Unlock()
	for _, b := range blocks {
		t.println(b)
	}
	t.prog.Send(repaintMsg{})
}

// Close quits the program and waits for the terminal to be restored.
// Every block is already in the program's queue ahead of the Quit —
// Handle delivered them synchronously — so nothing is lost. Target
// writes the failure replay only after this returns, as plain prose
// below the vanished live region (DESIGN.md §3.3).
func (t *TUI) Close() error {
	t.prog.Quit()
	return <-t.done
}

// println delivers one scrollback block. Two scars live here. First,
// bubbletea downsamples only what its cell renderer repaints; Println
// content reaches the terminal raw (ultraviolet's InsertAbove writes the
// lines verbatim, v2.0.7), so NO_COLOR and low-color terminals would get
// the blocks in full color while the live region above them obeys — the
// block is downsampled here with the same detection bubbletea runs.
// Second, Program.Println, unlike Send, has no after-exit guard and
// would block the engine forever on a dead program; pairing the wait
// with the gone signal keeps a broken TUI from eating the build (at
// worst one parked goroutine per late block).
func (t *TUI) println(block string) {
	if t.profile != colorprofile.TrueColor {
		var sb strings.Builder
		_, _ = (&colorprofile.Writer{Forward: &sb, Profile: t.profile}).WriteString(block)
		block = sb.String()
	}
	delivered := make(chan struct{})
	go func() {
		t.prog.Println(block)
		close(delivered)
	}()
	select {
	case <-delivered:
	case <-t.gone:
	}
}

// tickMsg is the repaint heartbeat; repaintMsg is Handle's nudge that
// the tree changed — the render bubbletea performs after every Update is
// the point, so both are otherwise empty.
type (
	tickMsg    time.Time
	repaintMsg struct{}
)

// tuiModel is the Elm-loop face of the adapter: tick, resize, interrupt,
// and the live-region frame. The tree itself lives in TUI under its
// mutex; the model only paints it.
type tuiModel struct {
	t             *TUI
	width, height int
}

func (m tuiModel) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Resize is just another frame; the next View recomputes the
		// ladder from scratch (DESIGN.md §4.2).
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m, tick()
	case tea.InterruptMsg:
		// bubbletea swallows SIGINT and delivers it here instead. Real
		// interrupt teardown — cancel the root context, mark steps ⊘ —
		// is Phase 6; until then ^C drops the live region cleanly and a
		// second ^C (back at default disposition) kills the process
		// with the terminal already restored.
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) View() tea.View {
	m.t.mu.Lock()
	frame := m.t.tree.Frame(m.width, m.height, time.Now())
	m.t.mu.Unlock()
	return tea.NewView(frame)
}

// queries are the terminal interrogations bubbletea v2.0.7 emits even
// with input disabled: the kitty keyboard query on the first render, and
// the DECRQM synchronized-output/unicode-core pair at startup on capable
// terminals. With WithInput(nil) the answers can never be read — they
// land in the cooked-mode stdin buffer, where the line discipline echoes
// them onto the screen as ^[[?1u-style garbage and the next stdin reader
// inherits them as phantom keystrokes. Stripping the questions costs
// nothing: the features they would unlock need the very answers that are
// never read.
var queries = [][]byte{
	[]byte("\x1b[?u"),      // ansi.RequestKittyKeyboard
	[]byte("\x1b[?2026$p"), // ansi.RequestModeSynchronizedOutput
	[]byte("\x1b[?2027$p"), // ansi.RequestModeUnicodeCore
}

// maxQueryCarry bounds the held-back tail: the longest query is 9 bytes,
// so a partial match is at most 8.
const maxQueryCarry = 8

// tuiWriter prepares w for bubbletea: every output gets the query
// stripper (whose mutex also serializes the program's two writing
// goroutines — ticker flush and insert-above — a genuine data race on
// any non-file writer). A terminal file additionally keeps its
// Read/Close/Fd surface: bubbletea discovers the TTY by type-asserting
// the output, and losing Fd() would lose resize handling and size
// queries with it.
func tuiWriter(w io.Writer) io.Writer {
	if f, ok := w.(interface {
		io.ReadWriteCloser
		Fd() uintptr
	}); ok {
		return &strippedFile{stripWriter: stripWriter{w: f}, f: f}
	}
	return &stripWriter{w: w}
}

// stripWriter removes the query sequences from the stream, holding back
// at most maxQueryCarry trailing bytes when they could be the start of a
// query split across Write calls; the next write resolves them. Held
// bytes only ever wait for output that bubbletea is already about to
// send — every query is followed by more startup output in the same
// breath.
type stripWriter struct {
	mu    sync.Mutex
	w     io.Writer
	carry []byte
}

func (s *stripWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := append(s.carry, p...)
	for _, q := range queries {
		data = bytes.ReplaceAll(data, q, nil)
	}
	// Write before touching carry: data may share carry's backing array.
	cut := len(data) - partialQuerySuffix(data)
	if _, err := s.w.Write(data[:cut]); err != nil {
		return 0, err
	}
	s.carry = append(s.carry[:0], data[cut:]...)
	return len(p), nil
}

// partialQuerySuffix returns the length of the longest data suffix that
// is a proper prefix of any query — the bytes that cannot be emitted yet.
func partialQuerySuffix(data []byte) int {
	for k := min(len(data), maxQueryCarry); k > 0; k-- {
		tail := data[len(data)-k:]
		for _, q := range queries {
			if k < len(q) && bytes.Equal(tail, q[:k]) {
				return k
			}
		}
	}
	return 0
}

// strippedFile is the stripWriter for terminal files: writes are
// filtered, everything else passes through so the output still
// satisfies bubbletea's term.File assertion.
type strippedFile struct {
	stripWriter
	f interface {
		io.ReadWriteCloser
		Fd() uintptr
	}
}

func (s *strippedFile) Read(p []byte) (int, error) { return s.f.Read(p) }
func (s *strippedFile) Close() error               { return s.f.Close() }
func (s *strippedFile) Fd() uintptr                { return s.f.Fd() }
