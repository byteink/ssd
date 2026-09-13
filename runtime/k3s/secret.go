package k3s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"al.essio.dev/pkg/shellescape"
)

// secretName is the K8s Secret backing a service's secrets.
func secretName(serviceName string) string { return serviceName + "-secret" }

// Namespace returns the K8s namespace this client operates in.
func (c *Client) Namespace() string { return c.namespace }

// NamespaceExists reports whether the stack's namespace is present.
//
// --ignore-not-found is what keeps "absent" (exit 0, empty stdout) distinct
// from a real failure (unreachable server, broken kubeconfig, RBAC), which
// still exits non-zero and carries the remote stderr. A plain `get` cannot
// tell the two apart — both exit 1.
func (c *Client) NamespaceExists(ctx context.Context) (bool, error) {
	cmd := fmt.Sprintf("k3s kubectl get namespace %s --ignore-not-found -o name",
		shellescape.Quote(c.namespace))
	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return false, fmt.Errorf("checking namespace %q: %w", c.namespace, err)
	}
	return strings.TrimSpace(output) != "", nil
}

// ensureNamespace creates the namespace if it is missing.
//
// Secrets must be settable before the first deploy: `build_secrets` resolves
// ${secret:KEY} during the build, so requiring a deployed namespace would make
// the documented workflow circular.
func (c *Client) ensureNamespace(ctx context.Context) error {
	ns := shellescape.Quote(c.namespace)
	cmd := fmt.Sprintf("k3s kubectl create namespace %s --dry-run=client -o yaml | k3s kubectl apply -f -", ns)
	if _, err := c.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("creating namespace %q: %w", c.namespace, err)
	}
	return nil
}

// secretExists reports whether the service's Secret object is present.
func (c *Client) secretExists(ctx context.Context, name string) (bool, error) {
	cmd := fmt.Sprintf("k3s kubectl get secret %s -n %s --ignore-not-found -o name",
		shellescape.Quote(name),
		shellescape.Quote(c.namespace))
	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return false, fmt.Errorf("checking secret %s in namespace %q: %w", name, c.namespace, err)
	}
	return strings.TrimSpace(output) != "", nil
}

// SetSecret creates or updates a K8s Secret key for the given service,
// creating the namespace first when the stack has never been deployed.
func (c *Client) SetSecret(ctx context.Context, serviceName, key, value string) error {
	if err := c.ensureNamespace(ctx); err != nil {
		return err
	}

	name := secretName(serviceName)
	exists, err := c.secretExists(ctx, name)
	if err != nil {
		return err
	}

	if !exists {
		literal := fmt.Sprintf("--from-literal=%s=%s", key, value)
		cmd := fmt.Sprintf("k3s kubectl create secret generic %s -n %s %s",
			shellescape.Quote(name),
			shellescape.Quote(c.namespace),
			shellescape.Quote(literal))
		if _, err := c.SSH(ctx, cmd); err != nil {
			return fmt.Errorf("creating secret %s in namespace %q: %w", name, c.namespace, err)
		}
		return nil
	}

	// Patch existing secret — build JSON safely
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	patch := map[string]map[string]string{
		"data": {key: encoded},
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	cmd := fmt.Sprintf("k3s kubectl patch secret %s -n %s -p %s",
		shellescape.Quote(name),
		shellescape.Quote(c.namespace),
		shellescape.Quote(string(patchJSON)))
	if _, err := c.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("updating secret %s in namespace %q: %w", name, c.namespace, err)
	}
	return nil
}

// ListSecrets returns the service's secrets as KEY=VALUE lines.
//
// A missing namespace or a missing Secret is an empty result, not an error:
// pre-deploy, "no secrets set" is the correct answer. Only a genuine failure
// returns an error, and it names the target and carries the remote stderr.
func (c *Client) ListSecrets(ctx context.Context, serviceName string) (string, error) {
	name := secretName(serviceName)
	cmd := fmt.Sprintf("k3s kubectl get secret %s -n %s --ignore-not-found -o go-template='{{range $k,$v := .data}}{{$k}}={{$v | base64decode}}\n{{end}}'",
		shellescape.Quote(name),
		shellescape.Quote(c.namespace))
	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("listing secret %s in namespace %q: %w", name, c.namespace, err)
	}
	return output, nil
}

// RemoveSecret removes a key from the K8s Secret for the given service.
func (c *Client) RemoveSecret(ctx context.Context, serviceName, key string) error {
	name := secretName(serviceName)
	exists, err := c.secretExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no secrets set for %s", serviceName)
	}

	// Build JSON patch safely — escape key for JSON pointer (RFC 6901)
	escapedKey := strings.ReplaceAll(key, "~", "~0")
	escapedKey = strings.ReplaceAll(escapedKey, "/", "~1")

	patch := []map[string]string{
		{"op": "remove", "path": "/data/" + escapedKey},
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	cmd := fmt.Sprintf("k3s kubectl patch secret %s -n %s --type=json -p %s",
		shellescape.Quote(name),
		shellescape.Quote(c.namespace),
		shellescape.Quote(string(patchJSON)))
	if _, err := c.SSH(ctx, cmd); err != nil {
		return fmt.Errorf("removing %s from secret %s in namespace %q: %w", key, name, c.namespace, err)
	}
	return nil
}
