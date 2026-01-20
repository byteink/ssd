package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_RejectsServerWithSemicolon(t *testing.T) {
	err := ValidateServer("host;whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid character")
}

func TestConfig_RejectsServerWithBackticks(t *testing.T) {
	err := ValidateServer("host`id`")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid character")
}

func TestConfig_RejectsNameWithDollar(t *testing.T) {
	err := ValidateName("app$(id)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid character")
}

func TestConfig_RejectsStackPathWithParentRef(t *testing.T) {
	err := ValidateStackPath("/../etc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestConfig_AcceptsValidName(t *testing.T) {
	err := ValidateName("my-app-123")
	require.NoError(t, err)
}

func TestConfig_AcceptsValidServer(t *testing.T) {
	err := ValidateServer("my.server.com")
	require.NoError(t, err)
}
