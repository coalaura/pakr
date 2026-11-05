package pak

import (
	"archive/zip"
	"bytes"
	"context"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestZipPathConflicts(t *testing.T) {
	cases := [][]string{
		{"same", "same"},
		{"parent", "parent/child"},
		{"parent/child", "parent"},
		{"../outside"},
		{"dir/../../outside"},
		{"/absolute"},
		{"C:/drive"},
	}

	for _, names := range cases {
		t.Run(names[0], func(t *testing.T) {
			var encoded bytes.Buffer

			writer := zip.NewWriter(&encoded)

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

			root := t.TempDir()
			options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: root, Threads: 4, Level: 3}

			err = Unpack(t.Context(), "-", options, &encoded)
			if err == nil {
				t.Fatal("conflicting or escaping paths accepted")
			}

			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("preflight wrote entries: %v", err)
			}
		})
	}
}

func TestZipParallelEntries(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")

	err := os.Mkdir(source, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	for index := range 50 {
		data := bytes.Repeat([]byte(strconv.Itoa(index)), index*1031)

		err = os.WriteFile(filepath.Join(source, strconv.Itoa(index)), data, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	archive := filepath.Join(base, "archive.zip")
	options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: archive, Threads: 8, Level: 3}

	err = Pack(t.Context(), []string{source}, options, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	options.Output = filepath.Join(base, "output")

	err = Unpack(t.Context(), archive, options, nil)
	if err != nil {
		t.Fatal(err)
	}

	for index := range 50 {
		actual, readErr := os.ReadFile(filepath.Join(options.Output, "source", strconv.Itoa(index)))
		if readErr != nil {
			t.Fatal(readErr)
		}

		expected := bytes.Repeat([]byte(strconv.Itoa(index)), index*1031)
		if !bytes.Equal(actual, expected) {
			t.Fatalf("entry %d changed", index)
		}
	}

	err = Unpack(t.Context(), archive, options, nil)
	if err == nil {
		t.Fatal("parallel extraction overwrote existing files")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	options.Output = filepath.Join(base, "cancelled")

	err = Unpack(ctx, archive, options, nil)
	if err == nil {
		t.Fatal("cancelled extraction succeeded")
	}
}

func TestZipChecksumFailure(t *testing.T) {
	sizes := []int{1024, zipBufferLimit + 1}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := make([]byte, size)

			compressed, err := native.CompressDeflate(data, 3)
			if err != nil {
				t.Fatal(err)
			}

			var encoded bytes.Buffer

			writer := zip.NewWriter(&encoded)

			header := &zip.FileHeader{
				Name:               "bad",
				Method:             zip.Deflate,
				CRC32:              crc32.ChecksumIEEE(data) ^ 1,
				CompressedSize64:   uint64(len(compressed)),
				UncompressedSize64: uint64(size),
			}

			entry, err := writer.CreateRaw(header)
			if err != nil {
				t.Fatal(err)
			}

			_, err = entry.Write(compressed)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			root := t.TempDir()
			options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: root, Threads: 4, Level: 3}

			err = Unpack(t.Context(), "-", options, &encoded)
			if err == nil {
				t.Fatal("invalid CRC accepted")
			}

			_, err = os.Stat(filepath.Join(root, "bad"))
			if !os.IsNotExist(err) {
				t.Fatalf("corrupt output retained: %v", err)
			}
		})
	}
}

func TestZipSymlinkConflict(t *testing.T) {
	var encoded bytes.Buffer

	writer := zip.NewWriter(&encoded)

	header := &zip.FileHeader{Name: "link", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)

	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.WriteString(entry, "../outside")
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Create("link/child")
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: root, Threads: 4, Level: 3}

	err = Unpack(t.Context(), "-", options, &encoded)
	if err == nil {
		t.Fatal("symlink ancestor accepted")
	}

	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preflight wrote entries: %v", err)
	}
}
