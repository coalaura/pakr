package pak

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestZipEntryLimitPreflight(t *testing.T) {
	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	names := []string{"one", "two", "three"}

	for _, name := range names {
		_, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	// A forged EOCD count must not bypass counting the actual directory records.
	data := archive.Bytes()

	binary.LittleEndian.PutUint16(data[len(data)-14:], 1)
	binary.LittleEndian.PutUint16(data[len(data)-12:], 1)

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	budget := newExtractionBudget(ctx, &ExtractionLimits{MaxEntries: 2}, cancel)

	err = checkZipEntries(bytes.NewReader(data), int64(len(data)), budget)
	if !errors.Is(err, ErrExtractionLimit) || !errors.Is(context.Cause(ctx), ErrExtractionLimit) {
		t.Fatalf("preflight: %v, %v", err, context.Cause(ctx))
	}
}

func TestRawStdinLimit(t *testing.T) {
	var archive bytes.Buffer

	directory := t.TempDir()
	input := filepath.Join(directory, "input")

	err := os.WriteFile(input, bytes.Repeat([]byte("data"), 1<<19), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	format, _ := ParseFormat("zst")

	err = Pack(context.Background(), []string{input}, Options{Format: format, Output: "-", Level: 3, Threads: 4}, &archive)
	if err != nil {
		t.Fatal(err)
	}

	output := t.TempDir()

	err = Unpack(context.Background(), "-", Options{Format: format, Output: output, Threads: 4, Limits: &ExtractionLimits{MaxOutput: 12345}}, &archive)
	if !errors.Is(err, ErrExtractionLimit) {
		t.Fatalf("unknown-size stdin limit: %v", err)
	}

	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial output remains: %v, %v", entries, err)
	}
}

func TestZipEntryPreflightCompatibility(t *testing.T) {
	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	_, err := writer.Create("empty")
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	data := archive.Bytes()
	end := len(data) - 22

	extended := make([]byte, 76)

	binary.LittleEndian.PutUint32(extended, 0x06064b50)
	binary.LittleEndian.PutUint64(extended[4:], 44)
	binary.LittleEndian.PutUint64(extended[24:], 1)
	binary.LittleEndian.PutUint64(extended[32:], 1)
	binary.LittleEndian.PutUint64(extended[40:], uint64(binary.LittleEndian.Uint32(data[end+12:])))
	binary.LittleEndian.PutUint64(extended[48:], uint64(binary.LittleEndian.Uint32(data[end+16:])))
	binary.LittleEndian.PutUint32(extended[56:], 0x07064b50)
	binary.LittleEndian.PutUint64(extended[64:], uint64(end))
	binary.LittleEndian.PutUint32(extended[72:], 1)

	zip64 := append(bytes.Clone(data[:end]), extended...)
	zip64 = append(zip64, data[end:]...)

	binary.LittleEndian.PutUint16(zip64[len(zip64)-12:], 0xffff)

	archives := [][]byte{data, append([]byte("executable prefix"), data...), zip64}

	for _, encoded := range archives {
		ctx, cancel := context.WithCancelCause(context.Background())

		budget := newExtractionBudget(ctx, &ExtractionLimits{MaxEntries: 1}, cancel)

		err = checkZipEntries(bytes.NewReader(encoded), int64(len(encoded)), budget)
		cancel(nil)

		if err != nil {
			t.Fatal(err)
		}

		reader, readErr := zip.NewReader(bytes.NewReader(encoded), int64(len(encoded)))
		if readErr != nil || len(reader.File) != 1 {
			t.Fatalf("invalid compatibility fixture: %v", readErr)
		}
	}
}

func TestMetadataEntryLimits(t *testing.T) {
	var archive bytes.Buffer

	writer := tar.NewWriter(&archive)

	headers := []tar.Header{
		{Name: "directory", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "directory", Mode: 0o777},
	}

	for index := range headers {
		err := writer.WriteHeader(&headers[index])
		if err != nil {
			t.Fatal(err)
		}
	}

	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	format, _ := ParseFormat("tar")

	output := t.TempDir()

	err = Unpack(context.Background(), "-", Options{Format: format, Output: output, Threads: 1, Limits: &ExtractionLimits{MaxEntries: 1}}, bytes.NewReader(archive.Bytes()))
	if !errors.Is(err, ErrExtractionLimit) {
		t.Fatalf("metadata entries bypassed count: %v", err)
	}

	_, err = os.Lstat(filepath.Join(output, "link"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("over-limit link created: %v", err)
	}
}

func TestExtractionLimits(t *testing.T) {
	formats := []string{"tar", "tar.gz", "tar.zst", "zip", "7z", "gz", "zst", "bz2", "xz", "lz4", "br"}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			input := filepath.Join(directory, "input")

			data := bytes.Repeat([]byte("bounded output\n"), 100000)

			err := os.WriteFile(input, data, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			archive := filepath.Join(directory, "archive."+name)

			err = Pack(context.Background(), []string{input}, Options{Format: format, Output: archive, Level: 3, Threads: 4}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			limits := []ExtractionLimits{{MaxOutput: 1 << 20}, {MaxFileSize: 1 << 20}, {MaxOutput: uint64(len(data)), MaxFileSize: uint64(len(data)), MaxEntries: 1}}

			for index, limit := range limits {
				output := t.TempDir()
				err = Unpack(context.Background(), archive, Options{Format: format, Output: output, Threads: 4, Limits: &limit}, nil)

				if index == 2 {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if !errors.Is(err, ErrExtractionLimit) {
						t.Fatalf("limit %+v: %v", limit, err)
					}

					entries, readErr := os.ReadDir(output)
					if readErr != nil || len(entries) != 0 {
						t.Fatalf("partial output: %v, %v", entries, readErr)
					}
				}
			}
		})
	}
}

func TestArchiveEntryAndTotalLimits(t *testing.T) {
	formats := []string{"tar", "zip", "7z"}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			inputs := []string{filepath.Join(directory, "one"), filepath.Join(directory, "two")}

			for _, input := range inputs {
				err := os.WriteFile(input, []byte("12345678"), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			format, _ := ParseFormat(name)

			archive := filepath.Join(directory, "archive."+name)

			err := Pack(context.Background(), inputs, Options{Format: format, Output: archive, Level: 3, Threads: 8}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			limits := []ExtractionLimits{{MaxEntries: 1}, {MaxOutput: 15}}

			for _, limit := range limits {
				output := t.TempDir()

				err = Unpack(context.Background(), archive, Options{Format: format, Output: output, Threads: 8, Limits: &limit}, nil)
				if !errors.Is(err, ErrExtractionLimit) {
					t.Fatalf("limit %+v: %v", limit, err)
				}

				entries, _ := os.ReadDir(output)
				if name == "zip" && len(entries) != 0 {
					t.Fatal("ZIP did not reject all declared sizes before dispatch")
				}
			}
		})
	}
}

func TestSharedExtractionBudget(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	budget := newExtractionBudget(ctx, &ExtractionLimits{MaxOutput: 1024}, cancel)

	var pending sync.WaitGroup

	for range 16 {
		pending.Go(func() {
			writer := extractionWriter{target: io.Discard, budget: budget, declared: -1}

			for range 20 {
				_, err := writer.Write(make([]byte, 16))
				if err != nil {
					return
				}
			}
		})
	}

	pending.Wait()

	if budget.output != 1024 || !errors.Is(context.Cause(ctx), ErrExtractionLimit) {
		t.Fatalf("shared budget: %d, %v", budget.output, context.Cause(ctx))
	}

	budget = newExtractionBudget(ctx, &ExtractionLimits{}, cancel)
	budget.output = math.MaxUint64 - 1

	err := budget.entry(2)
	if !errors.Is(err, ErrExtractionLimit) {
		t.Fatalf("overflow accepted: %v", err)
	}
}
