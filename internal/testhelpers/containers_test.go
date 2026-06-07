package testhelpers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/byteink/ssd/remote"
)

// SSHConfigExecutor must satisfy remote.CommandExecutor so it can be passed
// to remote.NewClientWithExecutor in the integration and e2e suites. This
// is a compile-time guard: if the interface drifts, the build breaks here
// (in the always-compiled unit suite) instead of silently rotting behind
// the //go:build integration / e2e tags where CI never looks.
var _ remote.CommandExecutor = (*SSHConfigExecutor)(nil)

// TestSSHConfigExecutorSetOutput verifies SetOutput redirects RunInteractive
// output to the configured writer, mirroring RealExecutor. The e2e/integration
// deploy paths rely on this redirect (ui.Reporter's Stream tail window).
func TestSSHConfigExecutorSetOutput(t *testing.T) {
	exec := &SSHConfigExecutor{}

	var buf bytes.Buffer
	exec.SetOutput(&buf, &buf)

	// "echo" hits the default branch of injectSSHConfig (args unchanged) and
	// runs as a plain local command — no container or SSH required.
	if err := exec.RunInteractive(context.Background(), "echo", "hello-redirect"); err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}

	if got := buf.String(); got != "hello-redirect\n" {
		t.Fatalf("SetOutput did not redirect: got %q, want %q", got, "hello-redirect\n")
	}
}

// TestSSHConfigExecutorSetOutputNilRestoresDefault verifies passing nil
// restores the os.Stdout / os.Stderr default, matching RealExecutor.SetOutput.
func TestSSHConfigExecutorSetOutputNilRestoresDefault(t *testing.T) {
	exec := &SSHConfigExecutor{}

	var buf bytes.Buffer
	exec.SetOutput(&buf, &buf)
	exec.SetOutput(nil, nil) // restore default

	// With defaults restored the command still succeeds; output goes to the
	// process stdout/stderr (not buf), so buf stays empty.
	if err := exec.RunInteractive(context.Background(), "true"); err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}

	if buf.Len() != 0 {
		t.Fatalf("expected nil SetOutput to stop writing to buf, got %q", buf.String())
	}
}

// TestSSHConfigExecutorInjectsBashPipeline verifies the test SSH config is
// threaded into the inner ssh of a bash pipeline. Client.Rsync runs
// `bash -c "git archive … | ssh <server> …"`; without this the nested ssh
// ignores the per-test config and fails to resolve the container host.
func TestSSHConfigExecutorInjectsBashPipeline(t *testing.T) {
	exec := &SSHConfigExecutor{ConfigPath: "/tmp/ssh_config"}

	pipeline := "git -C /repo archive --format=tar HEAD | ssh testserver 'tar xf - -C /remote'"
	got := exec.injectSSHConfig("bash", []string{"-c", pipeline})

	want := "ssh -F /tmp/ssh_config testserver"
	if len(got) != 2 || !strings.Contains(got[1], want) {
		t.Fatalf("bash pipeline not injected: got %v, want inner %q", got, want)
	}
}
