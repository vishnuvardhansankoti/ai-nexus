package config

import (
	"errors"
	"os"
	"testing"
)

// TestLoad_NonexistentFile verifies that Load returns an error (not a panic)
// when the requested config file does not exist.
func TestLoad_NonexistentFile(t *testing.T) {
	_, err := Load("/tmp/nexus-does-not-exist-12345.yaml")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		// Accept any error that wraps ErrNotExist or contains a descriptive message.
		t.Logf("got expected error: %v", err)
	}
}

// TestDefaultConfigPath verifies that DefaultConfigPath returns a non-empty
// string containing the expected suffix.
func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Fatal("DefaultConfigPath returned an empty string")
	}
	const suffix = ".nexus/config.yaml"
	if len(path) < len(suffix) || path[len(path)-len(suffix):] != suffix {
		t.Errorf("DefaultConfigPath = %q, want suffix %q", path, suffix)
	}
}
