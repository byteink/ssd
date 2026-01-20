package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestProperty_ServiceInheritance(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rootServer := rapid.StringMatching(`^[a-zA-Z][a-zA-Z0-9._-]{0,252}$`).Draw(t, "rootServer")
		svcServer := rapid.OneOf(
			rapid.Just(""),
			rapid.StringMatching(`^[a-zA-Z][a-zA-Z0-9._-]{0,252}$`),
		).Draw(t, "svcServer")

		cfg := &RootConfig{Server: rootServer, Services: map[string]*Config{
			"web": {Server: svcServer},
		}}

		svc, err := cfg.GetService("web")
		require.NoError(t, err)

		// Property: non-empty service value overrides root
		if svcServer != "" {
			require.Equal(t, svcServer, svc.Server)
		} else {
			require.Equal(t, rootServer, svc.Server)
		}
	})
}

func TestProperty_StackInheritance(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rootStack := rapid.OneOf(
			rapid.Just(""),
			rapid.StringMatching(`^/[a-zA-Z0-9/_-]{1,100}$`),
		).Draw(t, "rootStack")
		svcStack := rapid.OneOf(
			rapid.Just(""),
			rapid.StringMatching(`^/[a-zA-Z0-9/_-]{1,100}$`),
		).Draw(t, "svcStack")

		cfg := &RootConfig{
			Server: "testserver",
			Stack:  rootStack,
			Services: map[string]*Config{
				"web": {Stack: svcStack},
			},
		}

		svc, err := cfg.GetService("web")
		require.NoError(t, err)

		// Property: non-empty service stack overrides root stack
		if svcStack != "" {
			require.Equal(t, svcStack, svc.Stack)
		} else if rootStack != "" {
			require.Equal(t, rootStack, svc.Stack)
		}
	})
}

func TestProperty_NameDefaults(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		serviceName := rapid.StringMatching(`^[a-zA-Z][a-zA-Z0-9_-]{0,127}$`).Draw(t, "serviceName")

		cfg := &RootConfig{
			Server: "testserver",
			Services: map[string]*Config{
				serviceName: {},
			},
		}

		svc, _ := cfg.GetService(serviceName)

		// Property: service name defaults to map key when Name field is empty
		require.Equal(t, serviceName, svc.Name)
	})
}

func TestProperty_InheritanceIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		server := rapid.StringMatching(`^[a-zA-Z][a-zA-Z0-9._-]{0,252}$`).Draw(t, "server")
		stack := rapid.StringMatching(`^/[a-zA-Z0-9/_-]+$`).Draw(t, "stack")

		cfg := &RootConfig{
			Server: server,
			Stack:  stack,
			Services: map[string]*Config{
				"web": {},
			},
		}

		svc1, err1 := cfg.GetService("web")
		svc2, err2 := cfg.GetService("web")

		// Property: calling GetService multiple times returns identical results
		if err1 == nil && err2 == nil {
			require.Equal(t, svc1.Server, svc2.Server)
			require.Equal(t, svc1.Stack, svc2.Stack)
			require.Equal(t, svc1.Name, svc2.Name)
		}
	})
}
