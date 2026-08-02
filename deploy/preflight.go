package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/ui"
)

// preDeployErrTail caps how much of a failed hook's output is folded into
// the error. The live tail window already showed it scrolling; this is the
// post-mortem copy.
const preDeployErrTail = 2000

// preflight runs the local, pre-sync checks for a service: the `pre_deploy`
// hooks first, then the `require_clean` git check.
//
// The order is the entire point of the pair. Hooks regenerate committed
// artifacts (generated HTML, embedded assets); the clean check then catches
// "you regenerated and did not commit". Reversed, the pair is useless —
// Rsync ships `git archive HEAD`, so uncommitted work never reaches the
// server and the deploy succeeds with stale content.
func preflight(ctx context.Context, r ui.Reporter, cfg *config.Config) error {
	dir, err := filepath.Abs(cfg.Context)
	if err != nil {
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if err := runPreDeploy(ctx, r, cfg.PreDeploy, dir); err != nil {
		return err
	}
	return checkClean(ctx, r, cfg, dir)
}

// runPreDeploy executes each command sequentially with the working directory
// set to the build context. The first non-zero exit aborts the deploy.
func runPreDeploy(ctx context.Context, r ui.Reporter, cmds []string, dir string) error {
	for _, c := range cmds {
		s := r.Step("Running pre_deploy: " + c)
		// ponytail: full output buffered so a failure can report it after the
		// live tail window collapses. Bounded in practice by one hook's output.
		var buf bytes.Buffer
		w := io.MultiWriter(s.Stream(streamTailLines), &buf)

		cmd := exec.CommandContext(ctx, "sh", "-c", c)
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = w, w

		if err := cmd.Run(); err != nil {
			s.Fail(err)
			return fmt.Errorf("pre_deploy command failed: %s: %w\n%s", c, err, lastChars(buf.String(), preDeployErrTail))
		}
		s.Done()
	}
	return nil
}

// checkClean fails (require_clean) or warns (default) when the build context
// has uncommitted tracked changes, which `git archive HEAD` would not ship.
func checkClean(ctx context.Context, r ui.Reporter, cfg *config.Config, dir string) error {
	dirty, err := dirtyTrackedFiles(ctx, dir)
	if err != nil {
		if cfg.CleanRequired() {
			return fmt.Errorf("require_clean: %w", err)
		}
		// Not a git repo (or git missing) — Rsync reports that with a better
		// message a moment later. Nothing useful to warn about here.
		return nil
	}
	if len(dirty) == 0 {
		return nil
	}

	list := "  " + strings.Join(dirty, "\n  ")
	if cfg.CleanRequired() {
		return fmt.Errorf("require_clean: uncommitted tracked changes in %s:\n%s\n"+
			"deploys ship `git archive HEAD` — commit or stash these first", dir, list)
	}
	r.Warn("uncommitted tracked changes will NOT be deployed (deploys ship `git archive HEAD`):\n%s", list)
	return nil
}

// dirtyTrackedFiles returns the porcelain status lines for tracked changes
// (staged or unstaged) under dir. Untracked files are excluded — `git archive`
// never shipped them and that is already understood. A moved submodule pointer
// shows up as a modified path, which is exactly the case that must fail.
func dirtyTrackedFiles(ctx context.Context, dir string) ([]string, error) {
	// Pathspec "." scopes the check to the build context, not the whole repo.
	// --porcelain paths are always relative to the repo root.
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--", ".")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status failed in %s: %w", dir, err)
	}

	var dirty []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) < 4 || strings.HasPrefix(line, "??") {
			continue
		}
		dirty = append(dirty, line)
	}
	return dirty, nil
}

// lastChars returns the final n runes of s, marked with an ellipsis when
// truncated. Rune-based so a cut never splits a multi-byte character.
func lastChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}
