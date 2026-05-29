// Package ui renders deploy progress in two modes:
//
//   - Plain: line-by-line transcript, safe for non-tty (CI, pipes, logs).
//   - Pretty: Docker-style live block with a spinner and per-step elapsed
//     time, repainted in-place. Only used when stdout is a tty.
//
// Both modes share the Reporter / Step contract so callers stay identical.
package ui

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// writef writes a formatted line, logging errors to stderr if the write
// fails. Centralised so the errcheck-clean call sites stay terse.
func writef(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		log.Printf("ui: write failed: %v", err)
	}
}

func writeln(w io.Writer, s string) {
	if _, err := fmt.Fprintln(w, s); err != nil {
		log.Printf("ui: write failed: %v", err)
	}
}

// Reporter is the top-level handle a command obtains for the duration of
// a deploy/restart/rollback. It is concurrency-safe.
type Reporter interface {
	// Header prints a one-line title at the very top (e.g. service ➜ host).
	Header(format string, args ...any)
	// Step starts a top-level step. The returned handle MUST be terminated
	// with Done() or Fail() before another Step is started.
	Step(name string) Step
	// Info prints a free-form line outside any step (e.g. "Version: 3 → 4").
	Info(format string, args ...any)
	// Warn prints a warning line (non-fatal).
	Warn(format string, args ...any)
	// Close finalises any pending live area. Safe to call multiple times.
	Close()
}

// Step is a handle to a single in-progress step. Detail lines accumulate
// under the step header; on Done()/Fail() the header is frozen with its
// final state and elapsed time.
type Step interface {
	// Detail adds an indented info line under this step.
	Detail(format string, args ...any)
	// Quiet freezes the live area and stops the spinner for this step.
	// Use it BEFORE invoking a subprocess that streams its own output to
	// the same terminal (docker build, kubectl rollout, etc) — otherwise
	// the spinner's cursor-up redraw collides with the subprocess lines.
	// After Quiet, Detail/Done/Fail write fresh lines below whatever the
	// subprocess produced. No-op for the plain reporter.
	Quiet()
	// Stream returns a writer that captures subprocess output and renders
	// the last `tail` lines as a scrolling window beneath the step header
	// (Docker-style). ANSI escape sequences are stripped. On Done/Fail
	// the tail window vanishes, leaving only the final ✓/✗ line. For the
	// plain reporter, returns a writer that passes lines through verbatim,
	// indented under the step.
	Stream(tail int) io.Writer
	// Done marks the step completed (✓).
	Done()
	// Fail marks the step failed (✗) with an error.
	Fail(err error)
}

// New returns the appropriate Reporter for w. If w is a tty, returns a
// pretty reporter; otherwise a plain one. nil writer falls back to stdout.
func New(w io.Writer) Reporter {
	if w == nil {
		w = os.Stdout
	}
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return NewPretty(w)
	}
	return NewPlain(w)
}

// Discard returns a reporter that swallows all output. Useful for tests.
func Discard() Reporter { return NewPlain(io.Discard) }

// -----------------------------------------------------------------------------
// Plain reporter
// -----------------------------------------------------------------------------

// NewPlain returns a transcript-style reporter that writes one line at a
// time. Safe for non-tty output. Step elapsed time is printed on Done/Fail.
func NewPlain(w io.Writer) Reporter {
	return &plainReporter{w: w, now: time.Now}
}

type plainReporter struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func (r *plainReporter) Header(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	writef(r.w, "[+] "+format+"\n", args...)
}

func (r *plainReporter) Step(name string) Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	writef(r.w, " · %s\n", name)
	return &plainStep{r: r, name: name, start: r.now()}
}

func (r *plainReporter) Info(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	writef(r.w, "   "+format+"\n", args...)
}

func (r *plainReporter) Warn(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	writef(r.w, "   ! "+format+"\n", args...)
}

func (r *plainReporter) Close() {
	// nothing to do — plain reporter has no live area to flush
}

type plainStep struct {
	r     *plainReporter
	name  string
	start time.Time
	ended bool
}

func (s *plainStep) Quiet() {
	// nothing to do — plain reporter has no live area to suspend
}

// Stream returns a writer that passes captured lines through verbatim,
// indented under the step. tail is ignored — plain mode shows everything.
func (s *plainStep) Stream(tail int) io.Writer {
	_ = tail
	return &plainStreamWriter{s: s}
}

type plainStreamWriter struct {
	s       *plainStep
	partial []byte
}

func (w *plainStreamWriter) Write(p []byte) (int, error) {
	w.s.r.mu.Lock()
	defer w.s.r.mu.Unlock()
	if w.s.ended {
		return len(p), nil
	}
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexAny(w.partial, "\r\n")
		if i < 0 {
			break
		}
		line := string(w.partial[:i])
		w.partial = w.partial[i+1:]
		stripped := ansi.Strip(line)
		if stripped == "" {
			continue
		}
		writef(w.s.r.w, "     %s\n", stripped)
	}
	return len(p), nil
}

func (s *plainStep) Detail(format string, args ...any) {
	if s.ended {
		return
	}
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	writef(s.r.w, "     "+format+"\n", args...)
}

func (s *plainStep) Done() {
	if s.ended {
		return
	}
	s.ended = true
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	writef(s.r.w, " ✓ %s  %s\n", s.name, formatElapsed(s.r.now().Sub(s.start)))
}

func (s *plainStep) Fail(err error) {
	if s.ended {
		return
	}
	s.ended = true
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	writef(s.r.w, " ✗ %s  %s\n     %v\n", s.name, formatElapsed(s.r.now().Sub(s.start)), err)
}

// -----------------------------------------------------------------------------
// Pretty reporter
// -----------------------------------------------------------------------------

var (
	styleHeader  = lipgloss.NewStyle().Bold(true)
	styleStep    = lipgloss.NewStyle()
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	styleFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // red
	styleRunning = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // cyan
	styleDetail  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // grey
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // yellow
	styleElapsed = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // grey
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewPretty returns a Docker-style live reporter. The current step's
// header line is repainted at ~10Hz to animate the spinner and tick the
// elapsed timer. Completed steps are frozen as plain transcript above.
func NewPretty(w io.Writer) Reporter {
	r := &prettyReporter{w: w, now: time.Now, tickEvery: 100 * time.Millisecond}
	return r
}

type prettyReporter struct {
	mu        sync.Mutex
	w         io.Writer
	now       func() time.Time
	tickEvery time.Duration

	active    *prettyStep // current step (nil if none)
	liveLines int         // number of lines currently in the live area
	stopTick  chan struct{}
}

func (r *prettyReporter) Header(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freezeLocked()
	writeln(r.w, styleHeader.Render(fmt.Sprintf("[+] "+format, args...)))
}

func (r *prettyReporter) Info(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freezeLocked()
	writeln(r.w, "   "+fmt.Sprintf(format, args...))
}

func (r *prettyReporter) Warn(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freezeLocked()
	writeln(r.w, styleWarn.Render("   ! "+fmt.Sprintf(format, args...)))
}

func (r *prettyReporter) Step(name string) Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freezeLocked()
	s := &prettyStep{r: r, name: name, start: r.now()}
	r.active = s
	r.paintLocked()
	r.startTickerLocked()
	return s
}

func (r *prettyReporter) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freezeLocked()
}

// freezeLocked finalises the live area: stops the ticker, clears the
// animated lines, and re-emits the active step (if any) as a frozen
// done/fail line. After this, liveLines == 0 and active == nil.
func (r *prettyReporter) freezeLocked() {
	r.stopTickerLocked()
	if r.active == nil {
		if r.liveLines > 0 {
			r.clearLiveLocked()
		}
		return
	}
	// Active step was never explicitly ended — treat as done.
	r.clearLiveLocked()
	r.writeFrozenLocked(r.active, false, nil)
	r.active = nil
}

// paintLocked repaints the live area in-place. Caller holds r.mu.
// When the active step is streaming, the body shows the last N captured
// lines (tail window) instead of accumulated details.
func (r *prettyReporter) paintLocked() {
	if r.active == nil {
		return
	}
	r.clearLiveLocked()
	lines := []string{r.formatActiveHeader(r.active)}
	body := r.active.details
	if r.active.streaming {
		body = r.active.tailLines
	}
	for _, d := range body {
		lines = append(lines, styleDetail.Render("     "+d))
	}
	// Write the live region WITHOUT a trailing newline so the cursor stays
	// parked on the last line. A trailing newline while the block sits on
	// the bottom screen row forces the terminal to scroll on every repaint,
	// committing each animation frame to scrollback (a 21s build at 10Hz
	// leaves 200+ spinner lines in the transcript). clearLiveLocked walks
	// back up from the parked cursor before the next paint.
	writef(r.w, "%s", strings.Join(lines, "\n"))
	r.liveLines = len(lines)
}

// clearLiveLocked erases the live area. The cursor is parked at the end of
// the last live line (paintLocked emits no trailing newline), so move to
// column 0, up to the first live line, then erase to end of screen.
// Caller holds r.mu.
func (r *prettyReporter) clearLiveLocked() {
	if r.liveLines == 0 {
		return
	}
	up := ""
	if r.liveLines > 1 {
		up = fmt.Sprintf("\x1b[%dA", r.liveLines-1)
	}
	// \r = column 0; CSI nA = cursor up n lines; CSI 0J = erase to end of screen.
	writef(r.w, "\r%s\x1b[0J", up)
	r.liveLines = 0
}

func (r *prettyReporter) writeFrozenLocked(s *prettyStep, failed bool, err error) {
	icon := styleOK.Render("✓")
	if failed {
		icon = styleFail.Render("✗")
	}
	elapsed := styleElapsed.Render(formatElapsed(r.now().Sub(s.start)))
	writef(r.w, " %s %s  %s\n", icon, styleStep.Render(s.name), elapsed)
	for _, d := range s.details {
		writeln(r.w, styleDetail.Render("     "+d))
	}
	if failed && err != nil {
		writeln(r.w, styleFail.Render("     "+err.Error()))
	}
}

func (r *prettyReporter) formatActiveHeader(s *prettyStep) string {
	frame := spinnerFrames[int(r.now().UnixNano()/int64(r.tickEvery))%len(spinnerFrames)]
	elapsed := styleElapsed.Render(formatElapsed(r.now().Sub(s.start)))
	return fmt.Sprintf(" %s %s  %s", styleRunning.Render(frame), styleStep.Render(s.name), elapsed)
}

func (r *prettyReporter) startTickerLocked() {
	if r.stopTick != nil {
		return
	}
	stop := make(chan struct{})
	r.stopTick = stop
	go func() {
		t := time.NewTicker(r.tickEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r.mu.Lock()
				if r.active != nil {
					r.paintLocked()
				}
				r.mu.Unlock()
			}
		}
	}()
}

func (r *prettyReporter) stopTickerLocked() {
	if r.stopTick == nil {
		return
	}
	close(r.stopTick)
	r.stopTick = nil
}

type prettyStep struct {
	r         *prettyReporter
	name      string
	start     time.Time
	details   []string
	ended     bool
	quiet     bool // true after Quiet() — live area suspended, output flows inline
	streaming bool // true after Stream() — paint shows tail window of captured output
	tailMax   int
	tailLines []string
	tailBuf   []byte // partial line awaiting \n
}

func (s *prettyStep) Detail(format string, args ...any) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.ended || s.r.active != s {
		return
	}
	line := fmt.Sprintf(format, args...)
	s.details = append(s.details, line)
	if s.quiet {
		writeln(s.r.w, styleDetail.Render("     "+line))
		return
	}
	s.r.paintLocked()
}

// Stream switches this step into tail-window mode. Until Done/Fail, the
// returned writer captures subprocess output and the live area shows the
// last `tail` lines under the spinner header. tail < 1 falls back to 1.
func (s *prettyStep) Stream(tail int) io.Writer {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if tail < 1 {
		tail = 1
	}
	if s.ended || s.r.active != s {
		// Return a no-op writer so callers don't crash on misuse.
		return io.Discard
	}
	s.streaming = true
	s.tailMax = tail
	s.r.paintLocked()
	return &prettyStreamWriter{s: s}
}

type prettyStreamWriter struct {
	s *prettyStep
}

func (w *prettyStreamWriter) Write(p []byte) (int, error) {
	s := w.s
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.ended {
		return len(p), nil
	}
	s.tailBuf = append(s.tailBuf, p...)
	changed := false
	for {
		i := bytes.IndexAny(s.tailBuf, "\r\n")
		if i < 0 {
			break
		}
		line := string(s.tailBuf[:i])
		s.tailBuf = s.tailBuf[i+1:]
		stripped := ansi.Strip(line)
		if stripped == "" {
			continue
		}
		if len(s.tailLines) >= s.tailMax {
			s.tailLines = append(s.tailLines[:0], s.tailLines[len(s.tailLines)-s.tailMax+1:]...)
		}
		s.tailLines = append(s.tailLines, stripped)
		changed = true
	}
	if changed {
		s.r.paintLocked()
	}
	return len(p), nil
}

func (s *prettyStep) Quiet() {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.ended || s.quiet || s.r.active != s {
		return
	}
	s.r.stopTickerLocked()
	s.r.clearLiveLocked()
	// Freeze a static start marker so the user can see where this step
	// began; subprocess output will stream beneath it.
	writef(s.r.w, " %s %s\n", styleRunning.Render("·"), styleStep.Render(s.name))
	for _, d := range s.details {
		writeln(s.r.w, styleDetail.Render("     "+d))
	}
	s.quiet = true
}

func (s *prettyStep) Done() {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	s.r.stopTickerLocked()
	if !s.quiet {
		s.r.clearLiveLocked()
	}
	s.r.writeFrozenLocked(s, false, nil)
	if s.r.active == s {
		s.r.active = nil
	}
}

func (s *prettyStep) Fail(err error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	s.r.stopTickerLocked()
	if !s.quiet {
		s.r.clearLiveLocked()
	}
	s.r.writeFrozenLocked(s, true, err)
	if s.r.active == s {
		s.r.active = nil
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	}
}
