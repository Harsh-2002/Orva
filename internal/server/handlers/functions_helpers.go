package handlers

import "os"

// createTempFile creates a temporary file with the given pattern.
// Extracted for testability.
func createTempFile(pattern string) (*os.File, error) {
	return os.CreateTemp("", pattern)
}

// removeTempFile removes the file at the given path, ignoring errors.
func removeTempFile(path string) {
	os.Remove(path)
}
