package deploy

import (
	"bytes"
	"io"
)

const (
	// redactMask replaces a secret wherever it appears in build output.
	redactMask = "***"
	// minRedactLen is the shortest value worth masking. Masking a one- or
	// two-character value would smear unrelated output into *** and make the
	// build log useless.
	minRedactLen = 4
	// maxRedactHold bounds the partial-line buffer. A subprocess that never
	// emits a newline must not grow it without limit.
	maxRedactHold = 32 << 10
)

// redactWriter masks known secret values in a subprocess output stream.
//
// Build args are resolved credentials, and a build step that fails will
// happily print the URL or command it was given (the MaxMind download URL
// carries the licence key in a query parameter). Everything the builder
// writes therefore passes through here on its way to the terminal and to the
// reporter's tail window.
//
// Output is held back to the last newline so a secret split across two writes
// is still matched. Values shorter than minRedactLen are ignored.
type redactWriter struct {
	w       io.Writer
	secrets [][]byte
	carry   int // bytes kept back on a forced flush, so no secret straddles it
	buf     []byte
}

// newRedactWriter wraps w. With no maskable secret it degrades to a plain
// pass-through, so the live tail window is not delayed by line buffering.
func newRedactWriter(w io.Writer, secrets []string) *redactWriter {
	rw := &redactWriter{w: w}
	for _, s := range secrets {
		if len(s) < minRedactLen {
			continue
		}
		rw.secrets = append(rw.secrets, []byte(s))
		if len(s) > rw.carry {
			rw.carry = len(s)
		}
	}
	return rw
}

// Write always reports the full length as written: a partial line is held in
// the buffer, not dropped, and callers must not see a short write.
func (rw *redactWriter) Write(p []byte) (int, error) {
	if len(rw.secrets) == 0 {
		return rw.w.Write(p)
	}

	rw.buf = append(rw.buf, p...)
	// A secret can never contain a newline (config rejects control
	// characters), so cutting at one can never split a match.
	if i := bytes.LastIndexByte(rw.buf, '\n'); i >= 0 {
		if err := rw.emit(i + 1); err != nil {
			return 0, err
		}
	}
	if len(rw.buf) > maxRedactHold {
		if err := rw.emit(len(rw.buf) - rw.carry); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes out whatever partial line is still buffered. Callers must
// invoke it once the subprocess has exited, otherwise a final line without a
// newline is lost.
func (rw *redactWriter) Flush() error {
	return rw.emit(len(rw.buf))
}

// emit masks and writes the first n buffered bytes, keeping the remainder.
func (rw *redactWriter) emit(n int) error {
	if n <= 0 {
		return nil
	}
	out := mask(rw.buf[:n], rw.secrets)
	rw.buf = append(rw.buf[:0], rw.buf[n:]...)
	_, err := rw.w.Write(out)
	return err
}

// mask returns a copy of b with every secret replaced.
func mask(b []byte, secrets [][]byte) []byte {
	out := append([]byte(nil), b...)
	for _, s := range secrets {
		out = bytes.ReplaceAll(out, s, []byte(redactMask))
	}
	return out
}
