package pak

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

type tarWorkerEntry struct {
	header tar.Header
	data   string
}

type tarCancelReader struct {
	source    *bytes.Reader
	cancel    context.CancelFunc
	remaining int
}

func (reader *tarCancelReader) Read(buffer []byte) (int, error) {
	count, err := reader.source.Read(buffer[:min(len(buffer), 512)])

	reader.remaining -= count
	if reader.remaining <= 0 {
		reader.cancel()
	}

	return count, err
}

func TestTarWorkers(t *testing.T) {
	modified := time.Unix(1700000000, 0)
	entries := make([]tarWorkerEntry, 0, 68)

	for index := range 64 {
		name := fmt.Sprintf("parent/branch%d/file%d", index%4, index)
		data := strings.Repeat(fmt.Sprintf("payload%d", index), index*16)

		entries = append(entries, tarWorkerEntry{header: tar.Header{Name: name, Mode: 0o640, ModTime: modified}, data: data})
	}

	entries = append(entries,
		tarWorkerEntry{header: tar.Header{Name: "large", Mode: 0o640, ModTime: modified}, data: strings.Repeat("x", tarWorkerSize+1)},
		tarWorkerEntry{header: tar.Header{Name: "last", Mode: 0o640, ModTime: modified}, data: "last payload"},
		tarWorkerEntry{header: tar.Header{Name: "linked", Typeflag: tar.TypeLink, Linkname: "last"}},
		tarWorkerEntry{header: tar.Header{Name: "parent/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: modified}},
	)

	encoded := tarWorkerArchive(t, entries)
	counts := []int{0, 1, 2, 4, 256}

	for _, threads := range counts {
		t.Run(fmt.Sprint(threads), func(t *testing.T) {
			output := t.TempDir()
			options := Options{Format: Format{Name: "tar", Container: "tar"}, Output: output, Threads: threads}

			err := Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}

			checkTarWorkerEntries(t, output, entries)
		})
	}

	t.Run("gzip", func(t *testing.T) {
		var compressed bytes.Buffer

		writer := gzip.NewWriter(&compressed)

		_, err := writer.Write(encoded)
		if err != nil {
			t.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			t.Fatal(err)
		}

		output := t.TempDir()
		options := Options{Format: Format{Name: "tar.gz", Container: "tar", Codec: native.Gzip}, Output: output, Threads: 4}

		err = Unpack(t.Context(), "-", options, &compressed)
		if err != nil {
			t.Fatal(err)
		}

		checkTarWorkerEntries(t, output, entries)
	})
}

func TestTarWorkerConflicts(t *testing.T) {
	sequences := [][]tarWorkerEntry{
		{{header: tar.Header{Name: "entry", Mode: 0o600}, data: "first"}, {header: tar.Header{Name: "./entry"}, data: "second"}},
		{{header: tar.Header{Name: "entry", Mode: 0o600}, data: "first"}, {header: tar.Header{Name: "entry/child"}, data: "second"}},
		{{header: tar.Header{Name: "entry/child", Mode: 0o600}, data: "first"}, {header: tar.Header{Name: "entry"}, data: "second"}},
	}

	// Exercise the same conflicts after the first entry has been dispatched
	// to another worker, as well as while it is still buffered in one batch.
	for _, sequence := range sequences {
		entries := make([]tarWorkerEntry, 0, 34)
		entries = append(entries, sequence[0])

		for index := range 32 {
			entries = append(entries, tarWorkerEntry{header: tar.Header{Name: fmt.Sprintf("unrelated%d", index), Mode: 0o600}, data: "other"})
		}

		entries = append(entries, sequence[1])
		sequences = append(sequences, entries)
	}

	for index, entries := range sequences {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			output := t.TempDir()
			encoded := tarWorkerArchive(t, entries)

			options := Options{Format: Format{Name: "tar", Container: "tar"}, Output: output, Threads: 4}

			err := Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
			if err == nil {
				t.Fatal("conflicting entry accepted")
			}

			data, err := os.ReadFile(filepath.Join(output, entries[0].header.Name))
			if err != nil || string(data) != "first" {
				t.Fatalf("first entry changed: %q, %v", data, err)
			}
		})
	}
}

func TestTarWorkerFailureAtEOF(t *testing.T) {
	output := t.TempDir()
	filename := filepath.Join(output, "existing")

	err := os.WriteFile(filename, []byte("original"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	entries := []tarWorkerEntry{{header: tar.Header{Name: "existing"}, data: "replacement"}}
	encoded := tarWorkerArchive(t, entries)

	options := Options{Format: Format{Name: "tar", Container: "tar"}, Output: output, Threads: 4}

	err = Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("worker failure lost at TAR EOF: %v", err)
	}

	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "original" {
		t.Fatalf("pre-existing output changed: %q, %v", data, err)
	}
}

func TestTarWorkerTruncatedPayload(t *testing.T) {
	entries := []tarWorkerEntry{{header: tar.Header{Name: "partial"}, data: strings.Repeat("x", tarWorkerSize)}}
	encoded := tarWorkerArchive(t, entries)

	output := t.TempDir()
	options := Options{Format: Format{Name: "tar", Container: "tar"}, Output: output, Threads: 4}

	err := Unpack(t.Context(), "-", options, bytes.NewReader(encoded[:515]))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated payload: %v", err)
	}

	_, err = os.Stat(filepath.Join(output, "partial"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial output remains: %v", err)
	}
}

func TestTarWorkerLimits(t *testing.T) {
	entries := make([]tarWorkerEntry, 64)

	for index := range entries {
		entries[index] = tarWorkerEntry{header: tar.Header{Name: fmt.Sprint(index), Mode: 0o600}, data: "payload"}
	}

	encoded := tarWorkerArchive(t, entries)

	cases := map[string]ExtractionLimits{
		"exact":   {MaxEntries: 64, MaxOutput: 448, MaxFileSize: 7},
		"entries": {MaxEntries: 63},
		"output":  {MaxOutput: 447},
		"file":    {MaxFileSize: 6},
	}

	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			output := t.TempDir()
			options := Options{Format: Format{Name: "tar", Container: "tar"}, Output: output, Threads: 4, Limits: &limits}

			err := Unpack(t.Context(), "-", options, bytes.NewReader(encoded))

			if name == "exact" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrExtractionLimit) {
				t.Fatalf("queued entry bypassed limit: %v", err)
			}

			files, err := os.ReadDir(output)
			if err != nil {
				t.Fatal(err)
			}

			if name == "exact" && len(files) != len(entries) {
				t.Fatalf("missing completed entries: %d", len(files))
			}

			if limits.MaxEntries != 0 && uint64(len(files)) > limits.MaxEntries || limits.MaxOutput != 0 && uint64(len(files)*7) > limits.MaxOutput {
				t.Fatal("completed files exceed limit")
			}

			for _, file := range files {
				data, readErr := os.ReadFile(filepath.Join(output, file.Name()))
				if readErr != nil || string(data) != "payload" {
					t.Fatalf("partial output remains: %q, %v", data, readErr)
				}
			}
		})
	}
}

func TestTarWorkerCancellation(t *testing.T) {
	entries := make([]tarWorkerEntry, 64)

	for index := range entries {
		entries[index] = tarWorkerEntry{header: tar.Header{Name: fmt.Sprint(index), Mode: 0o600}, data: "payload"}
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	source := &tarCancelReader{source: bytes.NewReader(tarWorkerArchive(t, entries)), cancel: cancel, remaining: 32 * 1024}

	output := t.TempDir()
	options := Options{Format: Format{Name: "tar", Container: "tar"}, Output: output, Threads: 4}

	err := Unpack(ctx, "-", options, source)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}

	files, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		data, readErr := os.ReadFile(filepath.Join(output, file.Name()))
		if readErr != nil || string(data) != "payload" {
			t.Fatalf("partial output remains after cancellation: %q, %v", data, readErr)
		}
	}
}

func tarWorkerArchive(t *testing.T, entries []tarWorkerEntry) []byte {
	t.Helper()

	var encoded bytes.Buffer

	writer := tar.NewWriter(&encoded)

	for index := range entries {
		entry := &entries[index]

		header := entry.header
		header.Size = int64(len(entry.data))

		err := writer.WriteHeader(&header)
		if err != nil {
			t.Fatal(err)
		}

		_, err = io.WriteString(writer, entry.data)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}

func checkTarWorkerEntries(t *testing.T, output string, entries []tarWorkerEntry) {
	t.Helper()

	for index := range entries {
		entry := &entries[index]
		filename := filepath.Join(output, entry.header.Name)

		info, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}

		if entry.header.Typeflag == tar.TypeLink {
			target, statErr := os.Stat(filepath.Join(output, entry.header.Linkname))
			if statErr != nil || !os.SameFile(info, target) {
				t.Fatalf("hardlink target not completed: %v", statErr)
			}

			continue
		}

		if !info.ModTime().Equal(entry.header.ModTime) || runtime.GOOS != "windows" && info.Mode().Perm() != os.FileMode(entry.header.Mode) {
			t.Fatalf("metadata not restored for %s: %v, %v", entry.header.Name, info.Mode(), info.ModTime())
		}

		if info.IsDir() {
			continue
		}

		data, readErr := os.ReadFile(filename)
		if readErr != nil || string(data) != entry.data {
			t.Fatalf("payload mismatch for %s: %v", entry.header.Name, readErr)
		}
	}
}
