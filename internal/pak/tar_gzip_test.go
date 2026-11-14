package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

type tarGzipProbeCase struct {
	name    string
	entries []tarWorkerEntry
	want    bool
}

func TestTarGzipParallel(t *testing.T) {
	format, err := ParseFormat("tar.gz")
	if err != nil {
		t.Fatal(err)
	}

	small := tarWorkerEntry{header: tar.Header{Name: "small", Mode: 0o600}, data: "small"}
	large := tarWorkerEntry{header: tar.Header{Name: strings.Repeat("long/", 40) + "large", Mode: 0o600}, data: strings.Repeat("x", tarWorkerSize+1)}
	directory := tarWorkerEntry{header: tar.Header{Name: "parent/", Typeflag: tar.TypeDir, Mode: 0o700}}
	metadata := make([]tarWorkerEntry, native.BufferSize/512+1)

	for index := range metadata {
		metadata[index] = directory
	}

	metadata = append(metadata, large)

	cases := []tarGzipProbeCase{
		{name: "empty"},
		{name: "small-first", entries: []tarWorkerEntry{small, large}},
		{name: "large-first", entries: []tarWorkerEntry{large, small}, want: true},
		{name: "directory-prefix", entries: []tarWorkerEntry{directory, large}, want: true},
		{name: "bounded-metadata", entries: metadata},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			encoded := encodeCompressedTar(t, format, tarWorkerArchive(t, test.entries))
			input := filepath.Join(t.TempDir(), "input.tar.gz")

			err := os.WriteFile(input, encoded, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			file, err := os.Open(input)
			if err != nil {
				t.Fatal(err)
			}

			defer file.Close()

			_, err = file.Seek(7, io.SeekStart)
			if err != nil {
				t.Fatal(err)
			}

			parallel, err := tarGzipParallel(t.Context(), file, int64(len(encoded)))
			if err != nil || parallel != test.want {
				t.Fatalf("parallel = %v, error = %v; want %v", parallel, err, test.want)
			}

			offset, err := file.Seek(0, io.SeekCurrent)
			if err != nil || offset != 7 {
				t.Fatalf("input offset changed: %d, %v", offset, err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err = tarGzipParallel(ctx, file, int64(len(encoded)))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestCompressedTarLargeFirst(t *testing.T) {
	modified := time.Unix(1700000000, 0)

	format, err := ParseFormat("tar.gz")
	if err != nil {
		t.Fatal(err)
	}

	entries := []tarWorkerEntry{
		{header: tar.Header{Name: "parent/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: modified}},
		{header: tar.Header{Name: "parent/large", Mode: 0o600, ModTime: modified}, data: string(compressedTarNoise())},
		{header: tar.Header{Name: "small", Mode: 0o600, ModTime: modified}, data: "small"},
	}

	encoded := encodeCompressedTar(t, format, tarWorkerArchive(t, entries))
	if len(encoded) < 1<<20 {
		t.Fatal("fixture does not exercise parallel decoding")
	}

	output, err := unpackCompressedTar(t, t.Context(), format, encoded, 4, true, nil)
	if err != nil {
		t.Fatal(err)
	}

	checkTarWorkerEntries(t, output, entries)

	corrupt := bytes.Clone(encoded)
	corrupt[len(corrupt)-1] ^= 0x80
	variants := [][]byte{corrupt, encoded[:len(encoded)-1]}

	for _, variant := range variants {
		_, err = unpackCompressedTar(t, t.Context(), format, variant, 4, true, nil)
		if err == nil {
			t.Fatal("accepted invalid gzip footer")
		}
	}

	_, err = unpackCompressedTar(t, t.Context(), format, encoded, 4, true, &ExtractionLimits{MaxFileSize: tarWorkerSize})
	if !errors.Is(err, ErrExtractionLimit) {
		t.Fatalf("file size limit lost: %v", err)
	}
}
