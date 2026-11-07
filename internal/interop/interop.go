//go:build integration

// Package interop exchanges test fixtures with Linux reference tools. Without
// an exchange mode, tests invoke the reference tools directly as usual.
package interop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Reference generates independent fixture bytes once, then reuses those exact
// bytes in the native test suite on each platform.
func Reference(t *testing.T, name string, generate func() []byte) []byte {
	t.Helper()

	mode := exchangeMode(t)
	if mode == "platform" || mode == "verify" {
		return read(t, artifactPath(t, "PAKR_INTEROP_REFERENCES", name))
	}

	data := generate()

	if mode == "prepare" {
		write(t, artifactPath(t, "PAKR_INTEROP_REFERENCES", name), data)
	}

	return data
}

// Output exports platform-produced bytes, or loads them for independent
// verification. The bool explicitly defers tool execution to the verifier job.
func Output(t *testing.T, name string, produced []byte) ([]byte, bool) {
	t.Helper()

	switch exchangeMode(t) {
	case "prepare":
		return nil, false
	case "platform":
		write(t, artifactPath(t, "PAKR_INTEROP_OUTPUTS", name), produced)

		return nil, false
	case "verify":
		return read(t, artifactPath(t, "PAKR_INTEROP_OUTPUTS", name)), true
	default:
		return produced, true
	}
}

// TargetOS selects the filesystem expectations of the archive's producer.
func TargetOS(t *testing.T) string {
	t.Helper()

	if exchangeMode(t) != "verify" {
		return runtime.GOOS
	}

	target := os.Getenv("PAKR_INTEROP_OS")

	switch target {
	case "linux", "windows", "darwin":
		return target
	default:
		t.Fatalf("invalid PAKR_INTEROP_OS: %q", target)

		return ""
	}
}

func exchangeMode(t *testing.T) string {
	t.Helper()

	mode := os.Getenv("PAKR_INTEROP_MODE")

	switch mode {
	case "", "prepare", "platform", "verify":
		return mode
	default:
		t.Fatalf("invalid PAKR_INTEROP_MODE: %q", mode)

		return ""
	}
}

func artifactPath(t *testing.T, variable, name string) string {
	t.Helper()

	directory := os.Getenv(variable)
	if directory == "" {
		t.Fatalf("%s is required for fixture exchange", variable)
	}

	return filepath.Join(directory, filepath.FromSlash(t.Name()), name)
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}

	if err != nil {
		t.Fatal(err)
	}
}
