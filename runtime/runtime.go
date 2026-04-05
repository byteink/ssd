package runtime

import (
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
