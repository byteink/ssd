package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBuildArgRef(t *testing.T) {
	tests := []struct {
		value    string
		wantKind string
		wantKey  string
		wantOK   bool
	}{
		{"${secret:MAXMIND_LICENSE_KEY}", "secret", "MAXMIND_LICENSE_KEY", true},
		{"${env:API_URL}", "env", "API_URL", true},
		{"stable", "", "", false},
		{"", "", "", false},
		{"$SHELL", "", "", false},
		// Malformed refs must not silently degrade to literals.
		{"${secret:}", "", "", false},
		{"${vault:KEY}", "", "", false},
		{"prefix${secret:KEY}", "", "", false},
		{"${secret:KEY}suffix", "", "", false},
		{"${secret:BAD-KEY}", "", "", false},
	}
	for _, tt := range tests {
		kind, key, ok := ParseBuildArgRef(tt.value)
		assert.Equal(t, tt.wantOK, ok, "ok for %q", tt.value)
		assert.Equal(t, tt.wantKind, kind, "kind for %q", tt.value)
		assert.Equal(t, tt.wantKey, key, "key for %q", tt.value)
	}
}

func TestValidateBuildArgs_AcceptsLiteralsAndRefs(t *testing.T) {
	err := validateBuildArgs(&Config{BuildArgs: map[string]string{
		"MAXMIND_ACCOUNT_ID":  "${secret:MAXMIND_ACCOUNT_ID}",
		"MAXMIND_LICENSE_KEY": "${secret:MAXMIND_LICENSE_KEY}",
		"API_URL":             "${env:API_URL}",
		"BUILD_CHANNEL":       "stable",
		"EMPTY_ON_PURPOSE":    "",
	}})
	require.NoError(t, err)
}

func TestValidateBuildArgs_RejectsBadKey(t *testing.T) {
	for _, key := range []string{"", "BAD KEY", "BAD-KEY", "1LEADING", "KEY;rm -rf /", "KEY$(id)"} {
		err := validateBuildArgs(&Config{BuildArgs: map[string]string{key: "x"}})
		require.Error(t, err, "key %q must be rejected", key)
		assert.Contains(t, err.Error(), "build_args")
	}
}

func TestValidateBuildArgs_RejectsMalformedRef(t *testing.T) {
	err := validateBuildArgs(&Config{BuildArgs: map[string]string{"K": "${vault:TOKEN}"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "K")
	assert.Contains(t, err.Error(), "${secret:KEY}")
}

func TestValidateBuildArgs_RejectsNewlineInValue(t *testing.T) {
	err := validateBuildArgs(&Config{BuildArgs: map[string]string{"K": "line1\nline2"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "K")
}

func TestValidateBuildArgs_RejectsOversizedValue(t *testing.T) {
	err := validateBuildArgs(&Config{BuildArgs: map[string]string{"K": strings.Repeat("x", maxBuildArgValue+1)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "K")
}

// A pre-built service never runs a build, so build_args there is a config
// mistake that would otherwise be silently ignored.
func TestValidateBuildArgs_RejectsOnPrebuiltImage(t *testing.T) {
	err := validateBuildArgs(&Config{
		Image:     "nginx:latest",
		BuildArgs: map[string]string{"K": "v"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image")
}

// Secrecy: a literal build arg may itself be a credential. No validation
// error may echo a value back to the terminal or a log.
func TestValidateBuildArgs_ErrorNeverContainsValue(t *testing.T) {
	const secret = "s3cr3t-license-key"
	for _, bad := range []map[string]string{
		{"BAD KEY": secret},
		{"K": secret + "\n"},
		{"K": "${vault:" + secret + "}"},
	} {
		err := validateBuildArgs(&Config{BuildArgs: bad})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secret, "error leaked a build arg value")
	}
}

func TestGetService_ResolvesBuildArgs(t *testing.T) {
	root, err := LoadFromBytes([]byte(
		"server: myserver\n" +
			"services:\n" +
			"  api:\n" +
			"    build_args:\n" +
			"      MAXMIND_ACCOUNT_ID: ${secret:MAXMIND_ACCOUNT_ID}\n" +
			"      BUILD_CHANNEL: stable\n"))
	require.NoError(t, err)

	cfg, err := root.GetService("api")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"MAXMIND_ACCOUNT_ID": "${secret:MAXMIND_ACCOUNT_ID}",
		"BUILD_CHANNEL":      "stable",
	}, cfg.BuildArgs)
}

func TestGetService_InvalidBuildArgsRejected(t *testing.T) {
	root, err := LoadFromBytes([]byte(
		"server: myserver\n" +
			"services:\n" +
			"  api:\n" +
			"    build_args:\n" +
			"      TOKEN: ${vault:TOKEN}\n"))
	require.NoError(t, err)

	_, err = root.GetService("api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TOKEN")
}

// Env overlays must be able to carry different build args per environment:
// keys the overlay names win, keys it omits are inherited from the base.
func TestResolve_EnvOverlayMergesBuildArgs(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".ssd"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".ssd", "ssd.yaml"), []byte(
		"server: base\n"+
			"services:\n"+
			"  api:\n"+
			"    build_args:\n"+
			"      BUILD_CHANNEL: dev\n"+
			"      MAXMIND_ACCOUNT_ID: ${secret:MAXMIND_ACCOUNT_ID}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".ssd", "ssd.prod.yaml"), []byte(
		"services:\n"+
			"  api:\n"+
			"    build_args:\n"+
			"      BUILD_CHANNEL: stable\n"), 0644))

	chdir(t, tmpDir)
	cfg, _, err := Resolve("", "prod")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"BUILD_CHANNEL":      "stable",
		"MAXMIND_ACCOUNT_ID": "${secret:MAXMIND_ACCOUNT_ID}",
	}, cfg.Services["api"].BuildArgs)
}
