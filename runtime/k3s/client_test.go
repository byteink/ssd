package k3s

import (
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
)

func TestNewClient(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/myapp",
	}

	client := NewClient(cfg)
	assert.NotNil(t, client)
	assert.Equal(t, "myapp", client.namespace)
}

func TestClient_ImplementsRemoteClient(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/myapp",
	}

	client := NewClient(cfg)
	// Compile-time check that Client satisfies RemoteClient
	var _ remote.RemoteClient = client
}
