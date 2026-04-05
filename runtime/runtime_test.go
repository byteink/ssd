package runtime

import (
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ComposeRuntime(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/web",
	}
	client := New("compose", cfg)
	require.NotNil(t, client)
}

func TestNew_K3sRuntime(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/web",
	}
	client := New("k3s", cfg)
	require.NotNil(t, client)
}

func TestNew_InvalidRuntime(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/web",
	}
	assert.Panics(t, func() {
		New("invalid", cfg)
	})
}
