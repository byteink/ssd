package config

import "testing"

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
