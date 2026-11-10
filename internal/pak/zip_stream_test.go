package pak

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

type zipInterruptWriter struct {
	cancel context.CancelFunc
	err    error
}

func (writer zipInterruptWriter) Write(data []byte) (int, error) {
	if writer.cancel != nil {
		writer.cancel()
	}

	if writer.err != nil {
		return 0, writer.err
	}

	return len(data), nil
}

func TestZipStreamingWorkers(t *testing.T) {
	data := archiveStreamTestData(3 * zipBufferLimit)
	base := t.TempDir()
	modified := time.Unix(1700000001, 0)
	inputs := make([]string, 3)

	for index := range inputs {
		inputs[index] = filepath.Join(base, "entry-\u00e9-"+strconv.Itoa(index))

		err := os.WriteFile(inputs[index], data, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Chtimes(inputs[index], modified, modified)
		if err != nil {
			t.Fatal(err)
		}
	}

	entries, _, err := collect(t.Context(), inputs, "")
	if err != nil {
		t.Fatal(err)
	}

	options := Options{Level: 3, Threads: 3}
	copying := copier{ctx: t.Context(), buffer: make([]byte, native.BufferSize)}

	var encoded bytes.Buffer

	err = packZip(&encoded, entries, options, &copying)
	if err != nil {
		t.Fatal(err)
	}

	archive, err := zip.NewReader(bytes.NewReader(encoded.Bytes()), int64(encoded.Len()))
	if err != nil {
		t.Fatal(err)
	}

	if len(archive.File) != len(inputs) {
		t.Fatalf("entry count: %d", len(archive.File))
	}

	for index, file := range archive.File {
		if file.Name != filepath.Base(inputs[index]) || file.Flags&0x808 != 0x808 || !file.Modified.Equal(modified) {
			t.Fatalf("streaming metadata changed: %+v", file.FileHeader)
		}

		if file.CRC32 != crc32.ChecksumIEEE(data) || file.UncompressedSize64 != uint64(len(data)) {
			t.Fatal("streaming descriptor changed")
		}

		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}

		actual, readErr := io.ReadAll(reader)
		reader.Close()

		if readErr != nil || !bytes.Equal(actual, data) {
			t.Fatalf("streaming payload changed: %v", readErr)
		}
	}

	modes := []string{"cancel", "output-error", "input-change"}

	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			working := copying
			working.ctx = ctx

			failure := errors.New("output failed")
			target := io.Discard

			switch mode {
			case "cancel":
				target = zipInterruptWriter{cancel: cancel}
			case "output-error":
				target = zipInterruptWriter{err: failure}
			case "input-change":
				truncateErr := os.Truncate(inputs[0], 1)
				if truncateErr != nil {
					t.Fatal(truncateErr)
				}
			}

			done := make(chan error, 1)

			go func() {
				done <- packZip(target, entries, options, &working)
			}()

			select {
			case packErr := <-done:
				if packErr == nil {
					t.Fatal("interrupted streaming succeeded")
				}

				if mode == "cancel" && !errors.Is(packErr, context.Canceled) {
					t.Fatalf("lost cancellation: %v", packErr)
				}

				if mode == "output-error" && !errors.Is(packErr, failure) {
					t.Fatalf("lost output error: %v", packErr)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("streaming workers did not stop")
			}
		})
	}
}

func TestZipStreamingReuse(t *testing.T) {
	// Compressible input can produce empty frames; the final partial frame and
	// compressor trailer must survive recycling the same queue for another entry.
	data := bytes.Repeat([]byte("repeated streaming data\n"), 400001)
	base := t.TempDir()
	inputs := []string{filepath.Join(base, "first"), filepath.Join(base, "second")}

	for _, filename := range inputs {
		err := os.WriteFile(filename, data, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	entries, _, err := collect(t.Context(), inputs, "")
	if err != nil {
		t.Fatal(err)
	}

	copying := copier{ctx: t.Context(), buffer: make([]byte, native.BufferSize)}
	options := Options{Level: 3, Threads: 1}

	var encoded bytes.Buffer

	err = packZip(&encoded, entries, options, &copying)
	if err != nil {
		t.Fatal(err)
	}

	archive, err := zip.NewReader(bytes.NewReader(encoded.Bytes()), int64(encoded.Len()))
	if err != nil {
		t.Fatal(err)
	}

	if len(archive.File) != len(inputs) {
		t.Fatalf("entry count: %d", len(archive.File))
	}

	for _, file := range archive.File {
		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}

		actual, readErr := io.ReadAll(reader)
		reader.Close()

		if readErr != nil || !bytes.Equal(actual, data) {
			t.Fatalf("recycled streaming payload changed: %v", readErr)
		}
	}
}

func archiveStreamTestData(size int) []byte {
	data := make([]byte, size)
	random := rand.NewPCG(12, 34)

	for offset := 0; offset < len(data); offset += 8 {
		binary.LittleEndian.PutUint64(data[offset:], random.Uint64())
	}

	return data
}
