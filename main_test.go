package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/coalaura/pakr/internal/pak"
	"github.com/urfave/cli/v3"
)

type commandOptionsCase struct {
	name      string
	arguments []string
	threads   int
	level     int
	extreme   bool
	memory    uint64
}

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

func TestExtractionOnlyCommands(t *testing.T) {
	data, err := os.ReadFile("internal/pak/testdata/archives.json")
	if err != nil {
		t.Fatal(err)
	}

	var fixtures map[string]string

	err = json.Unmarshal(data, &fixtures)
	if err != nil {
		t.Fatal(err)
	}

	references := map[string]string{"zipx": "test_read_format_zip_bzip2.zipx", "rar": "test_read_format_rar5_stored.rar"}

	for format, reference := range references {
		t.Run(format, func(t *testing.T) {
			archive, err := base64.StdEncoding.DecodeString(fixtures[reference])
			if err != nil {
				t.Fatal(err)
			}

			input := filepath.Join(t.TempDir(), "archive."+format)

			err = os.WriteFile(input, archive, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			arguments := [][]string{
				{"pakr", "unpack", input, "-o", t.TempDir(), "-q"},
				{"pakr", "unpack", "-", "-o", t.TempDir(), "-q"},
				{"pakr", "unpack", "-", "-f", format, "-o", t.TempDir(), "-q"},
			}

			for _, flags := range arguments {
				application := command()
				application.Reader = bytes.NewReader(archive)
				application.ErrWriter = io.Discard

				err = application.Run(t.Context(), flags)
				if err != nil {
					t.Fatalf("%v: %v", flags, err)
				}
			}

			output := filepath.Join(t.TempDir(), "archive."+format)
			arguments = [][]string{
				{"pakr", "pack", input, "-f", format, "-o", "-", "-q"},
				{"pakr", "pack", input, "-o", output, "-q"},
				{"pakr", "pack", input, "-f", "zip", "-o", output, "-q"},
			}

			for _, flags := range arguments {
				application := command()
				application.Writer = io.Discard
				application.ErrWriter = io.Discard

				err = application.Run(t.Context(), flags)
				if err == nil || !strings.Contains(err.Error(), "extraction-only") {
					t.Fatalf("%v: expected extraction-only error, got %v", flags, err)
				}
			}

			_, err = os.Stat(output)
			if !os.IsNotExist(err) {
				t.Fatalf("creation left output behind: %v", err)
			}
		})
	}

	var output bytes.Buffer

	application := command()
	application.Writer = &output

	err = application.Run(t.Context(), []string{"pakr", "--formats"})
	if err != nil {
		t.Fatal(err)
	}

	terms := []string{"zipx", "rar", "Extraction-only", "RAR4", "RAR5", "PPMd", "\nlz "}

	for _, term := range terms {
		if !strings.Contains(output.String(), term) {
			t.Fatalf("--formats missing %q", term)
		}
	}
}

func TestExtractionLimitOptions(t *testing.T) {
	arguments := [][]string{
		{},
		{"--max-output", "2TiB", "--max-file-size", "4GiB", "--max-entries", "42"},
		{"--max-output", "0", "--max-file-size", "0", "--max-entries", "0"},
	}

	expected := []pak.ExtractionLimits{
		{MaxOutput: pak.DefaultMaxOutput, MaxFileSize: pak.DefaultMaxFileSize, MaxEntries: pak.DefaultMaxEntries},
		{MaxOutput: 2 << 40, MaxFileSize: 4 << 30, MaxEntries: 42},
		{},
	}

	for index, flags := range arguments {
		application := command()

		for _, subcommand := range application.Commands {
			if subcommand.Name != "unpack" {
				continue
			}

			subcommand.Action = func(_ context.Context, parsed *cli.Command) error {
				options, err := optionsFor(parsed, false)
				if err != nil {
					return err
				}

				if options.Limits == nil || *options.Limits != expected[index] {
					t.Fatalf("limits: %+v", options.Limits)
				}

				return nil
			}
		}

		err := application.Run(context.Background(), append([]string{"pakr", "unpack"}, flags...))
		if err != nil {
			t.Fatal(err)
		}
	}

	invalid := []string{"-1", "1.5GiB", "1GB", "18446744073709551616", "16777216TiB", "KiB"}

	for _, input := range invalid {
		_, err := parseSize(input)
		if err == nil {
			t.Fatalf("accepted invalid size %q", input)
		}
	}
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

func TestCommandOptions(t *testing.T) {
	autoThreads := 1
	if runtime.NumCPU() == autoThreads {
		autoThreads = 2
	}

	previous := runtime.GOMAXPROCS(autoThreads)
	defer runtime.GOMAXPROCS(previous)

	allThreads := min(runtime.NumCPU(), 256)

	cases := []commandOptionsCase{
		{name: "pack-default", arguments: []string{"pack"}, threads: autoThreads, level: 3},
		{name: "unpack-default", arguments: []string{"unpack"}, threads: autoThreads, level: 3},
		{name: "pack-memory-auto", arguments: []string{"pack", "--memory", "auto"}, threads: autoThreads, level: 3},
		{name: "unpack-memory-auto", arguments: []string{"unpack", "--memory", "auto"}, threads: autoThreads, level: 3},
		{name: "explicit-memory", arguments: []string{"pack", "--memory", "2GiB"}, threads: autoThreads, level: 3, memory: 2 << 30},
		{name: "pack-all", arguments: []string{"pack", "--threads", "all"}, threads: allThreads, level: 3},
		{name: "unpack-all", arguments: []string{"unpack", "-t", "all"}, threads: allThreads, level: 3},
		{name: "max", arguments: []string{"pack", "--max"}, threads: allThreads, level: 9, extreme: true},
		{name: "max-disabled", arguments: []string{"pack", "--max=false"}, threads: autoThreads, level: 3},
		{name: "max-threads-after", arguments: []string{"pack", "--max", "--threads", "4"}, threads: 4, level: 9, extreme: true},
		{name: "max-threads-before", arguments: []string{"pack", "-t", "4", "--max"}, threads: 4, level: 9, extreme: true},
		{name: "max-auto-after", arguments: []string{"pack", "--max", "--threads", "auto"}, threads: autoThreads, level: 9, extreme: true},
		{name: "max-auto-before", arguments: []string{"pack", "-t", "auto", "--max"}, threads: autoThreads, level: 9, extreme: true},
		{name: "max-level-after", arguments: []string{"pack", "--max", "--level", "5"}, threads: allThreads, level: 5},
		{name: "max-level-before", arguments: []string{"pack", "-l", "5", "--max"}, threads: allThreads, level: 5},
		{name: "max-default-level-after", arguments: []string{"pack", "--max", "--level", "3"}, threads: allThreads, level: 3},
		{name: "max-default-level-before", arguments: []string{"pack", "-l", "3", "--max"}, threads: allThreads, level: 3},
		{name: "max-fast-after", arguments: []string{"pack", "--max", "--preset", "fast"}, threads: allThreads, level: 1},
		{name: "max-fast-before", arguments: []string{"pack", "-p", "fast", "--max"}, threads: allThreads, level: 1},
		{name: "max-balanced-after", arguments: []string{"pack", "--max", "--preset", "balanced"}, threads: allThreads, level: 3},
		{name: "max-balanced-before", arguments: []string{"pack", "-p", "balanced", "--max"}, threads: allThreads, level: 3},
		{name: "max-best", arguments: []string{"pack", "--max", "--preset", "best"}, threads: allThreads, level: 9},
		{name: "max-combined", arguments: []string{"pack", "-t", "4", "--max", "-l", "3"}, threads: 4, level: 3},
		{name: "bzip3-max-budget", arguments: []string{"pack", "-f", "bz3", "--max", "--memory", "8GiB"}, threads: allThreads, level: 9, extreme: true, memory: 8 << 30},
		{name: "bzip3-decode-budget", arguments: []string{"unpack", "--memory", "256MiB"}, threads: autoThreads, level: 3, memory: 256 << 20},
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			application := command()
			application.Writer = io.Discard
			application.ErrWriter = io.Discard

			called := false

			for _, subcommand := range application.Commands {
				subcommand.Action = func(_ context.Context, parsed *cli.Command) error {
					called = true

					options, err := optionsFor(parsed, parsed.Name == "pack")
					if err != nil {
						return err
					}

					if options.Threads != scenario.threads || options.Level != scenario.level {
						t.Fatalf("got threads=%d level=%d, want threads=%d level=%d", options.Threads, options.Level, scenario.threads, scenario.level)
					}

					if options.Extreme != scenario.extreme {
						t.Fatalf("got extreme=%t, want %t", options.Extreme, scenario.extreme)
					}

					if options.Memory != scenario.memory {
						t.Fatalf("got codec memory=%d, want %d", options.Memory, scenario.memory)
					}

					return nil
				}
			}

			arguments := append([]string{"pakr"}, scenario.arguments...)

			err := application.Run(context.Background(), arguments)
			if err != nil {
				t.Fatal(err)
			}

			if !called {
				t.Fatal("command action was not called")
			}
		})
	}
}

func TestInvalidOptions(t *testing.T) {
	cases := [][]string{
		{"--threads", "0"},
		{"--threads", "257"},
		{"--threads", "unknown"},
		{"--level", "0"},
		{"--level", "10"},
		{"--preset", "unknown"},
		{"--memory", "0"},
		{"--memory", "-1"},
		{"--memory", "8GB"},
		{"--preset", "fast", "--level", "1"},
		{"--format", "unknown"},
		{"--max", "--threads", "0"},
		{"--max", "--threads", "257"},
		{"--max", "--threads", "unknown"},
		{"--max", "--level", "0"},
		{"--max", "--level", "10"},
		{"--max", "--preset", "unknown"},
		{"--max", "--preset", "fast", "--level", "1"},
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

func TestUnpackRejectsMax(t *testing.T) {
	application := command()
	application.Writer = io.Discard
	application.ErrWriter = io.Discard

	err := application.Run(context.Background(), []string{"pakr", "unpack", "unused", "--max"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -max") {
		t.Fatalf("expected unknown max flag, got %v", err)
	}
}

func TestCommandStatusPipeline(t *testing.T) {
	formats := []string{"tar", "tar.zst", "tar.bz3", "tar.lz", "tlz", "zip", "7z", "gz", "bz3", "lz", "lzip"}

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

			if format == "gz" || format == "bz3" || format == "lz" || format == "lzip" {
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
