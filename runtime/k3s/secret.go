package k3s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"al.essio.dev/pkg/shellescape"
)

// SetSecret creates or updates a K8s Secret key for the given service.
func (c *Client) SetSecret(ctx context.Context, serviceName, key, value string) error {
	secretName := serviceName + "-secret"

	// Check if secret exists
	checkCmd := fmt.Sprintf("k3s kubectl get secret %s -n %s -o name 2>/dev/null",
		shellescape.Quote(secretName),
		shellescape.Quote(c.namespace))
	output, err := c.SSH(ctx, checkCmd)
	if err != nil {
		// SSH failed — treat as secret not existing
		output = ""
	}

	if strings.TrimSpace(output) == "" {
		// Create new secret
		literal := fmt.Sprintf("--from-literal=%s=%s", key, value)
		cmd := fmt.Sprintf("k3s kubectl create secret generic %s -n %s %s",
			shellescape.Quote(secretName),
			shellescape.Quote(c.namespace),
			shellescape.Quote(literal))
		_, err := c.SSH(ctx, cmd)
		return err
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
		shellescape.Quote(secretName),
		shellescape.Quote(c.namespace),
		shellescape.Quote(string(patchJSON)))
	_, err = c.SSH(ctx, cmd)
	return err
}

// ListSecrets lists all keys in the K8s Secret for the given service.
func (c *Client) ListSecrets(ctx context.Context, serviceName string) (string, error) {
	secretName := serviceName + "-secret"
	cmd := fmt.Sprintf("k3s kubectl get secret %s -n %s -o go-template='{{range $k,$v := .data}}{{$k}}={{$v | base64decode}}\n{{end}}' 2>/dev/null",
		shellescape.Quote(secretName),
		shellescape.Quote(c.namespace))
	output, err := c.SSH(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("failed to list secrets: %w", err)
	}
	return output, nil
}

// RemoveSecret removes a key from the K8s Secret for the given service.
func (c *Client) RemoveSecret(ctx context.Context, serviceName, key string) error {
	secretName := serviceName + "-secret"

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
		shellescape.Quote(secretName),
		shellescape.Quote(c.namespace),
		shellescape.Quote(string(patchJSON)))
	_, err = c.SSH(ctx, cmd)
	return err
}
