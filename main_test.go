package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	flags := []string{"-v", "--version"}

	for _, flag := range flags {
		t.Run(flag, func(t *testing.T) {
			var output bytes.Buffer

			application := command()
			application.Writer = &output

			err := application.Run(context.Background(), []string{"pakr", flag})
			if err != nil {
				t.Fatal(err)
			}

			if output.String() != "pakr version "+version+"\n" {
				t.Fatalf("unexpected version: %q", output.String())
			}
		})
	}
}

func TestCommandPipeline(t *testing.T) {
	directory := t.TempDir()
	input := filepath.Join(directory, "hello.txt")

	data := []byte("a clean pack and unpack pipeline\n")

	err := os.WriteFile(input, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stream bytes.Buffer

	packing := command()
	packing.Writer = &stream

	err = packing.Run(context.Background(), []string{"pakr", "pack", input, "-f", "tgz", "-o", "-", "-t", "2", "-p", "fast", "-q"})
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(directory, "output")

	unpacking := command()
	unpacking.Reader = &stream

	err = unpacking.Run(context.Background(), []string{"pakr", "unpack", "-", "-f", "tar.gzip", "-o", output, "--quiet"})
	if err != nil {
		t.Fatal(err)
	}

	actual, err := os.ReadFile(filepath.Join(output, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(actual, data) {
		t.Fatal("pipeline changed the contents")
	}
}

func TestInvalidOptions(t *testing.T) {
	cases := [][]string{
		{"--threads", "0"},
		{"--threads", "257"},
		{"--level", "0"},
		{"--level", "10"},
		{"--preset", "unknown"},
		{"--preset", "fast", "--level", "1"},
		{"--format", "unknown"},
	}

	for _, arguments := range cases {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			application := command()

			args := append([]string{"pakr", "pack", "unused"}, arguments...)

			err := application.Run(context.Background(), args)
			if err == nil || strings.Contains(err.Error(), "unused") {
				t.Fatalf("expected option validation before opening inputs, got %v", err)
			}
		})
	}
}
