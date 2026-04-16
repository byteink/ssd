package cleanup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
)

// K3sCleaner implements ImageCleaner for the k3s runtime via nerdctl
// and buildctl over SSH.
//
// Differences from the compose runtime (verified on byteink.main):
//   - `nerdctl images <name>` name-filter returns nothing → list all,
//     filter by prefix in Go.
//   - nerdctl emits many `<none>:<none>` intermediate layer lines that
//     must be dropped.
//   - Build cache lives in buildkitd (not containerd), so pruning uses
//     `buildctl --addr unix:///run/buildkit/buildkitd.sock prune`, which
//     requires sudo.
type K3sCleaner struct {
	ssh SSHRunner
}

const buildkitSocket = "unix:///run/buildkit/buildkitd.sock"

// NewK3sCleaner wires a k3s cleaner to an SSH runner.
func NewK3sCleaner(ssh SSHRunner) *K3sCleaner {
	return &K3sCleaner{ssh: ssh}
}

// ListTags lists all images in the k8s.io namespace and filters to the
// requested repository client-side. Numeric tags are parsed; everything
// else is dropped.
func (c *K3sCleaner) ListTags(ctx context.Context, imageName string) ([]Tag, error) {
	out, err := c.ssh.SSH(ctx, "nerdctl --namespace k8s.io images --format '{{.Repository}}:{{.Tag}}'")
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", imageName, err)
	}
	return parseK3sRepoTags(out, imageName), nil
}

// RemoveImage runs `nerdctl rmi` in the k8s.io namespace.
// Refuses non-ssd refs for safety.
func (c *K3sCleaner) RemoveImage(ctx context.Context, imageRef string) error {
	if err := guardImageRef(imageRef); err != nil {
		return err
	}
	cmd := fmt.Sprintf("nerdctl --namespace k8s.io rmi %s", shellescape.Quote(imageRef))
	if _, err := c.ssh.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("remove image %s: %w", imageRef, err)
	}
	return nil
}

// PruneBuildCache runs `sudo buildctl prune --keep-duration 168h` against
// the buildkit daemon socket. Sudo is required — buildkitd.sock is
// root-owned on byteink.main.
func (c *K3sCleaner) PruneBuildCache(ctx context.Context) error {
	cmd := fmt.Sprintf("sudo buildctl --addr %s prune --keep-duration %s", buildkitSocket, buildCacheMaxAge)
	if _, err := c.ssh.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("prune build cache: %w", err)
	}
	return nil
}

// PruneDangling runs `nerdctl image prune -f` in the k8s.io namespace.
func (c *K3sCleaner) PruneDangling(ctx context.Context) error {
	if _, err := c.ssh.SSH(ctx, "nerdctl --namespace k8s.io image prune -f"); err != nil {
		return fmt.Errorf("prune dangling: %w", err)
	}
	return nil
}

// parseK3sRepoTags filters nerdctl image output to tags belonging to the
// requested repo. Drops <none>:<none>, :<none>, and foreign repos.
func parseK3sRepoTags(out, expectedRepo string) []Tag {
	var tags []Tag
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "<none>:<none>" {
			continue
		}
		// nerdctl may emit "ghcr.io/foo/bar:<tag>" — split on the last colon.
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		repo, tag := line[:idx], line[idx+1:]
		if repo != expectedRepo || tag == "<none>" {
			continue
		}
		if n, err := strconv.Atoi(tag); err == nil && n > 0 {
			tags = append(tags, Tag{Numeric: n})
			continue
		}
		tags = append(tags, Tag{Raw: tag})
	}
	return tags
}
