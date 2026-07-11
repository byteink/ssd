package runtime

import (
	"context"
	"fmt"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/remote"
	"github.com/byteink/ssd/runtime/k3s"
)

// New returns a RemoteClient for the given runtime.
// Panics on unknown runtime — callers should validate via config.ValidateRuntime first.
func New(rt string, cfg *config.Config) remote.RemoteClient {
	switch rt {
	case "compose":
		return remote.NewClient(cfg)
	case "k3s":
		return k3s.NewClient(cfg)
	default:
		panic(fmt.Sprintf("unknown runtime: %s", rt))
	}
}

// StatusClient reports what is running on the server. It is kept out of
// remote.RemoteClient because internal/testhelpers cannot import remote (its
// mocks are used by remote's own in-package tests), and only `ssd status`
// needs it.
type StatusClient interface {
	GetStatus(ctx context.Context, serviceName string) ([]remote.ServiceStatus, error)
}

// NewStatus returns a StatusClient for the given runtime. Same contract as New.
func NewStatus(rt string, cfg *config.Config) StatusClient {
	switch rt {
	case "compose":
		return remote.NewClient(cfg)
	case "k3s":
		return k3s.NewClient(cfg)
	default:
		panic(fmt.Sprintf("unknown runtime: %s", rt))
	}
}
