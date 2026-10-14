package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type commandFailureCase struct {
	name      string
	arguments []string
	cancelled bool
}

type quietArgumentCase struct {
	name      string
	arguments []string
	quiet     bool
}

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

	var (
		stream bytes.Buffer
		logs   bytes.Buffer
	)

	packing := command()
	packing.Writer = &stream
	packing.ErrWriter = &logs

	err = packing.Run(context.Background(), []string{"pakr", "pack", input, "-f", "tgz", "-o", "-", "-t", "2", "-p", "fast", "-q"})
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(directory, "output")

	unpacking := command()
	unpacking.Reader = &stream
	unpacking.ErrWriter = &logs

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

	if logs.Len() != 0 {
		t.Fatalf("quiet pipeline produced status: %q", logs.String())
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

func TestCommandStatusPipeline(t *testing.T) {
	formats := []string{"tar", "tar.zst", "zip", "7z", "gz"}

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			directory := t.TempDir()
			input := filepath.Join(directory, "MixedCase.txt")

			data := []byte("a small archive\n")

			err := os.WriteFile(input, data, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			var (
				stream bytes.Buffer
				logs   bytes.Buffer
			)

			packing := command()
			packing.Writer = &stream
			packing.ErrWriter = &logs

			err = run(context.Background(), packing, []string{"pakr", "pack", input, "-f", format, "-o", "-"})
			if err != nil {
				t.Fatal(err)
			}

			packedSize := stream.Len()
			size := fmt.Sprintf("%d b", packedSize)

			if packedSize >= 1024 {
				size = fmt.Sprintf("%.1f kib", float64(packedSize)/1024)
			}

			ratio := float64(packedSize) / float64(len(data)) * 100

			expected := fmt.Sprintf(":: packed 1 entry | %d b -> %s (%.1f%%) | ", len(data), size, ratio)
			if !strings.Contains(logs.String(), expected) || !strings.HasSuffix(logs.String(), "s | stdout\n") {
				t.Fatalf("unexpected pack summary: %q", logs.String())
			}

			if !strings.HasPrefix(logs.String(), ":: pack ") || strings.ContainsAny(logs.String(), "\r\x1b") {
				t.Fatalf("unexpected redirected status: %q", logs.String())
			}

			// Extra tar padding is validated but is not extracted file data.
			if format == "tar" {
				stream.Write(make([]byte, 4096))
			}

			logs.Reset()

			output := filepath.Join(directory, "restored")

			unpacking := command()
			unpacking.Reader = &stream
			unpacking.Writer = io.Discard
			unpacking.ErrWriter = &logs

			err = run(context.Background(), unpacking, []string{"pakr", "unpack", "-", "-f", format, "-o", output})
			if err != nil {
				t.Fatal(err)
			}

			expected = fmt.Sprintf(":: unpacked 1 entry | %d b | ", len(data))
			if !strings.HasPrefix(logs.String(), ":: unpack stdin -> ") || !strings.Contains(logs.String(), expected) {
				t.Fatalf("unexpected unpack summary: %q", logs.String())
			}

			if !strings.HasSuffix(logs.String(), "s | "+output+"\n") || strings.ContainsAny(logs.String(), "\r\x1b") {
				t.Fatalf("unexpected redirected status: %q", logs.String())
			}

			name := "MixedCase.txt"

			if format == "gz" {
				name = "stdin"
			}

			actual, err := os.ReadFile(filepath.Join(output, name))
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(actual, data) {
				t.Fatal("status output changed archive contents")
			}
		})
	}
}

func TestCommandFailureOutput(t *testing.T) {
	cases := []commandFailureCase{
		{name: "options", arguments: []string{"pack", "unused", "--threads", "0"}},
		{name: "usage", arguments: []string{"pack", "--invalid"}},
		{name: "input", arguments: []string{"unpack", filepath.Join(t.TempDir(), "Missing.tar")}},
		{name: "cancelled", arguments: []string{"pack", t.TempDir()}, cancelled: true},
	}
	quietFlags := []string{"", "-q", "--quiet"}

	for _, scenario := range cases {
		for _, quiet := range quietFlags {
			t.Run(scenario.name+quiet, func(t *testing.T) {
				var (
					output bytes.Buffer
					logs   bytes.Buffer
				)

				application := command()
				application.Writer = &output
				application.ErrWriter = &logs

				arguments := []string{"pakr"}

				if quiet == "-q" {
					arguments = append(arguments, quiet)
				}

				arguments = append(arguments, scenario.arguments...)

				if quiet == "--quiet" {
					arguments = append(arguments, quiet)
				}

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				if scenario.cancelled {
					cancel()
				}

				err := run(ctx, application, arguments)
				if err == nil {
					t.Fatal("expected failure")
				}

				if output.Len() != 0 {
					t.Fatalf("failure polluted stdout: %q", output.String())
				}

				if quiet != "" {
					if logs.Len() != 0 {
						t.Fatalf("quiet failure produced output: %q", logs.String())
					}

					return
				}

				if scenario.cancelled {
					if !errors.Is(err, context.Canceled) || !strings.HasSuffix(logs.String(), "?? cancelled\n") {
						t.Fatalf("unexpected cancellation: %v, %q", err, logs.String())
					}
				} else if !strings.Contains(logs.String(), "!! ") {
					t.Fatalf("missing error: %q", logs.String())
				}

				if strings.Contains(logs.String(), "packed ") || strings.ContainsAny(logs.String(), "\r\x1b") {
					t.Fatalf("unexpected failure output: %q", logs.String())
				}
			})
		}
	}
}

func TestQuietArgumentBoundaries(t *testing.T) {
	cases := []quietArgumentCase{
		{name: "enabled", arguments: []string{"pack", "--invalid", "--quiet=true"}, quiet: true},
		{name: "disabled", arguments: []string{"pack", "--invalid", "--quiet=false"}},
		{name: "overridden", arguments: []string{"-q", "pack", "--invalid", "--quiet=false"}},
		{name: "output", arguments: []string{"pack", "-o", "--quiet", "--threads", "0"}},
		{name: "input", arguments: []string{"pack", "--threads", "0", "--", "--quiet"}},
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			var logs bytes.Buffer

			application := command()
			application.Writer = io.Discard
			application.ErrWriter = &logs

			arguments := append([]string{"pakr"}, scenario.arguments...)

			err := run(context.Background(), application, arguments)
			if err == nil {
				t.Fatal("expected invalid arguments to fail")
			}

			if (logs.Len() == 0) != scenario.quiet {
				t.Fatalf("unexpected quiet handling: %q", logs.String())
			}
		})
	}
}

func TestNativeFailureOutput(t *testing.T) {
	input := os.Getenv("PAKR_TEST_NATIVE_INPUT")
	if input != "" {
		arguments := []string{"pakr", "unpack", input, "-o", t.TempDir(), "--quiet=" + os.Getenv("PAKR_TEST_NATIVE_QUIET")}

		err := run(context.Background(), command(), arguments)
		if err == nil {
			t.Fatal("truncated gzip accepted")
		}

		return
	}

	var compressed bytes.Buffer

	codec := gzip.NewWriter(&compressed)

	_, err := codec.Write(bytes.Repeat([]byte("truncated gzip data\n"), 256))
	if err != nil {
		t.Fatal(err)
	}

	err = codec.Close()
	if err != nil {
		t.Fatal(err)
	}

	input = filepath.Join(t.TempDir(), "truncated.gz")

	data := compressed.Bytes()

	err = os.WriteFile(input, data[:len(data)-8], 0o600)
	if err != nil {
		t.Fatal(err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	quietFlags := []string{"false", "true"}

	for _, quiet := range quietFlags {
		t.Run(quiet, func(t *testing.T) {
			var logs bytes.Buffer

			// A subprocess also captures native diagnostics that bypass Go's stderr writer.
			process := exec.Command(executable, "-test.run=^TestNativeFailureOutput$")
			process.Env = append(os.Environ(), "PAKR_TEST_NATIVE_INPUT="+input, "PAKR_TEST_NATIVE_QUIET="+quiet)
			process.Stderr = &logs

			output, err := process.Output()
			if err != nil {
				t.Fatalf("native failure check: %v\n%s\n%s", err, output, logs.String())
			}

			if quiet == "true" {
				if logs.Len() != 0 {
					t.Fatalf("quiet native failure produced output: %q", logs.String())
				}

				return
			}

			if strings.Count(logs.String(), "\n") != 2 || !strings.Contains(logs.String(), "\n!! ") {
				t.Fatalf("unexpected native error output: %q", logs.String())
			}
		})
	}
}
