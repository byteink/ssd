package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactWriter_MasksSecretInLine(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, []string{"abc123xyz"})

	n, err := w.Write([]byte("downloading with key=abc123xyz ok\n"))
	require.NoError(t, err)
	assert.Equal(t, len("downloading with key=abc123xyz ok\n"), n, "must report the full write")
	require.NoError(t, w.Flush())

	assert.NotContains(t, out.String(), "abc123xyz")
	assert.Contains(t, out.String(), "downloading with key="+redactMask+" ok")
}

// Subprocess output arrives in arbitrary chunks; a secret split across two
// writes must still be masked.
func TestRedactWriter_MasksSecretSplitAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, []string{"abc123xyz"})

	_, err := w.Write([]byte("key=abc1"))
	require.NoError(t, err)
	assert.Empty(t, out.String(), "a partial line must be held back, not leaked")

	_, err = w.Write([]byte("23xyz done\n"))
	require.NoError(t, err)
	require.NoError(t, w.Flush())

	assert.NotContains(t, out.String(), "abc123xyz")
	assert.Contains(t, out.String(), "key="+redactMask+" done")
}

// A build can fail mid-line; whatever is buffered must still be masked.
func TestRedactWriter_FlushMasksTrailingPartialLine(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, []string{"abc123xyz"})

	_, err := w.Write([]byte("no newline abc123xyz"))
	require.NoError(t, err)
	require.NoError(t, w.Flush())

	assert.NotContains(t, out.String(), "abc123xyz")
	assert.Contains(t, out.String(), "no newline "+redactMask)
}

func TestRedactWriter_MasksEverySecret(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, []string{"account-1234", "license-abcd"})

	_, err := w.Write([]byte("u=account-1234 p=license-abcd\n"))
	require.NoError(t, err)
	require.NoError(t, w.Flush())

	assert.Equal(t, "u="+redactMask+" p="+redactMask+"\n", out.String())
}

// Nothing to hide: output must pass straight through, unbuffered, so the
// live tail window stays responsive.
func TestRedactWriter_NoSecretsPassesThrough(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, nil)

	_, err := w.Write([]byte("partial"))
	require.NoError(t, err)
	assert.Equal(t, "partial", out.String())
}

// Masking a 1-2 character value would smear unrelated output into ***, so
// short values are left alone.
func TestRedactWriter_IgnoresTooShortSecrets(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, []string{"1"})

	_, err := w.Write([]byte("version 1 built\n"))
	require.NoError(t, err)
	require.NoError(t, w.Flush())

	assert.Equal(t, "version 1 built\n", out.String())
}

// A subprocess that never emits a newline must not grow the hold buffer
// without bound — and the secret must survive the forced flush intact.
func TestRedactWriter_BoundedHoldStillMasks(t *testing.T) {
	var out bytes.Buffer
	w := newRedactWriter(&out, []string{"abc123xyz"})

	_, err := w.Write([]byte(strings.Repeat("x", maxRedactHold+1)))
	require.NoError(t, err)
	assert.NotEmpty(t, out.String(), "the buffer must flush instead of growing forever")

	// The secret straddles the forced flush boundary.
	_, err = w.Write([]byte("abc"))
	require.NoError(t, err)
	_, err = w.Write([]byte("123xyz\n"))
	require.NoError(t, err)
	require.NoError(t, w.Flush())

	assert.NotContains(t, out.String(), "abc123xyz")
	assert.Contains(t, out.String(), redactMask)
}
