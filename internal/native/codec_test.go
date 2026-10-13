package native

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCodecs(t *testing.T) {
	formats := []int{Gzip, Zstd, Bzip2, XZ, LZ4, Brotli, Deflate}
	sizes := []int{0, 1, BufferSize + 37}

	for _, format := range formats {
		for _, size := range sizes {
			t.Run(strconv.Itoa(format)+"/"+strconv.Itoa(size), func(t *testing.T) {
				data := testData(size)
				compressed := encode(t, format, data)

				reader, err := NewReader(bytes.NewReader(compressed), format, 2)
				if err != nil {
					t.Fatal(err)
				}

				defer reader.Close()

				var output bytes.Buffer

				_, err = io.CopyBuffer(&output, reader, make([]byte, 317))
				if err != nil {
					t.Fatal(err)
				}

				if !bytes.Equal(output.Bytes(), data) {
					t.Fatal("round-trip content mismatch")
				}

				verifyReference(t, format, compressed, data)

				truncated, err := NewReader(bytes.NewReader(compressed[:len(compressed)-1]), format, 2)
				if err != nil {
					t.Fatal(err)
				}

				defer truncated.Close()

				_, err = io.Copy(io.Discard, truncated)
				if err == nil {
					t.Fatal("truncated stream accepted")
				}
			})
		}
	}
}

func TestParallelReaders(t *testing.T) {
	formats := []int{Gzip, Bzip2}
	data := testData(BufferSize + 37)

	for _, format := range formats {
		t.Run(strconv.Itoa(format), func(t *testing.T) {
			compressed := encode(t, format, data)

			filename := filepath.Join(t.TempDir(), "données.bin")

			joined := append(bytes.Clone(compressed), compressed...)

			err := os.WriteFile(filename, joined, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			reader, err := OpenParallel(filename, format, 2)
			if err != nil {
				t.Fatal(err)
			}

			output, err := io.ReadAll(reader)

			reader.Close()

			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(output, bytes.Repeat(data, 2)) {
				t.Fatal("concatenated stream mismatch")
			}

			err = os.WriteFile(filename, compressed[:len(compressed)-8], 0o600)
			if err != nil {
				t.Fatal(err)
			}

			reader, err = OpenParallel(filename, format, 2)
			if err == nil {
				_, err = io.Copy(io.Discard, reader)

				reader.Close()
			}

			if err == nil {
				t.Fatal("truncated parallel stream accepted")
			}
		})
	}
}

func TestLibdeflate(t *testing.T) {
	sizes := []int{0, 1, BufferSize + 37}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := testData(size)

			compressed, err := CompressDeflate(data, 3)
			if err != nil {
				t.Fatal(err)
			}

			output := make([]byte, size)

			err = DecompressDeflate(compressed, output)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(output, data) {
				t.Fatal("libdeflate round-trip content mismatch")
			}
		})
	}
}

func TestGzipChecksumMismatch(t *testing.T) {
	compressed := encode(t, Gzip, testData(BufferSize+37))
	compressed[len(compressed)-8] ^= 1

	filename := filepath.Join(t.TempDir(), "corrupt.gz")

	err := os.WriteFile(filename, compressed, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := OpenParallel(filename, Gzip, 2)
	if err == nil {
		_, err = io.Copy(io.Discard, reader)

		reader.Close()
	}

	if err == nil {
		t.Fatal("gzip checksum mismatch accepted")
	}
}

func TestGzipWriter(t *testing.T) {
	levels := []int{1, 3, 6, 9}
	sizes := []int{0, 1, 3*BufferSize + 37}

	for _, level := range levels {
		for _, size := range sizes {
			t.Run(strconv.Itoa(level)+"/"+strconv.Itoa(size), func(t *testing.T) {
				data := testData(size)

				var output bytes.Buffer

				writer, err := NewGzipWriter(&output, level, 2)
				if err != nil {
					t.Fatal(err)
				}

				_, err = writer.Write(data)
				if err != nil {
					writer.Close()

					t.Fatal(err)
				}

				err = writer.Close()
				if err != nil {
					t.Fatal(err)
				}

				verifyReference(t, Gzip, output.Bytes(), data)
			})
		}
	}
}

func verifyReference(t *testing.T, format int, compressed, expected []byte) {
	t.Helper()

	var reader io.Reader

	switch format {
	case Gzip:
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}

		defer gzipReader.Close()

		reader = gzipReader
	case Bzip2:
		reader = bzip2.NewReader(bytes.NewReader(compressed))
	default:
		return
	}

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(output, expected) {
		t.Fatal("standard decoder content mismatch")
	}
}

func encode(t *testing.T, format int, data []byte) []byte {
	t.Helper()

	var compressed bytes.Buffer

	writer, err := NewWriter(&compressed, format, 3, 2)
	if err != nil {
		t.Fatal(err)
	}

	for offset := 0; offset < len(data); offset += 8191 {
		_, err = writer.Write(data[offset:min(offset+8191, len(data))])
		if err != nil {
			writer.Close()

			t.Fatal(err)
		}
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return compressed.Bytes()
}

func testData(size int) []byte {
	data := make([]byte, size)

	random := rand.NewChaCha8([32]byte{1})
	random.Read(data)

	clear(data[:size/2])

	return data
}
