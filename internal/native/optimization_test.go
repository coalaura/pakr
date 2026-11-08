package native

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
	"testing"
)

type levelCase struct {
	backend int
	values  [9]int
}

func TestSeekableScheduling(t *testing.T) {
	sizes := []int{1024, 66 << 20}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			var compressed bytes.Buffer

			writer, err := NewSeekableWriter(&compressed, 3, 4)
			if err != nil {
				t.Fatal(err)
			}

			writer.frameSize = size
			data := make([]byte, size)

			_, err = writer.Write(data)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			reader, err := OpenSeekable(bytes.NewReader(compressed.Bytes()), int64(compressed.Len()), 8)
			if err != nil {
				t.Fatal(err)
			}

			defer reader.Close()

			if len(reader.workers) != 1 || reader.buffered != (size <= seekBufferLimit) {
				t.Fatal("incorrect sequential/fallback selection")
			}

			if reader.buffered && reader.workers[0].jobs != nil {
				t.Fatal("single-frame reader started a worker")
			}

			actual, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("decoded frame: %v", err)
			}
		})
	}
}

func TestNativeLevels(t *testing.T) {
	cases := []levelCase{
		{Gzip, [9]int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{Zstd, [9]int{-3, 1, 3, 6, 9, 12, 16, 19, 22}},
		{Bzip2, [9]int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{XZ, [9]int{0, 1, 3, 4, 5, 6, 7, 8, 9}},
		{LZ4, [9]int{-4, -1, 0, 3, 5, 7, 9, 10, 12}},
		{Brotli, [9]int{0, 1, 3, 4, 5, 6, 8, 10, 11}},
		{Deflate, [9]int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{Libdeflate, [9]int{1, 2, 3, 4, 5, 7, 9, 10, 12}},
		{SevenZip, [9]int{0, 1, 3, 4, 5, 6, 7, 8, 9}},
		{ISAL, [9]int{0, 1, 2, 3, -1, -1, -1, -1, -1}},
	}

	for _, test := range cases {
		for index, expected := range test.values {
			actual := NativeLevel(test.backend, index+1)
			if actual != expected {
				t.Fatalf("backend %d level %d: got %d, want %d", test.backend, index+1, actual, expected)
			}
		}
	}
}

func TestBzip2Members(t *testing.T) {
	data := testData(3*BufferSize + 37)

	var compressed bytes.Buffer

	writer, err := NewBzip2Writer(&compressed, 3, 4)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write(data)
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	verifyReference(t, Bzip2, compressed.Bytes(), data)

	corrupt := bytes.Clone(compressed.Bytes())

	// The first member's block CRC follows its six-byte block magic.
	corrupt[10] ^= 1

	reader, err := NewReader(bytes.NewReader(corrupt), Bzip2, 1)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	_, err = io.Copy(io.Discard, reader)
	if err == nil {
		t.Fatal("corrupt bzip2 member accepted")
	}
}

func TestNativeParameters(t *testing.T) {
	formats := []int{Zstd, XZ, LZ4, Brotli, Deflate}

	for _, format := range formats {
		var (
			writer *Writer
			err    error
		)

		if format == XZ {
			// Test requested settings independently of available memory on the runner.
			writer, err = NewXZWriter(t.Context(), io.Discard, 9, 4, true, 8<<30)
		} else {
			writer, err = NewWriterExtreme(io.Discard, format, 9, 4, true)
		}

		if err != nil {
			t.Fatal(err)
		}

		level, threads, extreme := writer.parameters()
		if level != NativeLevel(format, 9) || extreme != (format == XZ) {
			t.Fatalf("codec %d: level=%d threads=%d extreme=%t", format, level, threads, extreme)
		}

		if format == XZ && threads != 4 {
			t.Fatalf("xz silently reduced workers: %d", threads)
		}

		err = writer.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	writer, err := NewWriter(io.Discard, Zstd, 3, 4)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	_, threads, _ := writer.parameters()
	if threads != 4 {
		t.Fatalf("zstd multithreading unavailable: workers=%d", threads)
	}
}

func TestLZ4Workers(t *testing.T) {
	levels := []int{1, 3, 9}
	data := testData(17*BufferSize + 37)

	for _, level := range levels {
		var compressed bytes.Buffer

		writer, err := NewWriter(&compressed, LZ4, level, 4)
		if err != nil {
			t.Fatal(err)
		}

		_, err = writer.Write(data)
		if err != nil {
			t.Fatal(err)
		}

		_, threads, _ := writer.parameters()
		if threads != 4 {
			t.Fatalf("lz4 multithreading unavailable: workers=%d", threads)
		}

		err = writer.Close()
		if err != nil {
			t.Fatal(err)
		}

		reader, err := NewReader(bytes.NewReader(compressed.Bytes()), LZ4, 1)
		if err != nil {
			t.Fatal(err)
		}

		decoded, err := io.ReadAll(reader)

		reader.Close()

		if err != nil || !bytes.Equal(decoded, data) {
			t.Fatalf("lz4 parallel round trip: %v", err)
		}
	}
}

func TestSeekable(t *testing.T) {
	sizes := []int{0, 1, 2*seekFrameSize + 37}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := testData(size)
			compressed := encodeSeekable(t, data)

			ordinary, err := NewReader(bytes.NewReader(compressed), Zstd, 1)
			if err != nil {
				t.Fatal(err)
			}

			defer ordinary.Close()

			decoded, err := io.ReadAll(ordinary)
			if err != nil || !bytes.Equal(decoded, data) {
				t.Fatalf("ordinary decoder: %v, equal=%v", err, bytes.Equal(decoded, data))
			}

			reader, err := OpenSeekable(bytes.NewReader(compressed), int64(len(compressed)), 4)
			if err != nil || reader == nil {
				t.Fatalf("open seekable: %v", err)
			}

			defer reader.Close()

			decoded, err = io.ReadAll(reader)
			if err != nil || !bytes.Equal(decoded, data) {
				t.Fatalf("seekable decoder: %v, equal=%v", err, bytes.Equal(decoded, data))
			}

			early, err := OpenSeekable(bytes.NewReader(compressed), int64(len(compressed)), 4)
			if err != nil {
				t.Fatal(err)
			}

			early.Close()
		})
	}
}

func TestSeekableCorruption(t *testing.T) {
	compressed := encodeSeekable(t, testData(4096))
	positions := []int{len(compressed) - 25, len(compressed) - 17, len(compressed) - 13, len(compressed) - 5, 12}

	for _, position := range positions {
		t.Run(strconv.Itoa(position), func(t *testing.T) {
			corrupt := bytes.Clone(compressed)
			corrupt[position] ^= 4

			reader, err := OpenSeekable(bytes.NewReader(corrupt), int64(len(corrupt)), 4)
			if err == nil && reader != nil {
				_, err = io.Copy(io.Discard, reader)
				reader.Close()
			}

			if err == nil {
				t.Fatal("corrupt seekable stream accepted")
			}
		})
	}
}

func TestSeekableChecksumsAndLargeFrame(t *testing.T) {
	data := testData(seekBufferLimit + 1)
	compressed := encode(t, Zstd, data)

	var table [29]byte

	binary.LittleEndian.PutUint32(table[:4], seekTableMagic)
	binary.LittleEndian.PutUint32(table[4:8], 21)
	binary.LittleEndian.PutUint32(table[8:12], uint32(len(compressed)))
	binary.LittleEndian.PutUint32(table[12:16], uint32(len(data)))

	// An intentionally incorrect table checksum must fail even if the frame CRC is valid.
	binary.LittleEndian.PutUint32(table[20:24], 1)
	table[24] = 0x80
	binary.LittleEndian.PutUint32(table[25:], seekableMagic)

	compressed = append(compressed, table[:]...)

	reader, err := OpenSeekable(bytes.NewReader(compressed), int64(len(compressed)), 4)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	_, err = io.Copy(io.Discard, reader)
	if err == nil {
		t.Fatal("invalid seek table checksum accepted")
	}
}

func TestDeflateTrailingData(t *testing.T) {
	compressed, err := CompressDeflate([]byte("payload"), 9)
	if err != nil {
		t.Fatal(err)
	}

	compressed = append(compressed, 0)

	err = DecompressDeflate(compressed, make([]byte, 7))
	if err == nil {
		t.Fatal("trailing deflate data accepted")
	}
}

func TestStreamTruncationAndConcatenation(t *testing.T) {
	formats := []int{Gzip, Zstd, Bzip2, XZ, LZ4, Brotli}
	data := testData(4096)

	for _, format := range formats {
		t.Run(strconv.Itoa(format), func(t *testing.T) {
			compressed := encode(t, format, data)

			reader, err := NewReader(bytes.NewReader(compressed[:len(compressed)-1]), format, 2)
			if err != nil {
				t.Fatal(err)
			}

			_, err = io.Copy(io.Discard, reader)
			reader.Close()

			if err == nil {
				t.Fatal("truncated stream accepted")
			}

			joined := append(bytes.Clone(compressed), compressed...)

			reader, err = NewReader(bytes.NewReader(joined), format, 2)
			if err != nil {
				t.Fatal(err)
			}

			decoded, err := io.ReadAll(reader)

			reader.Close()

			if format == Brotli {
				if err == nil {
					t.Fatal("trailing brotli stream accepted")
				}
			} else if err != nil || !bytes.Equal(decoded, bytes.Repeat(data, 2)) {
				t.Fatalf("concatenated streams: %v", err)
			}
		})
	}
}

func TestSeekableEmptyChecksum(t *testing.T) {
	compressed := encode(t, Zstd, nil)

	var table [29]byte

	binary.LittleEndian.PutUint32(table[:4], seekTableMagic)
	binary.LittleEndian.PutUint32(table[4:8], 21)
	binary.LittleEndian.PutUint32(table[8:12], uint32(len(compressed)))

	// Low 32 bits of the standard XXH64 empty-input vector (seed zero).
	binary.LittleEndian.PutUint32(table[16:20], 0x51d8e999)
	binary.LittleEndian.PutUint32(table[20:24], 1)
	table[24] = 0x80
	binary.LittleEndian.PutUint32(table[25:], seekableMagic)

	compressed = append(compressed, table[:]...)

	reader, err := OpenSeekable(bytes.NewReader(compressed), int64(len(compressed)), 4)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	count, err := io.Copy(io.Discard, reader)
	if err != nil || count != 0 {
		t.Fatalf("valid empty checksummed frame: %d, %v", count, err)
	}
}

func encodeSeekable(t *testing.T, data []byte) []byte {
	t.Helper()

	var compressed bytes.Buffer

	writer, err := NewSeekableWriter(&compressed, 3, 2)
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

	return compressed.Bytes()
}
