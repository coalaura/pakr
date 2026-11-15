package pak

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestZipMixedBufferBoundaries(t *testing.T) {
	base := t.TempDir()
	data := archiveStreamTestData(zipBufferLimit + 8)
	sizes := []int{zipBufferLimit + 1, 17, zipBufferLimit - 1, zipBufferLimit, 0, zipBufferLimit + 1, 31, zipBufferLimit - 1, zipBufferLimit}
	inputs := make([]string, len(sizes))
	hashes := make([][sha256.Size]byte, len(sizes))

	for index, size := range sizes {
		inputs[index] = filepath.Join(base, strconv.Itoa(index))
		content := data[:size]

		if index%2 != 0 {
			content = bytes.Repeat([]byte{'a'}, size)
		}

		hashes[index] = sha256.Sum256(content)

		err := os.WriteFile(inputs[index], content, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	threadCounts := []int{1, 4, 16}

	for _, threads := range threadCounts {
		t.Run(strconv.Itoa(threads), func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "mixed.zip")
			options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: archivePath, Threads: threads, Level: 3}

			err := Pack(t.Context(), inputs, options, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			archive, err := zip.OpenReader(archivePath)
			if err != nil {
				t.Fatal(err)
			}

			defer archive.Close()

			if len(archive.File) != len(inputs) {
				t.Fatalf("entry count: %d", len(archive.File))
			}

			for index, file := range archive.File {
				if file.Name != filepath.Base(inputs[index]) || file.UncompressedSize64 != uint64(sizes[index]) {
					t.Fatalf("entry order or size changed: %+v", file.FileHeader)
				}

				reader, openErr := file.Open()
				if openErr != nil {
					t.Fatal(openErr)
				}

				hash := sha256.New()
				_, copyErr := io.Copy(hash, reader)
				reader.Close()

				if copyErr != nil || !bytes.Equal(hash.Sum(nil), hashes[index][:]) {
					t.Fatalf("independent decode of %s: %v", file.Name, copyErr)
				}
			}

			options.Output = t.TempDir()

			err = Unpack(t.Context(), archivePath, options, nil)
			if err != nil {
				t.Fatal(err)
			}

			files, err := os.ReadDir(options.Output)
			if err != nil || len(files) != len(inputs) {
				t.Fatalf("extracted inventory: %v, %v", files, err)
			}

			for index := range inputs {
				actual, readErr := os.ReadFile(filepath.Join(options.Output, filepath.Base(inputs[index])))
				if readErr != nil || len(actual) != sizes[index] || sha256.Sum256(actual) != hashes[index] {
					t.Fatalf("extracted entry %d changed: %v", index, readErr)
				}
			}
		})
	}
}

func TestZipMalformedBufferedSizes(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, zipBufferLimit-1)

	compressed, err := native.CompressDeflate(data, 3)
	if err != nil {
		t.Fatal(err)
	}

	modes := []string{"short-size", "long-size", "streaming-size", "truncated", "trailing-data"}

	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			payload := compressed
			size := len(data)

			switch mode {
			case "short-size":
				size--
			case "long-size":
				size++
			case "streaming-size":
				size = zipBufferLimit + 1
			case "truncated":
				payload = payload[:len(payload)-1]
			case "trailing-data":
				payload = append(bytes.Clone(payload), 0)
			}

			var encoded bytes.Buffer

			writer := zip.NewWriter(&encoded)
			header := &zip.FileHeader{
				Name:               "bad",
				Method:             zip.Deflate,
				CRC32:              crc32.ChecksumIEEE(data),
				CompressedSize64:   uint64(len(payload)),
				UncompressedSize64: uint64(size),
			}

			entry, createErr := writer.CreateRaw(header)
			if createErr != nil {
				t.Fatal(createErr)
			}

			_, writeErr := entry.Write(payload)
			if writeErr != nil {
				t.Fatal(writeErr)
			}

			closeErr := writer.Close()
			if closeErr != nil {
				t.Fatal(closeErr)
			}

			output := t.TempDir()
			options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: output, Threads: 4, Level: 3}

			unpackErr := Unpack(t.Context(), "-", options, &encoded)
			if unpackErr == nil {
				t.Fatal("malformed deflate entry accepted")
			}

			_, statErr := os.Stat(filepath.Join(output, "bad"))
			if !os.IsNotExist(statErr) {
				t.Fatalf("incomplete file retained: %v", statErr)
			}
		})
	}
}
