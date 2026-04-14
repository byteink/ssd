package k3s

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/remote"
)

// Client handles remote operations for K3s runtime via SSH.
// Implements remote.RemoteClient using nerdctl and kubectl instead of docker/docker-compose.
type Client struct {
	inner     *remote.Client // Reuse SSH transport, rsync, env, file ops
	cfg       *config.Config
	namespace string // K8s namespace derived from stack path
}

// NewClient creates a K3s client wrapping the shared SSH transport.
func NewClient(cfg *config.Config) *Client {
	return &Client{
		inner:     remote.NewClient(cfg),
		cfg:       cfg,
		namespace: filepath.Base(cfg.Stack),
	}
}

// NewClientWithExecutor constructs a K3s client with a custom command executor.
// Used in tests to intercept SSH invocations.
func NewClientWithExecutor(cfg *config.Config, executor remote.CommandExecutor) *Client {
	return &Client{
		inner:     remote.NewClientWithExecutor(cfg, executor),
		cfg:       cfg,
		namespace: filepath.Base(cfg.Stack),
	}
}

// SSH delegates to the inner client.
func (c *Client) SSH(ctx context.Context, command string) (string, error) {
	return c.inner.SSH(ctx, command)
}

// SSHInteractive delegates to the inner client.
func (c *Client) SSHInteractive(ctx context.Context, command string) error {
	return c.inner.SSHInteractive(ctx, command)
}

// Rsync delegates to the inner client (git archive is runtime-agnostic).
func (c *Client) Rsync(ctx context.Context, localPath, remotePath string) error {
	return c.inner.Rsync(ctx, localPath, remotePath)
}

// MakeTempDir delegates to the inner client.
func (c *Client) MakeTempDir(ctx context.Context) (string, error) {
	return c.inner.MakeTempDir(ctx)
}

// Cleanup delegates to the inner client.
func (c *Client) Cleanup(ctx context.Context, path string) error {
	return c.inner.Cleanup(ctx, path)
}

// CopyFiles delegates to the inner client (file transfer is runtime-agnostic).
func (c *Client) CopyFiles(ctx context.Context, files map[string]string) error {
	return c.inner.CopyFiles(ctx, files)
}

// CreateEnvFile delegates to the inner client (.env files stored on disk same way).
func (c *Client) CreateEnvFile(ctx context.Context, serviceName string) error {
	return c.inner.CreateEnvFile(ctx, serviceName)
}

// CreateEnvFiles delegates to the inner client.
func (c *Client) CreateEnvFiles(ctx context.Context, serviceNames []string) error {
	return c.inner.CreateEnvFiles(ctx, serviceNames)
}

// GetEnvFile delegates to the inner client.
func (c *Client) GetEnvFile(ctx context.Context, serviceName string) (string, error) {
	return c.inner.GetEnvFile(ctx, serviceName)
}

// UploadEnvFile delegates to the inner client.
func (c *Client) UploadEnvFile(ctx context.Context, serviceName, localPath string) error {
	return c.inner.UploadEnvFile(ctx, serviceName, localPath)
}

// SetEnvVar delegates to the inner client.
func (c *Client) SetEnvVar(ctx context.Context, serviceName, key, value string) error {
	return c.inner.SetEnvVar(ctx, serviceName, key, value)
}

// RemoveEnvVar delegates to the inner client.
func (c *Client) RemoveEnvVar(ctx context.Context, serviceName, key string) error {
	return c.inner.RemoveEnvVar(ctx, serviceName, key)
}

// --- K3s-specific implementations ---

// BuildImage builds a container image using nerdctl on the remote server.
// Uses --namespace k8s.io so K3s can see the image.
func (c *Client) BuildImage(ctx context.Context, buildDir string, version int) error {
	// Ensure buildkitd is running
	if _, err := c.SSH(ctx, "systemctl is-active buildkitd || sudo systemctl start buildkitd"); err != nil {
		return fmt.Errorf("failed to ensure buildkitd: %w", err)
	}

	imageTag := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)
	dockerfile := strings.TrimPrefix(c.cfg.Dockerfile, "./")

	targetFlag := ""
	if c.cfg.Target != "" {
		targetFlag = " --target " + shellescape.Quote(c.cfg.Target)
	}

	cmd := fmt.Sprintf("cd %s && sudo nerdctl --namespace k8s.io build -t %s -f %s%s .",
		shellescape.Quote(buildDir),
		shellescape.Quote(imageTag),
		shellescape.Quote(dockerfile),
		targetFlag)
	return c.SSHInteractive(ctx, cmd)
}

// PullImage pulls a container image using nerdctl.
func (c *Client) PullImage(ctx context.Context, image string) error {
	cmd := fmt.Sprintf("sudo nerdctl --namespace k8s.io pull %s", shellescape.Quote(image))
	return c.SSHInteractive(ctx, cmd)
}

// GetCurrentVersion reads the current image version from manifests.yaml on the server.
func (c *Client) GetCurrentVersion(ctx context.Context) (int, error) {
	content, err := c.ReadManifest(ctx)
	if err != nil {
		return 0, nil
	}
	project := filepath.Base(c.cfg.Stack)
	imageName := fmt.Sprintf("ssd-%s-%s", project, c.cfg.Name)
	return remote.ParseVersionFromContent(content, imageName)
}

// ReadManifest reads the manifests.yaml from the remote server.
func (c *Client) ReadManifest(ctx context.Context) (string, error) {
	manifestPath := filepath.Join(c.cfg.StackPath(), "manifests.yaml")
	output, err := c.SSH(ctx, fmt.Sprintf("cat %s 2>/dev/null || echo ''", shellescape.Quote(manifestPath)))
	if err != nil {
		return "", nil
	}
	return output, nil
}

// UpdateManifest updates the image tag in manifests.yaml via sed.
func (c *Client) UpdateManifest(ctx context.Context, version int) error {
	manifestPath := filepath.Join(c.cfg.StackPath(), "manifests.yaml")
	newImage := fmt.Sprintf("%s:%d", c.cfg.ImageName(), version)
	project := filepath.Base(c.cfg.Stack)

	oldPattern := fmt.Sprintf("ssd-%s-%s:[0-9][0-9]*", project, c.cfg.Name)
	cmd := fmt.Sprintf("sed -i 's|%s|%s|g' %s", oldPattern, newImage, shellescape.Quote(manifestPath))

	if _, err := c.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("failed to update manifests.yaml: %w", err)
	}
	return nil
}

// StackExists checks if the stack directory and manifests.yaml exist.
func (c *Client) StackExists(ctx context.Context) (bool, error) {
	stackPath := c.cfg.StackPath()
	manifestPath := filepath.Join(stackPath, "manifests.yaml")

	cmd := fmt.Sprintf("test -d %s && test -f %s && echo yes || echo no",
		shellescape.Quote(stackPath),
		shellescape.Quote(manifestPath))

	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "yes", nil
}

// CreateStack creates the stack directory and writes manifests.yaml atomically.
// Also ensures the K8s namespace exists.
func (c *Client) CreateStack(ctx context.Context, manifestContent string) error {
	if manifestContent == "" {
		return fmt.Errorf("manifest content cannot be empty")
	}

	stackPath := c.cfg.StackPath()
	tmpFile := filepath.Join(stackPath, "manifests.yaml.tmp")
	finalFile := filepath.Join(stackPath, "manifests.yaml")

	// Create stack directory
	mkdirCmd := fmt.Sprintf("mkdir -p %s", shellescape.Quote(stackPath))
	if _, err := c.SSH(ctx, mkdirCmd); err != nil {
		return fmt.Errorf("failed to create stack directory: %w", err)
	}

	// Ensure namespace exists
	nsCmd := fmt.Sprintf("k3s kubectl create namespace %s 2>/dev/null || true", shellescape.Quote(c.namespace))
	if _, err := c.SSH(ctx, nsCmd); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Write to temp file
	escapedContent := strings.ReplaceAll(manifestContent, "'", "'\\''")
	writeCmd := fmt.Sprintf("echo '%s' > %s", escapedContent, shellescape.Quote(tmpFile))
	if _, err := c.SSH(ctx, writeCmd); err != nil {
		return fmt.Errorf("failed to write manifests.yaml.tmp: %w", err)
	}

	// Validate manifests (dry-run)
	validateCmd := fmt.Sprintf("k3s kubectl apply --dry-run=server -f %s 2>&1", shellescape.Quote(tmpFile))
	if output, err := c.SSH(ctx, validateCmd); err != nil {
		detail := strings.TrimSpace(output)
		if i := strings.IndexByte(detail, '\n'); i > 0 {
			detail = detail[:i]
		}
		if detail != "" {
			return fmt.Errorf("manifest validation failed: %s", detail)
		}
		return fmt.Errorf("manifest validation failed: %w", err)
	}

	// Atomic move
	moveCmd := fmt.Sprintf("mv %s %s", shellescape.Quote(tmpFile), shellescape.Quote(finalFile))
	if _, err := c.SSH(ctx, moveCmd); err != nil {
		return fmt.Errorf("failed to move manifests.yaml: %w", err)
	}

	return nil
}

// EnsureNetwork is a no-op for K3s (K8s handles networking via Services).
func (c *Client) EnsureNetwork(_ context.Context, _ string) error {
	return nil
}

// IsServiceRunning checks if a pod for the service is running.
func (c *Client) IsServiceRunning(ctx context.Context, serviceName string) (bool, error) {
	cmd := fmt.Sprintf("k3s kubectl get pods -n %s -l app=%s --field-selector=status.phase=Running -o name 2>/dev/null",
		shellescape.Quote(c.namespace),
		shellescape.Quote(serviceName))

	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(output) != "", nil
}

// applyEnvConfigMap populates the {service}-env ConfigMap from the
// {service}.env file in the stack directory. Must run before kubectl apply
// so the Deployment's envFrom finds the up-to-date data. Empty env files
// produce an empty ConfigMap (still valid).
func (c *Client) applyEnvConfigMap(ctx context.Context, serviceName string) error {
	envPath := filepath.Join(c.cfg.StackPath(), serviceName+".env")
	cmd := fmt.Sprintf("k3s kubectl create configmap %s -n %s --from-env-file=%s --dry-run=client -o yaml | k3s kubectl apply -f -",
		shellescape.Quote(serviceName+"-env"),
		shellescape.Quote(c.namespace),
		shellescape.Quote(envPath))
	if _, err := c.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("failed to apply env configmap for %s: %w", serviceName, err)
	}
	return nil
}

// StartService applies manifests and force-restarts the deployment.
func (c *Client) StartService(ctx context.Context, serviceName string) error {
	if err := c.applyEnvConfigMap(ctx, serviceName); err != nil {
		return err
	}

	manifestPath := filepath.Join(c.cfg.StackPath(), "manifests.yaml")

	// Apply only resources matching this service label
	applyCmd := fmt.Sprintf("k3s kubectl apply -f %s -l app=%s -n %s",
		shellescape.Quote(manifestPath),
		shellescape.Quote(serviceName),
		shellescape.Quote(c.namespace))
	if err := c.SSHInteractive(ctx, applyCmd); err != nil {
		return fmt.Errorf("failed to apply manifests: %w", err)
	}

	// Force restart
	restartCmd := fmt.Sprintf("k3s kubectl rollout restart deployment/%s -n %s",
		shellescape.Quote(serviceName),
		shellescape.Quote(c.namespace))
	if err := c.SSHInteractive(ctx, restartCmd); err != nil {
		return fmt.Errorf("failed to restart deployment: %w", err)
	}

	return nil
}

// RolloutService applies manifests and waits for rollout completion.
func (c *Client) RolloutService(ctx context.Context, serviceName string) error {
	if err := c.applyEnvConfigMap(ctx, serviceName); err != nil {
		return err
	}

	manifestPath := filepath.Join(c.cfg.StackPath(), "manifests.yaml")

	// Apply only resources matching this service label
	applyCmd := fmt.Sprintf("k3s kubectl apply -f %s -l app=%s -n %s",
		shellescape.Quote(manifestPath),
		shellescape.Quote(serviceName),
		shellescape.Quote(c.namespace))
	if err := c.SSHInteractive(ctx, applyCmd); err != nil {
		return fmt.Errorf("failed to apply manifests: %w", err)
	}

	// Wait for rollout
	waitCmd := fmt.Sprintf("k3s kubectl rollout status deployment/%s -n %s --timeout=300s",
		shellescape.Quote(serviceName),
		shellescape.Quote(c.namespace))
	return c.SSHInteractive(ctx, waitCmd)
}

// RestartStack applies all manifests in the stack.
func (c *Client) RestartStack(ctx context.Context) error {
	if err := c.applyEnvConfigMap(ctx, c.cfg.Name); err != nil {
		return err
	}
	manifestPath := filepath.Join(c.cfg.StackPath(), "manifests.yaml")
	cmd := fmt.Sprintf("k3s kubectl apply -f %s", shellescape.Quote(manifestPath))
	return c.SSHInteractive(ctx, cmd)
}

// GetContainerStatus returns pod status for the service.
func (c *Client) GetContainerStatus(ctx context.Context) (string, error) {
	cmd := fmt.Sprintf("k3s kubectl get pods -n %s -l app=%s -o wide",
		shellescape.Quote(c.namespace),
		shellescape.Quote(c.cfg.Name))
	return c.SSH(ctx, cmd)
}

// GetLogs returns logs for the service pods.
func (c *Client) GetLogs(ctx context.Context, follow bool, tail int) error {
	tailArg := ""
	if tail > 0 {
		tailArg = fmt.Sprintf("--tail=%d", tail)
	}

	followArg := ""
	if follow {
		followArg = "-f"
	}

	cmd := fmt.Sprintf("k3s kubectl logs -n %s -l app=%s %s %s",
		shellescape.Quote(c.namespace),
		shellescape.Quote(c.cfg.Name),
		followArg,
		tailArg)
	return c.SSHInteractive(ctx, cmd)
}
