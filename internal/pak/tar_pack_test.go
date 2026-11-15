package pak

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

type tarMutationWriter struct {
	mutate func()
}

func (writer *tarMutationWriter) Write(data []byte) (int, error) {
	if writer.mutate != nil {
		writer.mutate()
		writer.mutate = nil
	}

	return len(data), nil
}

func TestTarPackingInputChanges(t *testing.T) {
	changes := []string{"grow", "shrink", "cancel"}

	for _, change := range changes {
		t.Run(change, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "input")
			data := archiveStreamTestData(3 * native.BufferSize)

			err := os.WriteFile(filename, data, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			entries, _, err := collect(t.Context(), []string{filename}, "")
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			writer := tarMutationWriter{mutate: func() {
				switch change {
				case "grow":
					err = os.Truncate(filename, int64(len(data)+1))
				case "shrink":
					err = os.Truncate(filename, 0)
				case "cancel":
					cancel()
				}

				if err != nil {
					t.Fatal(err)
				}
			}}

			copying := copier{ctx: ctx, buffer: make([]byte, native.BufferSize)}

			err = packTar(&writer, entries, &copying)
			if err == nil {
				t.Fatal("input changed during packing without an error")
			}

			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error lost: %v", err)
			}
		})
	}
}

func TestTarPackingOutputFailures(t *testing.T) {
	formats := []string{"tar", "tar.gz", "tar.zst", "tar.xz"}
	sizes := []int{0, 3 * native.BufferSize}
	failure := errors.New("output failed")

	for _, name := range formats {
		for _, size := range sizes {
			t.Run(name+"/"+strconv.Itoa(size), func(t *testing.T) {
				filename := filepath.Join(t.TempDir(), "input")

				err := os.WriteFile(filename, archiveStreamTestData(size), 0o600)
				if err != nil {
					t.Fatal(err)
				}

				format, err := ParseFormat(name)
				if err != nil {
					t.Fatal(err)
				}

				options := Options{Format: format, Output: "-", Level: 3, Threads: 4, Memory: 1 << 30}

				err = Pack(t.Context(), []string{filename}, options, zipInterruptWriter{err: failure})
				if !errors.Is(err, failure) {
					t.Fatalf("output or finalization error lost: %v", err)
				}

				err = Pack(t.Context(), []string{filename}, options, zipShortWriter{})
				if !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("short write lost: %v", err)
				}
			})
		}
	}
}
