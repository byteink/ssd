package testhelpers

import (
	"path/filepath"
	"runtime"
)

// TestdataPath returns the absolute path to a file in the testdata directory
func TestdataPath(filename string) string {
	_, currentFile, _, _ := runtime.Caller(0)
	// Go up from internal/testhelpers to project root
	projectRoot := filepath.Join(filepath.Dir(currentFile), "..", "..")
	return filepath.Join(projectRoot, "testdata", filename)
}

// ComposePath returns the absolute path to a compose fixture file
func ComposePath(filename string) string {
	return TestdataPath(filepath.Join("compose", filename))
}
