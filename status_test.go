package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderStatus_Table(t *testing.T) {
	var buf bytes.Buffer
	rows := []remote.ServiceStatus{
		{Service: "backend", State: "running", Uptime: "18m", Version: "7", Ports: "8092→8090"},
		{Service: "kb", State: "running", Health: "healthy", Uptime: "2m", Version: "3"},
		{Service: "web", State: "exited", Uptime: "-", Version: "14"},
	}

	require.NoError(t, renderStatus(&buf, rows))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 4, "header + 3 rows")
	assert.Regexp(t, `^SERVICE\s+STATUS\s+UPTIME\s+VERSION\s+PORTS$`, lines[0])
	assert.Regexp(t, `^backend\s+running\s+18m\s+7\s+8092→8090$`, lines[1])
	assert.Regexp(t, `^kb\s+running \(healthy\)\s+2m\s+3\s+-$`, lines[2])
	assert.Regexp(t, `^web\s+exited\s+-\s+14\s+-$`, lines[3])
}

// The PORTS column is dropped entirely when nothing publishes a port (the
// common case on k3s, where traffic arrives through the Ingress) so the table
// stays narrow.
func TestRenderStatus_OmitsPortsColumnWhenUnused(t *testing.T) {
	var buf bytes.Buffer
	rows := []remote.ServiceStatus{
		{Service: "web", State: "running", Uptime: "5h", Version: "14"},
	}

	require.NoError(t, renderStatus(&buf, rows))

	assert.NotContains(t, buf.String(), "PORTS")
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Regexp(t, `^SERVICE\s+STATUS\s+UPTIME\s+VERSION$`, lines[0])
	assert.Regexp(t, `^web\s+running\s+5h\s+14$`, lines[1])
}

func TestRenderStatus_Empty(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderStatus(&buf, nil))
	assert.Equal(t, "No services running\n", buf.String())
}
