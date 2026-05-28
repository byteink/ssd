package ui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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
