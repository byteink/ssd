package remote

import (
	"context"
	"fmt"
	"os/exec"
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
	StackExists(ctx context.Context) (bool, error)
	ReadCompose(ctx context.Context) (string, error)
	IsServiceRunning(ctx context.Context, serviceName string) (bool, error)
	EnsureNetwork(ctx context.Context, name string) error
	CreateEnvFile(ctx context.Context, serviceName string) error
	CreateEnvFiles(ctx context.Context, serviceNames []string) error
	GetEnvFile(ctx context.Context, serviceName string) (string, error)
	SetEnvVar(ctx context.Context, serviceName, key, value string) error
	RemoveEnvVar(ctx context.Context, serviceName, key string) error
	CreateStack(ctx context.Context, composeContent string) error
	PullImage(ctx context.Context, image string) error
	StartService(ctx context.Context, serviceName string) error
	RolloutService(ctx context.Context, serviceName string) error
}

// Ensure Client implements RemoteClient
var _ RemoteClient = (*Client)(nil)

// Client handles remote operations via SSH
type Client struct {
	server        string
	cfg           *config.Config
	executor      CommandExecutor
	findGitRoot   func(string) (string, error)
	sshArgs       []string // Extra SSH args (e.g., ControlMaster options)
	composeCache  string
	composeCached bool
}

// defaultGitRoot finds the git repository root for the given directory
func defaultGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (or any parent): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// NewClient creates a new remote client with the default executor
func NewClient(cfg *config.Config) *Client {
	return &Client{
		server:      cfg.Server,
		cfg:         cfg,
		executor:    NewRealExecutor(),
		findGitRoot: defaultGitRoot,
		sshArgs: []string{
			"-o", "ControlMaster=auto",
			"-o", "ControlPath=/tmp/ssd-%C",
			"-o", "ControlPersist=60s",
		},
	}
}

// NewClientWithExecutor creates a client with a custom executor (for testing)
func NewClientWithExecutor(cfg *config.Config, executor CommandExecutor) *Client {
	return &Client{
		server:      cfg.Server,
		cfg:         cfg,
		executor:    executor,
		findGitRoot: defaultGitRoot,
	}
}

// SSH executes a command on the remote server
func (c *Client) SSH(ctx context.Context, command string) (string, error) {
	args := append(c.sshArgs, c.server, command)
	output, err := c.executor.Run(ctx, "ssh", args...)
	if err != nil {
		return "", fmt.Errorf("ssh command failed: %w", err)
	}
	return output, nil
}

// SSHInteractive runs an SSH command with output streamed to terminal.
// Output is streamed in real time via stdout/stderr passthrough.
func (c *Client) SSHInteractive(ctx context.Context, command string) error {
	args := append(c.sshArgs, c.server, command)
	return c.executor.RunInteractive(ctx, "ssh", args...)
}

// Rsync syncs local directory to remote server using git archive.
// Only git-tracked files are transferred, automatically respecting .gitignore.
func (c *Client) Rsync(ctx context.Context, localPath, remotePath string) error {
	// Find git repository root
	gitRoot, err := c.findGitRoot(localPath)
	if err != nil {
		return fmt.Errorf("failed to find git root: %w", err)
	}

	// Compute relative path from git root to the context directory
	relPath, err := filepath.Rel(gitRoot, localPath)
	if err != nil {
		return fmt.Errorf("failed to compute relative path: %w", err)
	}

	// Build git archive command (runs locally)
	archiveCmd := fmt.Sprintf("git -C %s archive --format=tar HEAD", shellescape.Quote(gitRoot))

	// Build tar extract command (runs on remote via SSH)
	extractCmd := fmt.Sprintf("tar xf - -C %s", shellescape.Quote(remotePath))

	// If context is a subdirectory, archive only that path and strip prefix
	if relPath != "." {
		archiveCmd += fmt.Sprintf(" -- %s", shellescape.Quote(relPath))
		stripN := strings.Count(relPath, string(filepath.Separator)) + 1
		extractCmd += fmt.Sprintf(" --strip-components=%d", stripN)
	}

	// Pipeline: git archive | ssh [opts] server 'tar extract'
	sshCmd := "ssh"
	if len(c.sshArgs) > 0 {
		sshCmd += " " + strings.Join(c.sshArgs, " ")
	}
	pipeline := fmt.Sprintf("%s | %s %s %s",
		archiveCmd,
		sshCmd,
		c.server,
		shellescape.Quote(extractCmd))

	return c.executor.RunInteractive(ctx, "bash", "-c", pipeline)
}

// ReadCompose reads the current compose.yaml content from the remote server.
// Returns empty string (no error) if the file does not exist.
// Results are cached per Client instance; writes via CreateStack/UpdateCompose invalidate the cache.
func (c *Client) ReadCompose(ctx context.Context) (string, error) {
	if c.composeCached {
		return c.composeCache, nil
	}
	composePath := filepath.Join(c.cfg.StackPath(), "compose.yaml")
	output, err := c.SSH(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shellescape.Quote(composePath)))
	if err != nil {
		return "", nil
	}
	c.composeCache = output
	c.composeCached = true
	return output, nil
}

// ParseVersionFromContent extracts the version number from compose.yaml content
// imageName should be the full image name prefix (e.g., "ssd-myapp-api" without the :version tag)
// Returns 0, nil if no version found; error on parse failure
func ParseVersionFromContent(content, imageName string) (int, error) {
	// Validate inputs are valid UTF-8 to prevent regexp compilation panics
	if !utf8.ValidString(imageName) || !utf8.ValidString(content) {
		return 0, nil
	}

	// Match imageName:{version}
	re := regexp.MustCompile(fmt.Sprintf(`image:\s*%s:(\d+)`, regexp.QuoteMeta(imageName)))
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return strconv.Atoi(matches[1])
	}

	return 0, nil
}

// GetCurrentVersion reads the current image version from compose.yaml on the server.
// Reuses cached compose content from ReadCompose when available.
func (c *Client) GetCurrentVersion(ctx context.Context) (int, error) {
	content, err := c.ReadCompose(ctx)
	if err != nil {
		return 0, nil
	}
	project := filepath.Base(c.cfg.Stack)
	imageName := fmt.Sprintf("ssd-%s-%s", project, c.cfg.Name)
	return ParseVersionFromContent(content, imageName)
}

// BuildImage builds a Docker image on the remote server
func (c *Client) BuildImage(ctx context.Context, buildDir string, version int) error {
	imageTag := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)

	// Build command with dockerfile path relative to build context
	dockerfile := strings.TrimPrefix(c.cfg.Dockerfile, "./")

	targetFlag := ""
	if c.cfg.Target != "" {
		targetFlag = " --target " + shellescape.Quote(c.cfg.Target)
	}

	cmd := fmt.Sprintf("cd %s && docker build -t %s -f %s%s .", shellescape.Quote(buildDir), shellescape.Quote(imageTag), shellescape.Quote(dockerfile), targetFlag)
	return c.SSHInteractive(ctx, cmd)
}

// UpdateCompose updates the image tag in compose.yaml via server-side sed.
// Single SSH call instead of read-modify-write.
func (c *Client) UpdateCompose(ctx context.Context, version int) error {
	composePath := filepath.Join(c.cfg.StackPath(), "compose.yaml")
	newImage := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)
	project := filepath.Base(c.cfg.Stack)

	// sed pattern: replace ssd-project-service:NNN with new image tag
	// Uses | as delimiter to avoid conflicts with path separators
	oldPattern := fmt.Sprintf("ssd-%s-%s:[0-9][0-9]*", project, c.cfg.Name)
	cmd := fmt.Sprintf("sed -i 's|%s|%s|g' %s", oldPattern, newImage, shellescape.Quote(composePath))

	if _, err := c.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("failed to update compose.yaml: %w", err)
	}

	c.composeCached = false
	return nil
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

// StackExists checks if the stack directory and compose.yaml exist on the remote server
func (c *Client) StackExists(ctx context.Context) (bool, error) {
	stackPath := c.cfg.StackPath()
	composePath := filepath.Join(stackPath, "compose.yaml")

	cmd := fmt.Sprintf("test -d %s && test -f %s && echo yes || echo no",
		shellescape.Quote(stackPath),
		shellescape.Quote(composePath))

	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(output) == "yes", nil
}

// IsServiceRunning checks if a service is running in the stack
func (c *Client) IsServiceRunning(ctx context.Context, serviceName string) (bool, error) {
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker compose ps --format json %s",
		shellescape.Quote(stackPath),
		shellescape.Quote(serviceName))

	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return false, err
	}

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return false, nil
	}

	return strings.Contains(trimmed, `"State":"running"`), nil
}

// EnsureNetwork creates a Docker network if it doesn't exist (idempotent)
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	cmd := fmt.Sprintf("docker network create %s 2>/dev/null || true", shellescape.Quote(name))
	_, err := c.SSH(ctx, cmd)
	return err
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
		return fmt.Errorf("path must not contain path traversal sequence")
	}

	return nil
}

// CreateEnvFiles creates empty {serviceName}.env files for all given services in a single SSH call.
// Existing files are not overwritten.
func (c *Client) CreateEnvFiles(ctx context.Context, serviceNames []string) error {
	if len(serviceNames) == 0 {
		return nil
	}
	stackDir := shellescape.Quote(c.cfg.StackPath())
	parts := []string{fmt.Sprintf("mkdir -p %s", stackDir)}
	for _, name := range serviceNames {
		envPath := filepath.Join(c.cfg.StackPath(), fmt.Sprintf("%s.env", name))
		quoted := shellescape.Quote(envPath)
		parts = append(parts, fmt.Sprintf("(test -f %s || install -m 600 /dev/null %s)", quoted, quoted))
	}
	_, err := c.SSH(ctx, strings.Join(parts, " && "))
	return err
}

// CreateEnvFile creates an empty {serviceName}.env file with mode 600 in the stack directory.
// Existing files are not overwritten, preserving any env vars already set.
func (c *Client) CreateEnvFile(ctx context.Context, serviceName string) error {
	stackDir := shellescape.Quote(c.cfg.StackPath())
	envPath := filepath.Join(c.cfg.StackPath(), fmt.Sprintf("%s.env", serviceName))
	quoted := shellescape.Quote(envPath)
	cmd := fmt.Sprintf("mkdir -p %s && test -f %s || install -m 600 /dev/null %s", stackDir, quoted, quoted)
	_, err := c.SSH(ctx, cmd)
	return err
}

// GetEnvFile reads the {serviceName}.env file from the stack directory
func (c *Client) GetEnvFile(ctx context.Context, serviceName string) (string, error) {
	envPath := filepath.Join(c.cfg.StackPath(), fmt.Sprintf("%s.env", serviceName))
	output, err := c.SSH(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shellescape.Quote(envPath)))
	if err != nil {
		return "", err
	}
	return output, nil
}

// SetEnvVar sets or updates an environment variable in the {serviceName}.env file
func (c *Client) SetEnvVar(ctx context.Context, serviceName, key, value string) error {
	content, err := c.GetEnvFile(ctx, serviceName)
	if err != nil {
		return err
	}

	lines := strings.Split(content, "\n")
	prefix := key + "="
	found := false
	newValue := prefix + value

	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = newValue
			found = true
			break
		}
	}

	if !found {
		if content != "" && !strings.HasSuffix(content, "\n") {
			lines = append(lines, newValue)
		} else {
			lines = append(lines[:len(lines)-1], newValue, "")
		}
	}

	newContent := strings.Join(lines, "\n")
	stackDir := shellescape.Quote(c.cfg.StackPath())
	envPath := filepath.Join(c.cfg.StackPath(), fmt.Sprintf("%s.env", serviceName))
	escapedContent := strings.ReplaceAll(newContent, "'", "'\\''")
	cmd := fmt.Sprintf("mkdir -p %s && echo '%s' | install -m 600 /dev/stdin %s", stackDir, escapedContent, shellescape.Quote(envPath))
	_, err = c.SSH(ctx, cmd)
	return err
}

// RemoveEnvVar removes an environment variable from the {serviceName}.env file
func (c *Client) RemoveEnvVar(ctx context.Context, serviceName, key string) error {
	content, err := c.GetEnvFile(ctx, serviceName)
	if err != nil {
		return err
	}

	lines := strings.Split(content, "\n")
	prefix := key + "="
	filtered := make([]string, 0, len(lines))

	for _, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			filtered = append(filtered, line)
		}
	}

	newContent := strings.Join(filtered, "\n")
	stackDir := shellescape.Quote(c.cfg.StackPath())
	envPath := filepath.Join(c.cfg.StackPath(), fmt.Sprintf("%s.env", serviceName))
	escapedContent := strings.ReplaceAll(newContent, "'", "'\\''")
	cmd := fmt.Sprintf("mkdir -p %s && echo '%s' | install -m 600 /dev/stdin %s", stackDir, escapedContent, shellescape.Quote(envPath))
	_, err = c.SSH(ctx, cmd)
	return err
}

// CreateStack creates a stack directory and compose.yaml file with atomic write
func (c *Client) CreateStack(ctx context.Context, composeContent string) error {
	if composeContent == "" {
		return fmt.Errorf("compose content cannot be empty")
	}

	stackPath := c.cfg.StackPath()
	tmpFile := filepath.Join(stackPath, "compose.yaml.tmp")
	finalFile := filepath.Join(stackPath, "compose.yaml")

	// Step 1: Create stack directory
	mkdirCmd := fmt.Sprintf("mkdir -p %s", shellescape.Quote(stackPath))
	if _, err := c.SSH(ctx, mkdirCmd); err != nil {
		return fmt.Errorf("failed to create stack directory: %w", err)
	}

	// Step 2: Write content to temporary file
	escapedContent := strings.ReplaceAll(composeContent, "'", "'\\''")
	writeCmd := fmt.Sprintf("echo '%s' > %s", escapedContent, shellescape.Quote(tmpFile))
	if _, err := c.SSH(ctx, writeCmd); err != nil {
		return fmt.Errorf("failed to write compose.yaml.tmp: %w", err)
	}

	// Step 3: Validate compose file
	validateCmd := fmt.Sprintf("cd %s && docker compose -f compose.yaml.tmp config 2>&1", shellescape.Quote(stackPath))
	if output, err := c.SSH(ctx, validateCmd); err != nil {
		// Include first line of docker compose output for diagnostics
		detail := strings.TrimSpace(output)
		if i := strings.IndexByte(detail, '\n'); i > 0 {
			detail = detail[:i]
		}
		if detail != "" {
			return fmt.Errorf("compose.yaml validation failed: %s", detail)
		}
		return fmt.Errorf("compose.yaml validation failed: %w", err)
	}

	// Step 4: Move temp file to final location
	moveCmd := fmt.Sprintf("mv %s %s", shellescape.Quote(tmpFile), shellescape.Quote(finalFile))
	if _, err := c.SSH(ctx, moveCmd); err != nil {
		return fmt.Errorf("failed to move compose.yaml.tmp to compose.yaml: %w", err)
	}

	c.composeCached = false
	return nil
}

// PullImage pulls a Docker image on the remote server
func (c *Client) PullImage(ctx context.Context, image string) error {
	cmd := fmt.Sprintf("docker pull %s", shellescape.Quote(image))
	return c.SSHInteractive(ctx, cmd)
}

// StartService starts a specific service in the stack
func (c *Client) StartService(ctx context.Context, serviceName string) error {
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker compose up -d --force-recreate %s", shellescape.Quote(stackPath), shellescape.Quote(serviceName))
	return c.SSHInteractive(ctx, cmd)
}

// RolloutService performs a zero-downtime update using docker rollout
func (c *Client) RolloutService(ctx context.Context, serviceName string) error {
	stackPath := c.cfg.StackPath()
	cmd := fmt.Sprintf("cd %s && docker rollout %s", shellescape.Quote(stackPath), shellescape.Quote(serviceName))
	return c.SSHInteractive(ctx, cmd)
}
