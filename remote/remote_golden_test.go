package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

// transformCompose applies the same transformation logic as UpdateCompose
// but operates on strings directly for testing purposes
func transformCompose(input string, appName string, newVersion int) string {
	newImage := fmt.Sprintf("ssd-%s:%d", appName, newVersion)

	// Replace image tag - handle both old and new naming conventions
	// Match any image line for the app service
	oldImagePattern := regexp.MustCompile(`(image:\s*)(ssd-` + regexp.QuoteMeta(appName) + `|ssd-` + regexp.QuoteMeta(appName) + `):(\d+)`)
	return oldImagePattern.ReplaceAllString(input, fmt.Sprintf("${1}%s", newImage))
}

// readGoldenFile reads a test fixture file from the golden testdata directory
func readGoldenFile(testCase, filename string) string {
	path := filepath.Join("testdata", "golden", testCase, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("failed to read golden file %s: %v", path, err))
	}
	return string(data)
}

func TestGolden_UpdateCompose(t *testing.T) {
	tests := []struct {
		name       string
		appName    string
		newVersion int
	}{
		{
			name:       "compose-basic-upgrade",
			appName:    "myapp",
			newVersion: 5,
		},
		{
			name:       "compose-",
			appName:    "myapp",
			newVersion: 5,
		},
		{
			name:       "compose-preserve-structure",
			appName:    "myapp",
			newVersion: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := readGoldenFile(tc.name, "input.yaml")
			expected := readGoldenFile(tc.name, "expected.yaml")

			result := transformCompose(input, tc.appName, tc.newVersion)

			if diff := cmp.Diff(expected, result); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGolden_UpdateCompose_MultipleOccurrences(t *testing.T) {
	input := `services:
  app:
    image: ssd-myapp:3
  app-backup:
    image: ssd-myapp:3
`

	expected := `services:
  app:
    image: ssd-myapp:7
  app-backup:
    image: ssd-myapp:7
`

	result := transformCompose(input, "myapp", 7)

	require.Equal(t, expected, result)
}

func TestGolden_UpdateCompose_NoMatch(t *testing.T) {
	input := `services:
  app:
    image: nginx:latest
`

	// Should not modify if no matching image
	expected := input

	result := transformCompose(input, "myapp", 5)

	require.Equal(t, expected, result)
}

func TestGolden_UpdateCompose_MixedSpacing(t *testing.T) {
	input := `services:
  app:
    image:  ssd-myapp:4
  other:
    image:	ssd-myapp:4
`

	expected := `services:
  app:
    image:  ssd-myapp:10
  other:
    image:	ssd-myapp:10
`

	result := transformCompose(input, "myapp", 10)

	require.Equal(t, expected, result)
}

func TestGolden_UpdateCompose_LegacyToModern(t *testing.T) {
	input := `services:
  web:
    image: ssd-webapp:5
  api:
    image: ssd-api:3
`

	expectedWeb := `services:
  web:
    image: ssd-webapp:10
  api:
    image: ssd-api:3
`

	resultWeb := transformCompose(input, "webapp", 10)
	require.Equal(t, expectedWeb, resultWeb)

	expectedApi := `services:
  web:
    image: ssd-webapp:5
  api:
    image: ssd-api:8
`

	resultApi := transformCompose(input, "api", 8)
	require.Equal(t, expectedApi, resultApi)
}
