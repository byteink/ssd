package remote

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"al.essio.dev/pkg/shellescape"
	"github.com/byteink/ssd/config"
)

// RemoteClient defines the interface for remote operations
type RemoteClient interface {
	SSH(ctx context.Context, command string) (string, error)
	SSHInteractive(ctx context.Context, command string) error
	Rsync(ctx context.Context, localPath, remotePath string) error
	GetCurrentVersion(ctx context.Context) (int, error)
	BuildImage(ctx context.Context, buildDir string, version int) error
	UpdateCompose(ctx context.Context, version int) error
	RestartStack(ctx context.Context) error
	GetContainerStatus(ctx context.Context) (string, error)
	GetLogs(ctx context.Context, follow bool, tail int) error
	Cleanup(ctx context.Context, path string) error
	MakeTempDir(ctx context.Context) (string, error)
}

// Ensure Client implements RemoteClient
var _ RemoteClient = (*Client)(nil)

// Client handles remote operations via SSH
type Client struct {
	server   string
	cfg      *config.Config
	executor CommandExecutor
}

// NewClient creates a new remote client with the default executor
func NewClient(cfg *config.Config) *Client {
	return &Client{
		server:   cfg.Server,
		cfg:      cfg,
		executor: NewRealExecutor(),
	}
}

// NewClientWithExecutor creates a client with a custom executor (for testing)
func NewClientWithExecutor(cfg *config.Config, executor CommandExecutor) *Client {
	return &Client{
		server:   cfg.Server,
		cfg:      cfg,
		executor: executor,
	}
}

// SSH executes a command on the remote server
func (c *Client) SSH(ctx context.Context, command string) (string, error) {
	output, err := c.executor.Run(ctx, "ssh", c.server, command)
	if err != nil {
		return "", fmt.Errorf("ssh command failed: %w", err)
	}
	return output, nil
}

// SSHInteractive runs an SSH command with output streamed to terminal
func (c *Client) SSHInteractive(ctx context.Context, command string) error {
	return c.executor.RunInteractive(ctx, "ssh", c.server, command)
}

// Rsync syncs local directory to remote server
func (c *Client) Rsync(ctx context.Context, localPath, remotePath string) error {
	// Build rsync command with common excludes
	excludes := []string{
		".git",
		"node_modules",
		".next",
		".DS_Store",
		"*.log",
	}

	args := []string{
		"-avz",
		"--delete",
		"--progress",
	}

	for _, ex := range excludes {
		args = append(args, "--exclude", ex)
	}

	// Ensure local path ends with / to copy contents, not the directory itself
	if !strings.HasSuffix(localPath, "/") {
		localPath += "/"
	}

	args = append(args, localPath, fmt.Sprintf("%s:%s", c.server, remotePath))

	return c.executor.RunInteractive(ctx, "rsync", args...)
}

// parseVersionFromContent extracts the version number from compose.yaml content
// Returns 0, nil if no version found; error on parse failure
func parseVersionFromContent(content, appName string) (int, error) {
	// Validate inputs are valid UTF-8 to prevent regexp compilation panics
	if !utf8.ValidString(appName) || !utf8.ValidString(content) {
		return 0, nil
	}

	// Match ssd-{name}:{version}
	imageName := fmt.Sprintf("ssd-%s", appName)
	re := regexp.MustCompile(fmt.Sprintf(`image:\s*%s:(\d+)`, regexp.QuoteMeta(imageName)))
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return strconv.Atoi(matches[1])
	}

	return 0, nil
}

// GetCurrentVersion reads the current image version from compose.yaml on the server
func (c *Client) GetCurrentVersion(ctx context.Context) (int, error) {
	composePath := filepath.Join(c.cfg.StackPath(), "compose.yaml")
	output, err := c.SSH(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shellescape.Quote(composePath)))
	if err != nil {
		return 0, nil // No compose.yaml means version 0
	}

	return parseVersionFromContent(output, c.cfg.Name)
}

// BuildImage builds a Docker image on the remote server
func (c *Client) BuildImage(ctx context.Context, buildDir string, version int) error {
	imageTag := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)

	// Build command with dockerfile path relative to build context
	dockerfile := c.cfg.Dockerfile
	if strings.HasPrefix(dockerfile, "./") {
		dockerfile = dockerfile[2:]
	}

	cmd := fmt.Sprintf("cd %s && docker build -t %s -f %s .", shellescape.Quote(buildDir), shellescape.Quote(imageTag), shellescape.Quote(dockerfile))
	return c.SSHInteractive(ctx, cmd)
}

// UpdateCompose updates the image tag in compose.yaml
func (c *Client) UpdateCompose(ctx context.Context, version int) error {
	composePath := filepath.Join(c.cfg.StackPath(), "compose.yaml")
	newImage := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)

	// Read current compose.yaml
	output, err := c.SSH(ctx, fmt.Sprintf("cat %s", shellescape.Quote(composePath)))
	if err != nil {
		return fmt.Errorf("failed to read compose.yaml: %w", err)
	}

	// Replace image tag
	oldImagePattern := regexp.MustCompile(`(image:\s*)(ssd-` + regexp.QuoteMeta(c.cfg.Name) + `):(\d+)`)
	newContent := oldImagePattern.ReplaceAllString(output, fmt.Sprintf("${1}%s", newImage))

	// Write back
	escapedContent := strings.ReplaceAll(newContent, "'", "'\\''")
	cmd := fmt.Sprintf("echo '%s' > %s", escapedContent, shellescape.Quote(composePath))
	_, err = c.SSH(ctx, cmd)
	return err
}

// RestartStack runs docker compose up -d in the stack directory
func (c *Client) RestartStack(ctx context.Context) error {
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker compose up -d", shellescape.Quote(stackPath))
	return c.SSHInteractive(ctx, cmd)
}

// GetContainerStatus returns the status of the container
func (c *Client) GetContainerStatus(ctx context.Context) (string, error) {
	// Try to find container by compose project name
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker compose ps --format '{{.Name}}\\t{{.Status}}'", shellescape.Quote(stackPath))
	return c.SSH(ctx, cmd)
}

// GetLogs returns logs from the container
func (c *Client) GetLogs(ctx context.Context, follow bool, tail int) error {
	stackPath := c.cfg.StackPath()

	tailArg := ""
	if tail > 0 {
		tailArg = fmt.Sprintf("--tail %d", tail)
	}

	followArg := ""
	if follow {
		followArg = "-f"
	}

	cmd := fmt.Sprintf("cd %s && docker compose logs %s %s", shellescape.Quote(stackPath), followArg, tailArg)
	return c.SSHInteractive(ctx, cmd)
}

// Cleanup removes a directory on the remote server
func (c *Client) Cleanup(ctx context.Context, path string) error {
	if err := ValidateTempPath(path); err != nil {
		return err
	}
	_, err := c.SSH(ctx, fmt.Sprintf("rm -rf %s", shellescape.Quote(path)))
	return err
}

// MakeTempDir creates a temporary directory on the remote server
func (c *Client) MakeTempDir(ctx context.Context) (string, error) {
	output, err := c.SSH(ctx, "mktemp -d")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// ValidateTempPath validates that a path is safe for temporary operations
func ValidateTempPath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	normalized := filepath.Clean(path)

	if !strings.HasPrefix(normalized, "/tmp/") {
		return fmt.Errorf("path must start with /tmp/")
	}

	if strings.Contains(normalized, "..") {
		return fmt.Errorf("path must not contain ..")
	}

	return nil
}
