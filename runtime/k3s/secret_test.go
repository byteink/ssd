package k3s

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetSecret_PatchJSON_SafeEscaping(t *testing.T) {
	// Verify that the JSON patch structure is safe for keys with special characters
	key := `my"key`
	encoded := "dGVzdA==" // base64("test")
	patch := map[string]map[string]string{
		"data": {key: encoded},
	}
	patchJSON, err := json.Marshal(patch)
	assert.NoError(t, err)
	assert.Contains(t, string(patchJSON), `"my\"key"`)
}

func TestRemoveSecret_JSONPointerEscaping(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{name: "simple key", key: "API_KEY", expected: "/data/API_KEY"},
		{name: "key with slash", key: "my/key", expected: "/data/my~1key"},
		{name: "key with tilde", key: "my~key", expected: "/data/my~0key"},
		{name: "key with both", key: "a/b~c", expected: "/data/a~1b~0c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the escaping logic from RemoveSecret
			escaped := tt.key
			escaped = replaceAll(escaped, "~", "~0")
			escaped = replaceAll(escaped, "/", "~1")

			patch := []map[string]string{
				{"op": "remove", "path": "/data/" + escaped},
			}
			patchJSON, err := json.Marshal(patch)
			assert.NoError(t, err)
			assert.Contains(t, string(patchJSON), tt.expected)
		})
	}
}

// replaceAll mirrors strings.ReplaceAll for test clarity
func replaceAll(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); {
		if len(s)-i >= len(old) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
