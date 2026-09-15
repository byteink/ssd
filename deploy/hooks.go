package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/ui"
)

// hookErrTail caps how much of a failed hook's output is folded into the
// error. The live tail window already showed it scrolling; this is the
// post-mortem copy.
const hookErrTail = 2000

// PostDeploy runs the service's `post_deploy` hooks. Callers invoke it only
// after StartService/RolloutService returned nil — a CDN purge that fires
// before the rollout is repopulated by the old pod and worse than no purge.
//
// A failing hook fails the command (non-zero exit) even though the rollout
// cannot be undone: warn-only would leave a stale edge cache behind a green
// deploy, which is the exact bug the hook exists to prevent. The error says
// the service is live so the operator does not read it as a failed rollout.
//
// Unlike pre_deploy this also runs for pre-built (image:) services: they sync
// no build context, but they still deploy and still may need a purge.
func PostDeploy(ctx context.Context, r ui.Reporter, cfg *config.Config) error {
	if len(cfg.PostDeploy) == 0 {
		return nil
	}
	dir, err := filepath.Abs(cfg.Context)
	if err != nil {
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if err := runHooks(ctx, r, "post_deploy", cfg.PostDeploy, dir); err != nil {
		return fmt.Errorf("%s is deployed and live, but %w", cfg.Name, err)
	}
	return nil
}

// runHooks executes each command sequentially via `sh -c` with the working
// directory set to the build context, so hook scripts live next to ssd.yaml.
// The first non-zero exit aborts; the error names the field, the command and
// the tail of its output.
func runHooks(ctx context.Context, r ui.Reporter, field string, cmds []string, dir string) error {
	for _, c := range cmds {
		s := r.Step("Running " + field + ": " + c)
		// ponytail: full output buffered so a failure can report it after the
		// live tail window collapses. Bounded in practice by one hook's output.
		var buf bytes.Buffer
		w := io.MultiWriter(s.Stream(streamTailLines), &buf)

		cmd := exec.CommandContext(ctx, "sh", "-c", c)
		cmd.Dir = dir
		cmd.Stdout, cmd.Stderr = w, w

		if err := cmd.Run(); err != nil {
			s.Fail(err)
			return fmt.Errorf("%s command failed: %s: %w\n%s", field, c, err, lastChars(buf.String(), hookErrTail))
		}
		s.Done()
	}
	return nil
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
