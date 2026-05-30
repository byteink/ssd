package ui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// fakeClock returns deterministic times so elapsed values are predictable.
type fakeClock struct {
	start time.Time
	tick  time.Duration
	step  int
}

func (c *fakeClock) Now() time.Time {
	t := c.start.Add(time.Duration(c.step) * c.tick)
	c.step++
	return t
}

func newFakeClock(tick time.Duration) *fakeClock {
	return &fakeClock{
		start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		tick:  tick,
	}
}

func newTestPlain(buf *bytes.Buffer, tick time.Duration) *plainReporter {
	c := newFakeClock(tick)
	return &plainReporter{w: buf, now: c.Now}
}

func TestPlainReporter_HappyPath(t *testing.T) {
	var buf bytes.Buffer
	r := newTestPlain(&buf, 250*time.Millisecond)

	r.Header("Deploying web → byteink.main")
	r.Info("Version: 3 → 4")
	s := r.Step("rsync")
	s.Detail("transferred 142 files")
	s.Done()
	r.Close()

	got := buf.String()
	want := strings.Join([]string{
		"[+] Deploying web → byteink.main",
		"   Version: 3 → 4",
		" · rsync",
		"     transferred 142 files",
		" ✓ rsync  250ms",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("plain output mismatch:\nwant:\n%q\ngot:\n%q", want, got)
	}
}

func TestPlainReporter_Fail(t *testing.T) {
	var buf bytes.Buffer
	r := newTestPlain(&buf, time.Second)

	s := r.Step("build")
	s.Fail(errors.New("docker build exit 1"))
	r.Close()

	got := buf.String()
	if !strings.Contains(got, " ✗ build  ") {
		t.Errorf("missing fail marker: %q", got)
	}
	if !strings.Contains(got, "docker build exit 1") {
		t.Errorf("missing error text: %q", got)
	}
}

func TestPlainReporter_Warn(t *testing.T) {
	var buf bytes.Buffer
	r := newTestPlain(&buf, time.Second)
	r.Warn("cleanup skipped: %s", "no tags")
	r.Close()
	if got := buf.String(); !strings.Contains(got, "   ! cleanup skipped: no tags") {
		t.Errorf("warn line wrong: %q", got)
	}
}

func TestPlainReporter_DoubleEndIgnored(t *testing.T) {
	var buf bytes.Buffer
	r := newTestPlain(&buf, time.Second)
	s := r.Step("only-once")
	s.Done()
	s.Done() // must not emit a second line
	s.Fail(errors.New("ignored"))
	r.Close()
	if c := strings.Count(buf.String(), "only-once"); c != 2 { // header + done
		t.Errorf("step rendered %d times, want 2: %q", c, buf.String())
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{500 * time.Millisecond, "500ms"},
		{1500 * time.Millisecond, "1.5s"},
		{59 * time.Second, "59.0s"},
		{61 * time.Second, "1m01s"},
		{125 * time.Second, "2m05s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.in); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewSelectsPlainForNonTTY(t *testing.T) {
	// bytes.Buffer is not *os.File → must get plain.
	var buf bytes.Buffer
	if _, ok := New(&buf).(*plainReporter); !ok {
		t.Errorf("New on non-tty must return plain reporter")
	}
}

func TestDiscardSwallowsAll(t *testing.T) {
	r := Discard()
	r.Header("x")
	s := r.Step("x")
	s.Detail("x")
	s.Quiet()
	s.Done()
	r.Close()
	// no assertion — just must not panic / not write anywhere we can see
}

func TestPlainStreamIndentsLines(t *testing.T) {
	var buf bytes.Buffer
	r := newTestPlain(&buf, time.Second)
	s := r.Step("build")
	w := s.Stream(4)
	// Mixed CRLF and ANSI — both should be normalised.
	_, _ = w.Write([]byte("\x1b[1m#1 load\x1b[0m\n#2 transfer\r\n"))
	s.Done()
	r.Close()
	got := buf.String()
	if !strings.Contains(got, "     #1 load\n") {
		t.Errorf("missing first stream line: %q", got)
	}
	if !strings.Contains(got, "     #2 transfer\n") {
		t.Errorf("missing second stream line: %q", got)
	}
}

func TestPrettyStreamRingBufferDropsOldLines(t *testing.T) {
	// White-box: drive the writer directly so we can assert ring trim.
	s := &prettyStep{
		r:         &prettyReporter{w: &bytes.Buffer{}, now: time.Now, tickEvery: time.Second},
		streaming: true,
		tailMax:   3,
	}
	s.r.active = s
	w := &prettyStreamWriter{s: s}
	for i := 1; i <= 5; i++ {
		_, _ = fmt.Fprintf(w, "line-%d\n", i)
	}
	if got, want := len(s.tailLines), 3; got != want {
		t.Fatalf("tail kept %d lines, want %d: %v", got, want, s.tailLines)
	}
	if s.tailLines[0] != "line-3" || s.tailLines[2] != "line-5" {
		t.Errorf("tail content wrong: %v", s.tailLines)
	}
}

// A live repaint must never end with a newline. A trailing newline while
// the block sits on the bottom terminal row scrolls the screen on every
// 10Hz tick, dumping each spinner frame into scrollback (the "200 spinner
// lines for one build" bug). Repositioning instead uses \r + CSI nA.
func TestPrettyPaintNoTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	r := &prettyReporter{w: &buf, now: time.Now, tickEvery: time.Second}
	s := &prettyStep{r: r, name: "build", streaming: true, tailMax: 4}
	r.active = s
	r.paintLocked()
	if got := buf.String(); strings.HasSuffix(got, "\n") {
		t.Fatalf("live paint ended with newline (scrolls at screen bottom): %q", got)
	}

	// A second paint must walk the cursor back up, not append below.
	buf.Reset()
	r.paintLocked()
	if got := buf.String(); !strings.HasPrefix(got, "\r") {
		t.Fatalf("repaint must reposition with \\r, got: %q", got)
	}
}

// visibleWidth strips the repaint control prefix (\r + optional CSI nA) and
// any SGR styling, then returns the printed cell width of a painted line.
func visibleWidth(line string) int {
	return ansi.StringWidth(strings.TrimLeft(ansi.Strip(line), "\r"))
}

// A painted line wider than the terminal would wrap onto a second physical
// row, desyncing the cursor-up repaint math and flooding scrollback. Every
// painted line — header AND tail — must be clamped so one logical line is
// always exactly one physical row, using the full width-1 budget.
func TestPrettyPaintClampsToTerminalWidth(t *testing.T) {
	var buf bytes.Buffer
	const cols = 40
	r := &prettyReporter{w: &buf, now: time.Now, tickEvery: time.Second, width: func() int { return cols }}
	s := &prettyStep{r: r, name: strings.Repeat("n", 200), streaming: true, tailMax: 4}
	s.tailLines = []string{
		strings.Repeat("x", 200), // long ASCII: must truncate to exactly cols-1
		strings.Repeat("你", 100), // wide (2-cell) runes: must not overshoot
	}
	r.active = s
	r.paintLocked()

	out := buf.String()
	lines := strings.Split(out, "\n")
	if len(lines) != 3 { // header + 2 tail lines
		t.Fatalf("want 3 painted lines, got %d: %q", len(lines), out)
	}
	// Header (lines[0]) and the long ASCII tail (lines[1]) must use the full
	// width-1 budget: not wider (would wrap), not narrower (off-by-one slack).
	for i, want := 0, cols-1; i <= 1; i++ {
		if got := visibleWidth(lines[i]); got != want {
			t.Errorf("line %d width = %d, want exactly %d (width-1): %q", i, got, want, lines[i])
		}
	}
	// Wide runes can't always fill the last cell, so allow <= width-1 but
	// never wider — overshoot is the wrap bug.
	if got := visibleWidth(lines[2]); got > cols-1 {
		t.Errorf("wide-rune line width = %d, want <= %d: %q", got, cols-1, lines[2])
	}

	// Row-count integrity: with no line wrapping, the logical line count the
	// repaint walks back over must equal the physical rows painted.
	if r.liveLines != 3 {
		t.Fatalf("liveLines = %d, want 3 (physical rows)", r.liveLines)
	}
	// The next paint must reposition by exactly liveLines-1 rows. A wrong
	// count here is the desync that floods scrollback.
	buf.Reset()
	r.paintLocked()
	if up := "\x1b[2A"; !strings.Contains(buf.String(), up) {
		t.Errorf("repaint must walk cursor up %q (liveLines-1), got: %q", up, buf.String())
	}
}

// When the width is unknown (output is not a sized tty), clamping is skipped
// so piped/redirected output keeps full lines.
func TestPrettyPaintSkipsClampWhenWidthUnknown(t *testing.T) {
	long := strings.Repeat("x", 200)
	for _, tc := range []struct {
		name  string
		width func() int
	}{
		{"nil width", nil},
		{"zero width", func() int { return 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := &prettyReporter{w: &buf, now: time.Now, tickEvery: time.Second, width: tc.width}
			s := &prettyStep{r: r, name: "build", streaming: true, tailMax: 4}
			s.tailLines = []string{long}
			r.active = s
			r.paintLocked()
			if !strings.Contains(buf.String(), long) {
				t.Errorf("unknown width must not clamp; full line missing: %q", buf.String())
			}
		})
	}
}

func TestPlainStepQuietIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	r := newTestPlain(&buf, time.Second)
	s := r.Step("build")
	s.Quiet() // plain mode ignores it; output stays line-by-line
	s.Detail("transferred 1MB")
	s.Done()
	r.Close()
	got := buf.String()
	if !strings.Contains(got, " · build") || !strings.Contains(got, "     transferred 1MB") || !strings.Contains(got, " ✓ build  ") {
		t.Errorf("Quiet should not change plain output, got:\n%q", got)
	}
}
