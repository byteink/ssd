package testhelpers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// DinDContainer manages a Docker-in-Docker container for testing
type DinDContainer struct {
	container testcontainers.Container
	host      string
	port      nat.Port
}

// StartDinDContainer initializes and starts a Docker-in-Docker container
func StartDinDContainer(ctx context.Context, t *testing.T) (*DinDContainer, error) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "docker:dind",
		ExposedPorts: []string{"2375/tcp"},
		Privileged:   true,
		WaitingFor:   wait.ForListeningPort("2375/tcp"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start DinD container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("failed to terminate container: %v", termErr)
		}
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "2375")
	if err != nil {
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("failed to terminate container: %v", termErr)
		}
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	return &DinDContainer{
		container: container,
		host:      host,
		port:      mappedPort,
	}, nil
}

// RunDocker executes a docker command inside the DinD container
func (d *DinDContainer) RunDocker(ctx context.Context, command string) (string, error) {
	if d.container == nil {
		return "", fmt.Errorf("container not initialized")
	}

	exitCode, reader, err := d.container.Exec(ctx, []string{"docker", command})
	if err != nil {
		return "", fmt.Errorf("failed to execute docker command: %w", err)
	}

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, reader); err != nil {
		return "", fmt.Errorf("failed to read command output: %w", err)
	}
	output := buf.String()

	if exitCode != 0 {
		return "", fmt.Errorf("docker command failed with exit code %d: %s", exitCode, output)
	}

	return output, nil
}

// Cleanup terminates the DinD container
func (d *DinDContainer) Cleanup(ctx context.Context) error {
	if d.container == nil {
		return nil
	}

	if err := d.container.Terminate(ctx); err != nil {
		return fmt.Errorf("failed to terminate container: %w", err)
	}

	return nil
}

// Host returns the host address of the DinD container
func (d *DinDContainer) Host() string {
	return d.host
}

// Port returns the mapped port of the DinD container
func (d *DinDContainer) Port() nat.Port {
	return d.port
}

// DockerHost returns the DOCKER_HOST environment variable value for connecting to this DinD instance
func (d *DinDContainer) DockerHost() string {
	return fmt.Sprintf("tcp://%s:%s", d.host, d.port.Port())
}
