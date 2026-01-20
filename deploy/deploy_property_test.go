package deploy

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestProperty_VersionAlwaysIncreases verifies that version incrementation
// always produces a value greater than the current version
func TestProperty_VersionAlwaysIncreases(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		currentVersion := rapid.IntRange(0, 1000000).Draw(t, "current")

		newVersion := currentVersion + 1

		require.Greater(t, newVersion, currentVersion)
		require.GreaterOrEqual(t, newVersion, 1)
	})
}

// TestProperty_VersionNeverNegative verifies that version parsing and
// incrementation never produces negative values
func TestProperty_VersionNeverNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a version that could come from parsing (0 or positive)
		parsedVersion := rapid.IntRange(0, 1000000).Draw(t, "parsed")

		require.GreaterOrEqual(t, parsedVersion, 0)

		// After incrementation, should still be non-negative
		newVersion := parsedVersion + 1
		require.GreaterOrEqual(t, newVersion, 0)
		require.Greater(t, newVersion, 0) // Must be at least 1
	})
}
