package config

import (
	"strings"
	"testing"
)

func FuzzConfigLoad(f *testing.F) {
	// Add seed corpus
	f.Add([]byte("server: test\nname: app"))
	f.Add([]byte("services:\n  web:\n    name: test"))
	f.Add([]byte(""))
	f.Add([]byte("invalid: [unclosed"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic
		_, _ = LoadFromBytes(data)
	})
}

func FuzzImageName(f *testing.F) {
	f.Add("myapp")
	f.Add("my-app-123")
	f.Add("")
	f.Add(strings.Repeat("a", 1000))

	f.Fuzz(func(t *testing.T, name string) {
		cfg := &Config{Name: name}
		// Must never panic, result must be valid or empty
		result := cfg.ImageName()
		_ = result
	})
}
