package completion

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn while replacing os.Stdout with a pipe and
// returns whatever fn wrote. The completion functions print directly
// to os.Stdout (matching the cobra-style contract); this is the
// least-invasive way to assert on their output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

func linesOf(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestComplete_NoArgs_ReturnsTopLevelCommands(t *testing.T) {
	out := captureStdout(t, func() { Complete(nil, nil) })
	lines := linesOf(out)
	for _, want := range []string{"deploy", "up", "down", "env", "secret", "completion", "version"} {
		if !contains(lines, want) {
			t.Errorf("top-level completion missing %q; got %v", want, lines)
		}
	}
}

func TestComplete_ServiceCommand_ReturnsServices(t *testing.T) {
	services := []string{"web", "api", "worker"}
	out := captureStdout(t, func() { Complete([]string{"deploy"}, services) })
	got := linesOf(out)
	if len(got) != len(services) {
		t.Fatalf("expected %d services, got %d (%v)", len(services), len(got), got)
	}
	for _, s := range services {
		if !contains(got, s) {
			t.Errorf("services missing %q; got %v", s, got)
		}
	}
}

func TestComplete_ServiceCommand_AfterServicePicked_ReturnsNothing(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"deploy", "web"}, []string{"web", "api"}) })
	if got := linesOf(out); len(got) != 0 {
		t.Errorf("expected no candidates after positional filled, got %v", got)
	}
}

func TestComplete_EnvSubcommand_FirstPositionalIsService(t *testing.T) {
	services := []string{"web", "api"}
	out := captureStdout(t, func() { Complete([]string{"env"}, services) })
	got := linesOf(out)
	if !contains(got, "web") || !contains(got, "api") {
		t.Errorf("env completion should offer services first, got %v", got)
	}
}

func TestComplete_EnvSubcommand_SecondPositionalIsAction(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"env", "web"}, []string{"web", "api"}) })
	got := linesOf(out)
	for _, want := range []string{"set", "list", "rm"} {
		if !contains(got, want) {
			t.Errorf("env actions missing %q; got %v", want, got)
		}
	}
}

func TestComplete_SecretSubcommand_SameShapeAsEnv(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"secret", "web"}, []string{"web"}) })
	got := linesOf(out)
	for _, want := range []string{"set", "list", "rm"} {
		if !contains(got, want) {
			t.Errorf("secret actions missing %q; got %v", want, got)
		}
	}
}

func TestComplete_ProvisionOffersCheck(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"provision"}, nil) })
	got := linesOf(out)
	if !contains(got, "check") {
		t.Errorf("expected provision to offer 'check', got %v", got)
	}
}

func TestComplete_PruneOffersFlags(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"prune"}, nil) })
	got := linesOf(out)
	for _, want := range []string{"--dry-run", "--images", "--all"} {
		if !contains(got, want) {
			t.Errorf("prune flags missing %q; got %v", want, got)
		}
	}
}

func TestComplete_CompletionSubcommand(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"completion"}, nil) })
	got := linesOf(out)
	for _, want := range []string{"bash", "zsh", "fish", "install"} {
		if !contains(got, want) {
			t.Errorf("completion subcommands missing %q; got %v", want, got)
		}
	}
}

func TestComplete_GlobalFlagsAreSkipped(t *testing.T) {
	// `ssd --config foo deploy <TAB>` should still see "deploy" as the command.
	out := captureStdout(t, func() { Complete([]string{"--config", "foo", "deploy"}, []string{"web"}) })
	got := linesOf(out)
	if !contains(got, "web") {
		t.Errorf("global flags before command should be skipped; got %v", got)
	}
}

func TestComplete_GlobalFlagWithEqualsIsSkipped(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"--config=foo", "deploy"}, []string{"web"}) })
	got := linesOf(out)
	if !contains(got, "web") {
		t.Errorf("--config=foo should be skipped; got %v", got)
	}
}

func TestComplete_UnknownCommand_ReturnsNothing(t *testing.T) {
	out := captureStdout(t, func() { Complete([]string{"frobnicate"}, []string{"web"}) })
	if got := linesOf(out); len(got) != 0 {
		t.Errorf("unknown command should yield no candidates, got %v", got)
	}
}

func TestScript_AllShellsReturnNonEmpty(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		s, err := Script(sh)
		if err != nil {
			t.Fatalf("Script(%q): %v", sh, err)
		}
		if !strings.Contains(s, "ssd __complete") {
			t.Errorf("%s script does not invoke `ssd __complete`", sh)
		}
	}
}

// Comma-separated service lists (`ssd deploy web,api`) must complete each
// item after the last comma. The candidate set comes from `ssd __complete`
// unchanged; each shell re-attaches the already-typed `prefix,` while
// filtering the tail. These assert the per-shell mechanism is wired.
func TestScript_CompletesCommaSeparatedServices(t *testing.T) {
	cases := map[string]string{
		"bash": `-P "${prefix}"`,        // compgen prepends the "web," prefix
		"zsh":  "compset -P '*,'",       // consume up-to-last-comma as fixed prefix
		"fish": "string match -q '*,*'", // detect a comma in the current token
	}
	for sh, want := range cases {
		s, err := Script(sh)
		if err != nil {
			t.Fatalf("Script(%q): %v", sh, err)
		}
		if !strings.Contains(s, want) {
			t.Errorf("%s script missing comma completion (%q)", sh, want)
		}
	}
}

func TestScript_UnknownShellErrors(t *testing.T) {
	if _, err := Script("powershell"); err == nil {
		t.Error("expected error for unknown shell")
	}
}

func TestInstallPath_KnownShells(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		p, err := InstallPath(sh)
		if err != nil {
			t.Fatalf("InstallPath(%q): %v", sh, err)
		}
		if p == "" {
			t.Errorf("InstallPath(%q) returned empty", sh)
		}
	}
}

func TestInstall_WritesScriptToInstallPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	for _, sh := range []string{"bash", "zsh", "fish"} {
		path, err := Install(sh)
		if err != nil {
			t.Fatalf("Install(%q): %v", sh, err)
		}
		want, _ := InstallPath(sh)
		if path != want {
			t.Errorf("Install(%q) wrote to %q, want %q", sh, path, want)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		expected, _ := Script(sh)
		if string(got) != expected {
			t.Errorf("Install(%q): file content does not match Script output", sh)
		}
	}
}

func TestDetectShell(t *testing.T) {
	cases := map[string]string{
		"/bin/bash":     "bash",
		"/usr/bin/zsh":  "zsh",
		"/usr/bin/fish": "fish",
		"/bin/sh":       "",
		"":              "",
	}
	for shellPath, want := range cases {
		t.Setenv("SHELL", shellPath)
		if got := DetectShell(); got != want {
			t.Errorf("DetectShell() with SHELL=%q = %q, want %q", shellPath, got, want)
		}
	}
}
