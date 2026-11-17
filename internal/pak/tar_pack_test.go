package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestTarPackingBufferBoundaries(t *testing.T) {
	directory := t.TempDir()
	sizes := []int{0, 1, 511, 512, 513, native.BufferSize - 512, native.BufferSize - 1, native.BufferSize, native.BufferSize + 1, 3*native.BufferSize + 513}
	entries := make([]entry, 0, len(sizes)+2)
	modified := time.Unix(1700000000, 123456789)
	content := archiveStreamTestData(4 * native.BufferSize)

	var expected bytes.Buffer

	reference := tar.NewWriter(&expected)

	for index, size := range sizes {
		name := strconv.Itoa(index)

		if index == len(sizes)-1 {
			name = strings.Repeat("deep/", 40) + name
		}

		filename := filepath.Join(directory, strconv.Itoa(index))
		data := content[:size]

		err := os.WriteFile(filename, data, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		entries = append(entries, entry{path: filename, header: native.Header{Name: name, Size: int64(size), Mode: 0o640, Modified: modified}})
		header := tar.Header{Name: name, Size: int64(size), Mode: 0o640, ModTime: modified, Typeflag: tar.TypeReg}

		err = reference.WriteHeader(&header)
		if err != nil {
			t.Fatal(err)
		}

		_, err = reference.Write(data)
		if err != nil {
			t.Fatal(err)
		}
	}

	metadata := []tar.Header{
		{Name: "directory", Mode: 0o750, ModTime: modified, Typeflag: tar.TypeDir},
		{Name: "link", Mode: 0o777, ModTime: modified, Typeflag: tar.TypeSymlink, Linkname: entries[len(entries)-1].header.Name},
	}

	for index := range metadata {
		header := &metadata[index]
		kind := native.Directory

		if header.Typeflag == tar.TypeSymlink {
			kind = native.Symlink
		}

		entries = append(entries, entry{header: native.Header{Name: header.Name, Mode: uint32(header.Mode), Modified: modified, Kind: kind, Link: header.Linkname}})

		err := reference.WriteHeader(header)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := reference.Close()
	if err != nil {
		t.Fatal(err)
	}

	var actual bytes.Buffer

	copying := copier{ctx: t.Context()}

	err = packTar(&actual, entries, &copying)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(actual.Bytes(), expected.Bytes()) {
		t.Fatal("buffered TAR differs from archive/tar output")
	}
}
