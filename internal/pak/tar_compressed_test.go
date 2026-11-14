package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type compressedTarFailure struct {
	name    string
	entries []tarWorkerEntry
	limits  *ExtractionLimits
	want    error
}

func TestCompressedTarWorkers(t *testing.T) {
	modified := time.Unix(1700000000, 0)
	entries := make([]tarWorkerEntry, 0, 70)

	for index := range 64 {
		entries = append(entries, tarWorkerEntry{header: tar.Header{Name: fmt.Sprintf("parent/branch%d/file%d", index%4, index), Mode: 0o640, ModTime: modified}, data: strings.Repeat("payload", index)})
	}

	entries = append(entries,
		tarWorkerEntry{header: tar.Header{Name: strings.Repeat("deep/", 36) + "file", Mode: 0o600, ModTime: modified}, data: "deep"},
		tarWorkerEntry{header: tar.Header{Name: "large", Mode: 0o640, ModTime: modified}, data: string(compressedTarNoise())},
		tarWorkerEntry{header: tar.Header{Name: "linked", Typeflag: tar.TypeLink, Linkname: "parent/branch1/file1"}},
		tarWorkerEntry{header: tar.Header{Name: "symbolic", Typeflag: tar.TypeSymlink, Linkname: "parent/branch1/file1"}},
		tarWorkerEntry{header: tar.Header{Name: "parent/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: modified}},
	)

	formats := []string{"tar.gz", "tar.xz"}
	counts := []int{1, 4, 16}
	modes := []bool{false, true}

	for _, name := range formats {
		format, err := ParseFormat(name)
		if err != nil {
			t.Fatal(err)
		}

		encoded := encodeCompressedTar(t, format, tarWorkerArchive(t, entries))
		if len(encoded) < 1<<20 {
			t.Fatal("fixture does not exercise file read-ahead")
		}

		for _, threads := range counts {
			for _, file := range modes {
				t.Run(fmt.Sprintf("%s/%d/file=%v", name, threads, file), func(t *testing.T) {
					output, unpackErr := unpackCompressedTar(t, t.Context(), format, encoded, threads, file, nil)
					if unpackErr != nil {
						t.Fatal(unpackErr)
					}

					checkTarWorkerEntries(t, output, entries)
				})
			}
		}
	}
}

func TestCompressedTarValidation(t *testing.T) {
	formats := []string{"tar.gz", "tar.xz", "tar.bz2", "tar.zst", "tar.lz4", "tar.br"}
	modes := []bool{false, true}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			empty := encodeCompressedTar(t, format, tarWorkerArchive(t, nil))
			entries := []tarWorkerEntry{{header: tar.Header{Name: "complete", Mode: 0o600}, data: "payload"}}

			// Put the codec footer beyond both the TAR end marker and read-ahead.
			archive := tarWorkerArchive(t, entries)
			archive = append(archive, compressedTarNoise()...)
			encoded := encodeCompressedTar(t, format, archive)

			corrupt := bytes.Clone(encoded)
			corrupt[len(corrupt)-1] ^= 0x80

			partial := []tarWorkerEntry{{header: tar.Header{Name: "partial", Mode: 0o600}, data: strings.Repeat("x", tarWorkerSize+1)}}
			truncated := encodeCompressedTar(t, format, tarWorkerArchive(t, partial)[:515])

			for _, file := range modes {
				output, unpackErr := unpackCompressedTar(t, t.Context(), format, empty, 4, file, nil)
				if unpackErr != nil {
					t.Fatalf("empty: %v", unpackErr)
				}

				files, readErr := os.ReadDir(output)
				if readErr != nil || len(files) != 0 {
					t.Fatalf("empty output: %v, %v", files, readErr)
				}

				variants := [][]byte{corrupt, encoded[:len(encoded)-1], truncated, {0, 1, 2}}

				for index, variant := range variants {
					output, unpackErr = unpackCompressedTar(t, t.Context(), format, variant, 4, file, nil)
					if unpackErr == nil {
						t.Fatalf("accepted invalid stream %d, file=%v", index, file)
					}

					_, statErr := os.Stat(filepath.Join(output, "partial"))
					if !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("incomplete file retained: %v", statErr)
					}
				}
			}
		})
	}
}

func TestCompressedTarWorkerFailures(t *testing.T) {
	first := tarWorkerEntry{header: tar.Header{Name: "first", Mode: 0o600}, data: "original"}

	cases := []compressedTarFailure{
		{name: "duplicate", entries: []tarWorkerEntry{first, {header: tar.Header{Name: "./first"}, data: "replacement"}}, want: os.ErrExist},
		{name: "file-parent", entries: []tarWorkerEntry{first, {header: tar.Header{Name: "first/child"}, data: "child"}}},
		{name: "directory-file", entries: []tarWorkerEntry{{header: tar.Header{Name: "first/child"}, data: "child"}, first}},
		{name: "escape", entries: []tarWorkerEntry{first, {header: tar.Header{Name: "../outside"}, data: "escape"}}},
		{name: "symlink-parent", entries: []tarWorkerEntry{first, {header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "."}}, {header: tar.Header{Name: "link/child"}, data: "escape"}}},
		{name: "entries", entries: []tarWorkerEntry{first}, limits: &ExtractionLimits{MaxEntries: 1}, want: ErrExtractionLimit},
		{name: "output", entries: []tarWorkerEntry{first}, limits: &ExtractionLimits{MaxOutput: 1}, want: ErrExtractionLimit},
		{name: "size", entries: []tarWorkerEntry{first}, limits: &ExtractionLimits{MaxFileSize: 1}, want: ErrExtractionLimit},
	}

	formats := []string{"tar.gz", "tar.xz"}
	modes := []bool{false, true}
	noise := string(compressedTarNoise())

	for _, name := range formats {
		format, err := ParseFormat(name)
		if err != nil {
			t.Fatal(err)
		}

		for _, test := range cases {
			t.Run(name+"/"+test.name, func(t *testing.T) {
				// Start workers before reaching the failure and leave compressed data unread.
				entries := make([]tarWorkerEntry, 0, 66+len(test.entries))

				for index := range 64 {
					entries = append(entries, tarWorkerEntry{header: tar.Header{Name: fmt.Sprintf("other/%d", index), Mode: 0o600}, data: "queued"})
				}

				entries = append(entries, test.entries...)
				entries = append(entries, tarWorkerEntry{header: tar.Header{Name: "large", Mode: 0o600}, data: noise})
				encoded := encodeCompressedTar(t, format, tarWorkerArchive(t, entries))

				for _, file := range modes {
					output, unpackErr := unpackCompressedTar(t, t.Context(), format, encoded, 4, file, test.limits)
					if unpackErr == nil || test.want != nil && !errors.Is(unpackErr, test.want) {
						t.Fatalf("failure lost: %v", unpackErr)
					}

					if test.name == "duplicate" || test.name == "file-parent" {
						actual, readErr := os.ReadFile(filepath.Join(output, "first"))
						if readErr != nil || string(actual) != "original" {
							t.Fatalf("first entry changed: %q, %v", actual, readErr)
						}
					}
				}
			})
		}
	}
}

func TestCompressedTarCancellationAndExistingOutput(t *testing.T) {
	entries := []tarWorkerEntry{
		{header: tar.Header{Name: "complete", Mode: 0o600}, data: "complete"},
		{header: tar.Header{Name: "partial", Mode: 0o600}, data: string(compressedTarNoise())},
	}

	formats := []string{"tar.gz", "tar.xz"}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			encoded := encodeCompressedTar(t, format, tarWorkerArchive(t, entries))
			output := t.TempDir()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			source := &tarCancelReader{source: bytes.NewReader(encoded), cancel: cancel, remaining: 32 << 10}
			options := Options{Format: format, Output: output, Threads: 4}

			err = Unpack(ctx, "-", options, source)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}

			_, err = os.Stat(filepath.Join(output, "partial"))
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial output retained: %v", err)
			}

			input := filepath.Join(t.TempDir(), name)

			err = os.WriteFile(input, encoded, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			options.Output = t.TempDir()
			existing := filepath.Join(options.Output, "complete")

			err = os.WriteFile(existing, []byte("original"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			err = Unpack(t.Context(), input, options, nil)
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("existing output accepted: %v", err)
			}

			actual, err := os.ReadFile(existing)
			if err != nil || string(actual) != "original" {
				t.Fatalf("existing output changed: %q, %v", actual, err)
			}

			err = Unpack(ctx, input, options, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("file cancellation lost: %v", err)
			}

			err = os.Remove(input)
			if err != nil {
				t.Fatalf("input handle retained: %v", err)
			}
		})
	}
}

func encodeCompressedTar(t *testing.T, format Format, data []byte) []byte {
	t.Helper()

	format.Container = ""

	return encodeStreamTest(t, format, data)
}

func unpackCompressedTar(t *testing.T, ctx context.Context, format Format, encoded []byte, threads int, file bool, limits *ExtractionLimits) (string, error) {
	t.Helper()

	output := t.TempDir()
	input := "-"

	var source io.Reader = bytes.NewReader(encoded)

	if file {
		input = filepath.Join(t.TempDir(), format.Name)

		err := os.WriteFile(input, encoded, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		source = nil
	}

	options := Options{Format: format, Output: output, Threads: threads, Limits: limits}
	err := Unpack(ctx, input, options, source)

	if file {
		removeErr := os.Remove(input)
		if removeErr != nil {
			t.Fatalf("input handle retained: %v", removeErr)
		}
	}

	return output, err
}

func compressedTarNoise() []byte {
	data := make([]byte, 3<<20)

	random := rand.NewChaCha8([32]byte{17})
	_, _ = random.Read(data)

	return data
}
