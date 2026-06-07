package render

import (
	"bytes"
	"errors"
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
	w       io.Writer            // the raw writer, kept for ^C degradation
	profile colorprofile.Profile // downsamples scrollback blocks
	done    chan error           // Run's verdict, consumed by Close
	gone    chan struct{}        // closed when Run returns; guards println

	// mu guards tree between Handle (engine goroutines) and View (the
	// program's event loop). Never held across prog calls: the event
	// loop may be waiting on mu inside View while a prog call waits on
	// the event loop.
	mu       sync.Mutex
	tree     *Tree
	fallback *Plain // plain takeover after the program died mid-run
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
		w:       w,
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
// closed, and nudges the program to repaint. Once the program is gone
// while the engine still runs — ^C, a SIGTERM-quit, any premature
// program death — events degrade to a plain renderer instead
// (DESIGN.md §6): the terminal is already restored, the live region is
// gone, and the stragglers stream as plain lines below it until the
// engine drains.
func (t *TUI) Handle(ev events.Event) {
	select {
	case <-t.gone:
		t.degraded().Handle(ev)
		return
	default:
	}
	t.mu.Lock()
	t.tree.Handle(ev, time.Now())
	blocks := t.tree.TakeBlocks()
	t.mu.Unlock()
	for _, b := range blocks {
		t.println(b)
	}
	t.prog.Send(repaintMsg{})
}

// degraded lazily builds the plain renderer Handle falls back to after
// the program died mid-run, seeded with every step the tree knows so the
// stragglers' paths resolve. It writes through the same profile the
// blocks used: the raw writer needs no query stripping once bubbletea is
// gone.
func (t *TUI) degraded() *Plain {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fallback == nil {
		t.fallback = NewPlainOver(&colorprofile.Writer{Forward: t.w, Profile: t.profile}, t.tree)
	}
	return t.fallback
}

// Close quits the program and waits for the terminal to be restored.
// Every block is already in the program's queue ahead of the Quit —
// Handle delivered them synchronously — so nothing is lost. Target
// writes the failure replay only after this returns, as plain prose
// below the vanished live region (DESIGN.md §3.3). An ErrInterrupted
// verdict is not a renderer failure: it is bubbletea reporting the ^C
// that the degradation path already absorbed.
func (t *TUI) Close() error {
	t.prog.Quit()
	err := <-t.done
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
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
	}
	// No InterruptMsg case: bubbletea's event loop intercepts it before
	// any Update and returns ErrInterrupted from Run (v2.0.7 eventLoop) —
	// the model never sees it. SIGTERM likewise becomes an internal
	// QuitMsg. Either way the program exits and restores the terminal;
	// Handle notices the closed gone channel and degrades to plain
	// (DESIGN.md §6). Mage owns the actual interruption: its mainfile
	// cancels the target context on the first ^C and force-exits on the
	// second — which then finds the terminal already cooked.
	return m, nil
}

func (m tuiModel) View() tea.View {
	m.t.mu.Lock()
	frame := m.t.tree.Frame(m.width, m.height, time.Now())
	m.t.mu.Unlock()
	return tea.NewView(frame)
}

// stripped lists the terminal sequences bubbletea v2.0.7 emits even with
// input disabled, in two families, that must not reach the terminal.
//
// The interrogations — the kitty keyboard query on the first render and
// the DECRQM synchronized-output/unicode-core pair at startup: with
// WithInput(nil) the answers can never be read — they land in the
// cooked-mode stdin buffer, where the line discipline echoes them onto
// the screen as ^[[?1u-style garbage and the next stdin reader inherits
// them as phantom keystrokes.
//
// The keyboard enhancements — the renderer unconditionally enables
// modifyOtherKeys(2) and the kitty keyboard protocol with the
// disambiguate flag at start (cursed_renderer.go). In a terminal that
// honors either (Ghostty, kitty, WezTerm, xterm), ^C then arrives as an
// escape sequence on stdin instead of a 0x03 byte — the line discipline
// never raises SIGINT, and mage's whole interrupt handling (DESIGN.md
// §6) silently dies while nobody reads the "enhanced" events anyway.
//
// Stripping both families costs nothing: every feature they would
// unlock needs the input that is never read.
var stripped = [][]byte{
	[]byte("\x1b[?u"),      // ansi.RequestKittyKeyboard
	[]byte("\x1b[?2026$p"), // ansi.RequestModeSynchronizedOutput
	[]byte("\x1b[?2027$p"), // ansi.RequestModeUnicodeCore
	[]byte("\x1b[>4;2m"),   // ansi.SetModifyOtherKeys2
	[]byte("\x1b[>4m"),     // ansi.ResetModifyOtherKeys
	[]byte("\x1b[=1;1u"),   // ansi.KittyKeyboard(1, 1): disambiguate on
	[]byte("\x1b[=0;1u"),   // ansi.KittyKeyboard(0, 1): the reset
}

// maxStripCarry bounds the held-back tail: the longest query is 9 bytes,
// so a partial match is at most 8.
const maxStripCarry = 8

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
// at most maxStripCarry trailing bytes when they could be the start of a
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
	for _, q := range stripped {
		data = bytes.ReplaceAll(data, q, nil)
	}
	// Write before touching carry: data may share carry's backing array.
	cut := len(data) - partialStripSuffix(data)
	if _, err := s.w.Write(data[:cut]); err != nil {
		return 0, err
	}
	s.carry = append(s.carry[:0], data[cut:]...)
	return len(p), nil
}

// partialStripSuffix returns the length of the longest data suffix that
// is a proper prefix of any query — the bytes that cannot be emitted yet.
func partialStripSuffix(data []byte) int {
	for k := min(len(data), maxStripCarry); k > 0; k-- {
		tail := data[len(data)-k:]
		for _, q := range stripped {
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
