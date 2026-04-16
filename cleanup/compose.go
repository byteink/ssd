package cleanup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
)

// SSHRunner is the minimum surface of remote.RemoteClient that cleanup needs.
// Keeping it narrow lets callers pass any client (real or mock) without
// pulling the whole RemoteClient interface into this package.
type SSHRunner interface {
	SSH(ctx context.Context, command string) (string, error)
}

// ImageCleaner is implemented per runtime (compose, k3s) to list tags,
// remove images, and prune build cache / dangling images.
type ImageCleaner interface {
	ListTags(ctx context.Context, imageName string) ([]Tag, error)
	RemoveImage(ctx context.Context, imageRef string) error
	PruneBuildCache(ctx context.Context) error
	PruneDangling(ctx context.Context) error
}

// buildCacheMaxAge is the default threshold for pruning build cache.
// Anything not touched in the last 7 days is considered cold and reclaimable.
const buildCacheMaxAge = "168h"

// ssdImagePrefix is the required prefix for any image ssd is allowed to
// remove. Protects foreign images (nginx, postgres, user-pushed) from
// accidental deletion.
const ssdImagePrefix = "ssd-"

// ComposeCleaner implements ImageCleaner for the compose runtime via
// docker commands over SSH.
type ComposeCleaner struct {
	ssh SSHRunner
}

// NewComposeCleaner wires a compose cleaner to an SSH runner.
func NewComposeCleaner(ssh SSHRunner) *ComposeCleaner {
	return &ComposeCleaner{ssh: ssh}
}

// ListTags returns every tag of the given repository on the server.
// Numeric tags have Numeric populated; everything else (including <none>
// intermediate layers) is dropped.
func (c *ComposeCleaner) ListTags(ctx context.Context, imageName string) ([]Tag, error) {
	cmd := fmt.Sprintf("docker images --format '{{.Repository}}:{{.Tag}}' %s", shellescape.Quote(imageName))
	out, err := c.ssh.SSH(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", imageName, err)
	}
	return parseRepoTagLines(out, imageName), nil
}

// RemoveImage runs `docker rmi` against the given reference.
// Refuses any reference not prefixed with "ssd-" for safety.
func (c *ComposeCleaner) RemoveImage(ctx context.Context, imageRef string) error {
	if err := guardImageRef(imageRef); err != nil {
		return err
	}
	cmd := fmt.Sprintf("docker rmi %s", shellescape.Quote(imageRef))
	if _, err := c.ssh.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("remove image %s: %w", imageRef, err)
	}
	return nil
}

// PruneBuildCache runs `docker builder prune -af --filter until=168h`.
// Removes build cache entries untouched for at least 7 days.
func (c *ComposeCleaner) PruneBuildCache(ctx context.Context) error {
	cmd := fmt.Sprintf("docker builder prune -af --filter until=%s", buildCacheMaxAge)
	if _, err := c.ssh.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("prune build cache: %w", err)
	}
	return nil
}

// PruneDangling runs `docker image prune -f` to remove untagged images
// not referenced by any container.
func (c *ComposeCleaner) PruneDangling(ctx context.Context) error {
	if _, err := c.ssh.SSH(ctx, "docker image prune -f"); err != nil {
		return fmt.Errorf("prune dangling: %w", err)
	}
	return nil
}

// parseRepoTagLines turns raw `repo:tag` lines into Tag entries.
// Lines not matching the expected repo are ignored — name-filter is
// reinforced client-side for defense in depth.
func parseRepoTagLines(out, expectedRepo string) []Tag {
	var tags []Tag
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "<none>:<none>" {
			continue
		}
		repo, tag, ok := strings.Cut(line, ":")
		if !ok || repo != expectedRepo || tag == "<none>" {
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

// guardImageRef rejects anything not produced by ssd.
// Safety net — never trust callers to have pre-filtered.
func guardImageRef(ref string) error {
	if !strings.HasPrefix(ref, ssdImagePrefix) {
		return fmt.Errorf("refusing to remove non-ssd image %q", ref)
	}
	return nil
}
