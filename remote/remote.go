package remote

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/byteink/ssd/config"
)

// RemoteClient defines the interface for remote operations
type RemoteClient interface {
	SSH(command string) (string, error)
	SSHInteractive(command string) error
	Rsync(localPath, remotePath string) error
	GetCurrentVersion() (int, error)
	BuildImage(buildDir string, version int) error
	UpdateCompose(version int) error
	RestartStack() error
	GetContainerStatus() (string, error)
	GetLogs(follow bool, tail int) error
	Cleanup(path string) error
	MakeTempDir() (string, error)
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
func (c *Client) SSH(command string) (string, error) {
	output, err := c.executor.Run("ssh", c.server, command)
	if err != nil {
		return "", fmt.Errorf("ssh command failed: %w", err)
	}
	return output, nil
}

// SSHInteractive runs an SSH command with output streamed to terminal
func (c *Client) SSHInteractive(command string) error {
	return c.executor.RunInteractive("ssh", c.server, command)
}

// Rsync syncs local directory to remote server
func (c *Client) Rsync(localPath, remotePath string) error {
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

	return c.executor.RunInteractive("rsync", args...)
}

// GetCurrentVersion reads the current image version from compose.yaml on the server
func (c *Client) GetCurrentVersion() (int, error) {
	composePath := filepath.Join(c.cfg.StackPath(), "compose.yaml")
	output, err := c.SSH(fmt.Sprintf("cat %s 2>/dev/null || echo ''", composePath))
	if err != nil {
		return 0, nil // No compose.yaml means version 0
	}

	// Parse image tag from compose.yaml
	// Looking for pattern: image: ssd-{name}:{version} or ssd-{name}:{version}
	imageName := c.cfg.ImageName()

	// Try new format first: ssd-{name}:{version}
	re := regexp.MustCompile(fmt.Sprintf(`image:\s*%s:(\d+)`, regexp.QuoteMeta(imageName)))
	matches := re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		return strconv.Atoi(matches[1])
	}

	// Try : ssd-{name}:{version}
	legacyName := fmt.Sprintf("ssd-%s", c.cfg.Name)
	re = regexp.MustCompile(fmt.Sprintf(`image:\s*%s:(\d+)`, regexp.QuoteMeta(legacyName)))
	matches = re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		return strconv.Atoi(matches[1])
	}

	return 0, nil
}

// BuildImage builds a Docker image on the remote server
func (c *Client) BuildImage(buildDir string, version int) error {
	imageTag := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)

	// Build command with dockerfile path relative to build context
	dockerfile := c.cfg.Dockerfile
	if strings.HasPrefix(dockerfile, "./") {
		dockerfile = dockerfile[2:]
	}

	cmd := fmt.Sprintf("cd %s && docker build -t %s -f %s .", buildDir, imageTag, dockerfile)
	return c.SSHInteractive(cmd)
}

// UpdateCompose updates the image tag in compose.yaml
func (c *Client) UpdateCompose(version int) error {
	composePath := filepath.Join(c.cfg.StackPath(), "compose.yaml")
	newImage := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)

	// Read current compose.yaml
	output, err := c.SSH(fmt.Sprintf("cat %s", composePath))
	if err != nil {
		return fmt.Errorf("failed to read compose.yaml: %w", err)
	}

	// Replace image tag - handle both old and new naming conventions
	// Match any image line for the app service
	oldImagePattern := regexp.MustCompile(`(image:\s*)(ssd-` + regexp.QuoteMeta(c.cfg.Name) + `|ssd-` + regexp.QuoteMeta(c.cfg.Name) + `):(\d+)`)
	newContent := oldImagePattern.ReplaceAllString(output, fmt.Sprintf("${1}%s", newImage))

	// Write back
	escapedContent := strings.ReplaceAll(newContent, "'", "'\\''")
	cmd := fmt.Sprintf("echo '%s' > %s", escapedContent, composePath)
	_, err = c.SSH(cmd)
	return err
}

// RestartStack runs docker compose up -d in the stack directory
func (c *Client) RestartStack() error {
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker compose up -d", stackPath)
	return c.SSHInteractive(cmd)
}

// GetContainerStatus returns the status of the container
func (c *Client) GetContainerStatus() (string, error) {
	// Try to find container by compose project name
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker compose ps --format '{{.Name}}\\t{{.Status}}'", stackPath)
	return c.SSH(cmd)
}

// GetLogs returns logs from the container
func (c *Client) GetLogs(follow bool, tail int) error {
	stackPath := c.cfg.StackPath()

	tailArg := ""
	if tail > 0 {
		tailArg = fmt.Sprintf("--tail %d", tail)
	}

	followArg := ""
	if follow {
		followArg = "-f"
	}

	cmd := fmt.Sprintf("cd %s && docker compose logs %s %s", stackPath, followArg, tailArg)
	return c.SSHInteractive(cmd)
}

// Cleanup removes a directory on the remote server
func (c *Client) Cleanup(path string) error {
	_, err := c.SSH(fmt.Sprintf("rm -rf %s", path))
	return err
}

// MakeTempDir creates a temporary directory on the remote server
func (c *Client) MakeTempDir() (string, error) {
	output, err := c.SSH("mktemp -d")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}
