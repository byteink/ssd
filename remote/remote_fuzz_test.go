package remote

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzVersionParsing(f *testing.F) {
	f.Add("image: ssd-app:1", "app")
	f.Add("image: ssd-app:999999999999999999999", "app")
	f.Add("image: ssd-app:-1", "app")
	f.Add("image: ssd-app:5", "app")
	f.Add("", "app")

	f.Fuzz(func(t *testing.T, content, appName string) {
		version, err := ParseVersionFromContent(content, appName)
		if err == nil {
			require.GreaterOrEqual(t, version, 0)
		}
	})
}
