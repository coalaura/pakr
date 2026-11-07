package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type archiveHeaderCase struct {
	name   string
	format string
	header Header
}

type archiveFailureWriter struct {
	err    error
	cancel context.CancelFunc
}

func (writer archiveFailureWriter) Write(data []byte) (int, error) {
	if writer.cancel != nil {
		writer.cancel()

		return len(data), nil
	}

	return 0, writer.err
}

func TestArchiveWriterLimits(t *testing.T) {
	valid := Header{Name: "file", Mode: 0o644, Modified: time.Unix(1, 0)}

	cases := []archiveHeaderCase{
		{"newc size", "cpio", valid},
		{"newc date", "cpio", valid},
		{"newc trailer", "cpio", valid},
		{"ar directory", "ar", valid},
		{"ar path", "ar", valid},
		{"ar symlink", "ar", valid},
		{"ar size", "ar", valid},
		{"ar owner", "ar", valid},
		{"iso size", "iso", valid},
		{"iso depth", "iso", valid},
		{"iso date", "iso", valid},
		{"iso component", "iso", valid},
		{"invalid utf8", "cpio", valid},
		{"iso non-BMP", "iso", valid},
	}

	cases[0].header.Size = math.MaxUint32 + 1
	cases[1].header.Modified = time.Unix(-1, 0)
	cases[2].header.Name = "TRAILER!!!"
	cases[3].header.Kind = Directory
	cases[4].header.Name = "dir/file"
	cases[5].header.Kind = Symlink
	cases[6].header.Size = 10000000000
	cases[7].header.UID = 1000000
	cases[8].header.Size = 0xfffff800
	cases[9].header.Name = strings.Repeat("dir/", 8) + "file"
	cases[10].header.Modified = time.Date(2156, 1, 1, 0, 0, 0, 0, time.UTC)
	cases[11].header.Name = strings.Repeat("a", 256)
	cases[12].header.Name = "\xff"
	cases[13].header.Name = "\U0001f600"

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateArchiveHeader(test.header, test.format)
			if err == nil {
				t.Fatal("unrepresentable header accepted")
			}
		})
	}

	valid.Size = math.MaxUint32
	valid.Modified = time.Unix(math.MaxUint32, 0)

	err := validateArchiveHeader(valid, "cpio")
	if err != nil {
		t.Fatal(err)
	}

	valid.Size = 0xfffff7ff

	err = validateArchiveHeader(valid, "iso")
	if err != nil {
		t.Fatal(err)
	}
}

func TestArchiveWriterFailures(t *testing.T) {
	formats := []string{"cpio", "ar", "iso", "Z"}
	wanted := errors.New("destination failed")

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			writer, err := NewArchiveWriter(t.Context(), archiveFailureWriter{err: wanted}, format)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.WriteHeader(Header{Name: "file", Mode: 0o644, Modified: time.Unix(1, 0)})
			err = errors.Join(err, writer.Close())

			if !errors.Is(err, wanted) {
				t.Fatalf("lost output error: %v", err)
			}

			ctx, cancel := context.WithCancel(t.Context())

			writer, err = NewArchiveWriter(ctx, io.Discard, format)
			if err != nil {
				t.Fatal(err)
			}

			cancel()

			err = writer.Close()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}

			writer, err = NewArchiveWriter(t.Context(), archiveFailureWriter{}, format)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.WriteHeader(Header{Name: "file", Mode: 0o644, Modified: time.Unix(1, 0)})
			err = errors.Join(err, writer.Close())

			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("lost short write: %v", err)
			}
		})
	}
}

func TestCompressEmptyFraming(t *testing.T) {
	var output bytes.Buffer

	writer, err := NewArchiveWriter(t.Context(), &output, "Z")
	if err != nil {
		t.Fatal(err)
	}

	err = writer.WriteHeader(Header{Name: "empty", Modified: time.Unix(1, 0)})
	if err == nil {
		err = writer.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(output.Bytes(), []byte{0x1f, 0x9d, 0x90}) {
		t.Fatalf("empty compress stream has payload: %x", output.Bytes())
	}
}

func TestLZMAWriterResources(t *testing.T) {
	_, err := NewLZMAWriter(t.Context(), io.Discard, 9, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("memory budget: %v", err)
	}

	_, err = NewLZMAWriter(t.Context(), io.Discard, 0, 0)
	if err == nil {
		t.Fatal("invalid level accepted")
	}

	ctx, cancel := context.WithCancel(t.Context())

	writer, err := NewLZMAWriter(ctx, io.Discard, 1, 0)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	err = writer.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}

	wanted := errors.New("destination failed")

	writer, err = NewLZMAWriter(t.Context(), archiveFailureWriter{err: wanted}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if !errors.Is(err, wanted) {
		t.Fatalf("lost close error: %v", err)
	}
}

func TestISOStagingFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix TMPDIR staging behavior")
	}

	directory := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(directory, "missing"))

	writer, err := NewArchiveWriter(t.Context(), io.Discard, "iso")
	if err != nil {
		t.Fatal(err)
	}

	header := Header{Name: "file", Size: 4, Mode: 0o640, Modified: time.Unix(1, 0)}

	err = writer.WriteHeader(header)
	if err == nil {
		_, err = writer.Write([]byte("data"))
	}

	err = writer.Abort(err)
	if err == nil {
		t.Fatal("staging failure not propagated")
	}

	t.Setenv("TMPDIR", directory)

	aborts := []bool{false, true}

	for _, abort := range aborts {
		ctx, cancel := context.WithCancel(t.Context())
		target := archiveFailureWriter{cancel: cancel}

		writer, err = NewArchiveWriter(ctx, target, "iso")
		if err != nil {
			t.Fatal(err)
		}

		err = writer.WriteHeader(header)
		if err == nil {
			_, err = writer.Write([]byte("data"))
		}

		if err != nil {
			t.Fatal(writer.Abort(err))
		}

		if abort {
			err = writer.Abort(context.Canceled)
		} else {
			err = writer.Close()
		}

		cancel()

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation during image output: %v", err)
		}

		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatalf("staging files leaked: %v, %v", entries, err)
		}

		if runtime.GOOS == "linux" {
			descriptors, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}

			for _, descriptor := range descriptors {
				name, _ := os.Readlink(filepath.Join("/proc/self/fd", descriptor.Name()))
				if strings.HasPrefix(name, directory+"/") {
					t.Fatalf("staging descriptor leaked: %s", name)
				}
			}
		}
	}
}
