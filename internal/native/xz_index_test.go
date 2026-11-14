package native

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"
)

type xzProbeReader struct {
	source *bytes.Reader
	read   int
}

func (reader *xzProbeReader) ReadAt(data []byte, offset int64) (int, error) {
	reader.read += len(data)

	return reader.source.ReadAt(data, offset)
}

func TestXZDecoderThreads(t *testing.T) {
	encoded, _ := encodeXZ(t, testData(3<<20+1), 2, 1, false, 65537)
	counts := []int{1, 2, 4, 16}

	for _, threads := range counts {
		reader := bytes.NewReader(encoded)

		_, err := reader.Seek(7, io.SeekStart)
		if err != nil {
			t.Fatal(err)
		}

		actual := XZDecoderThreads(reader, int64(len(encoded)), threads)
		if actual != min(threads, 2) {
			t.Fatalf("budget %d: got %d", threads, actual)
		}

		position, err := reader.Seek(0, io.SeekCurrent)
		if err != nil || position != 7 {
			t.Fatalf("probe changed input position: %d, %v", position, err)
		}
	}

	empty, _ := encodeXZ(t, nil, 2, 1, false, 1)

	actual := XZDecoderThreads(bytes.NewReader(empty), int64(len(empty)), 4)
	if actual != 1 {
		t.Fatalf("empty stream: %d", actual)
	}

	variants := [][]byte{append(bytes.Clone(encoded), empty...), append(bytes.Clone(encoded), 0, 0, 0, 0)}
	indexSize := (int(binary.LittleEndian.Uint32(encoded[len(encoded)-8:])) + 1) * 4
	offsets := []int{0, 8, len(encoded) - 12 - indexSize, len(encoded) - 12, len(encoded) - 1}

	for _, offset := range offsets {
		corrupt := bytes.Clone(encoded)
		corrupt[offset] ^= 1
		variants = append(variants, corrupt)
	}

	oversized := bytes.Clone(encoded)

	footer := oversized[len(oversized)-12:]
	binary.LittleEndian.PutUint32(footer[4:8], 0xffffffff)
	binary.LittleEndian.PutUint32(footer[:4], crc32.ChecksumIEEE(footer[4:10]))

	variants = append(variants, oversized)

	for _, variant := range variants {
		reader := &xzProbeReader{source: bytes.NewReader(variant)}
		actual = XZDecoderThreads(reader, int64(len(variant)), 4)

		if actual != 4 || reader.read > xzIndexProbeLimit+24 {
			t.Fatalf("unsafe fallback: threads=%d, read=%d", actual, reader.read)
		}
	}

	for length := range len(empty) {
		actual = XZDecoderThreads(bytes.NewReader(empty[:length]), int64(length), 4)
		if actual != 4 {
			t.Fatalf("truncated stream at %d: %d", length, actual)
		}
	}
}

func FuzzXZDecoderThreads(f *testing.F) {
	var output bytes.Buffer

	writer, err := NewWriter(&output, XZ, 1, 1)
	if err != nil {
		f.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(output.Bytes())
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		reader := &xzProbeReader{source: bytes.NewReader(data)}
		threads := XZDecoderThreads(reader, int64(len(data)), 16)

		if threads < 1 || threads > 16 || reader.read > xzIndexProbeLimit+24 {
			t.Fatalf("unbounded probe: threads=%d, read=%d", threads, reader.read)
		}
	})
}
