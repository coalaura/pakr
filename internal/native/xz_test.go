package native

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"hash/crc64"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
)

type xzFailWriter struct {
	remaining int
	err       error
}

func (writer *xzFailWriter) Write(data []byte) (int, error) {
	count := min(len(data), writer.remaining)
	writer.remaining -= count

	if count != len(data) {
		return count, writer.err
	}

	return count, nil
}

func TestXZLevels(t *testing.T) {
	dictionaries := []int{256 << 10, 1 << 20, 4 << 20, 4 << 20, 8 << 20, 8 << 20, 16 << 20, 32 << 20, 64 << 20}
	presets := []int{0, 1, 3, 4, 5, 6, 7, 8, 9}
	data := testData(4097)

	for level := 1; level <= 9; level++ {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			encoded, settings := encodeXZ(t, data, level, 1, false, 7)
			backend := "Fast LZMA2"

			if level == 1 {
				backend = "liblzma"
			}

			if settings.Backend != backend || settings.Preset != presets[level-1] || settings.Dictionary != dictionaries[level-1] || settings.Workers != 1 {
				t.Fatalf("incorrect level mapping: %+v", settings)
			}

			actual, err := decodeXZ(encoded)
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("round trip: %v", err)
			}

			if level > 1 {
				checkXZFraming(t, encoded, data, settings.Dictionary)
			}
		})
	}
}

func TestXZStreaming(t *testing.T) {
	var output bytes.Buffer

	writer, err := NewXZWriter(t.Context(), &output, 2, 2, false, 128<<20)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	settings := writer.XZSettings()
	if settings.Workers != 2 || settings.Memory > 128<<20 {
		t.Fatalf("incorrect admission: %+v", settings)
	}

	block := xzRandom(256 << 10)
	expected := sha256.New()

	for range 24 {
		_, err = writer.Write(block)
		if err != nil {
			t.Fatal(err)
		}

		expected.Write(block)
	}

	if output.Len() <= 24 {
		t.Fatal("no payload emitted before Close across six dictionary windows")
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	actual, err := decodeXZ(output.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(actual)
	if len(actual) != 24*len(block) || !bytes.Equal(digest[:], expected.Sum(nil)) {
		t.Fatal("streamed payload mismatch")
	}
}

func TestXZIntegrity(t *testing.T) {
	data := []byte("streaming XZ integrity\n")
	encoded, settings := encodeXZ(t, data, 3, 1, false, 1)
	checkXZFraming(t, encoded, data, settings.Dictionary)

	for length := range len(encoded) {
		_, err := decodeXZ(encoded[:length])
		if err == nil {
			t.Fatalf("accepted truncation at %d", length)
		}
	}

	indexSize := (int(binary.LittleEndian.Uint32(encoded[len(encoded)-8:])) + 1) * 4
	indexStart := len(encoded) - 12 - indexSize
	offsets := []int{0, 8, 13, 20, 25, indexStart - 1, indexStart, len(encoded) - 1}

	for _, offset := range offsets {
		corrupt := bytes.Clone(encoded)
		corrupt[offset] ^= 1

		_, err := decodeXZ(corrupt)
		if err == nil {
			t.Fatalf("accepted corruption at %d", offset)
		}
	}

	empty, _ := encodeXZ(t, nil, 3, 1, false, 1)
	joined := append(bytes.Clone(empty), encoded...)
	joined = append(joined, 0, 0, 0, 0)
	joined = append(joined, encoded...)

	actual, err := decodeXZ(joined)
	if err != nil || !bytes.Equal(actual, bytes.Repeat(data, 2)) {
		t.Fatalf("concatenated streams: %v", err)
	}
}

func TestXZMemoryAndLegacy(t *testing.T) {
	writer, err := NewXZWriter(t.Context(), io.Discard, 2, 1, false, 128<<20)
	if err != nil {
		t.Fatal(err)
	}

	settings := writer.XZSettings()

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	writer, err = NewXZWriter(t.Context(), io.Discard, 2, 1, false, settings.Memory-1)
	if err == nil {
		writer.Close()
		t.Fatal("memory preflight silently changed effort or workers")
	}

	if !strings.Contains(err.Error(), "memory budget") {
		t.Fatal(err)
	}

	writer, err = NewXZWriter(t.Context(), io.Discard, 2, 2, false, settings.Memory)
	if err == nil {
		writer.Close()
		t.Fatal("worker admission silently reduced worker count")
	}

	levels := []int{3, 9}

	for _, level := range levels {
		data := []byte("liblzma extreme and max retain their presets")

		encoded, actual := encodeXZ(t, data, level, 1, true, 3)
		if actual.Backend != "liblzma" || actual.Preset != NativeLevel(XZ, level) {
			t.Fatalf("legacy path changed: %+v", actual)
		}

		decoded, err := decodeXZ(encoded)
		if err != nil || !bytes.Equal(decoded, data) {
			t.Fatalf("legacy decode: %v", err)
		}
	}
}

func TestXZOutputFailures(t *testing.T) {
	data := testData(4097)
	encoded, _ := encodeXZ(t, data, 3, 1, false, 317)
	positions := []int{0, 24, len(encoded) - 12}
	failure := errors.New("output failed")
	errorsToReturn := []error{nil, failure}

	for _, position := range positions {
		for _, outputError := range errorsToReturn {
			target := &xzFailWriter{remaining: position, err: outputError}

			writer, err := NewXZWriter(t.Context(), target, 3, 1, false, 128<<20)
			if err != nil {
				t.Fatal(err)
			}

			_, writeError := writer.Write(data)
			closeError := writer.Close()

			expected := outputError
			if expected == nil {
				expected = io.ErrShortWrite
			}

			if writeError != nil && !errors.Is(writeError, expected) {
				t.Fatalf("write: %v", writeError)
			}

			if !errors.Is(closeError, expected) || writer.codec != nil {
				t.Fatalf("close/cleanup: %v", closeError)
			}

			if !errors.Is(writer.Close(), expected) {
				t.Fatal("lost sticky error")
			}
		}
	}
}

func TestXZCancellation(t *testing.T) {
	data := xzRandom(7 << 20)
	phases := []string{"write", "close"}

	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			writer, err := NewXZWriter(ctx, io.Discard, 6, 1, false, 256<<20)
			if err != nil {
				t.Fatal(err)
			}

			defer writer.Close()

			if phase == "close" {
				_, err = writer.Write(data)
				if err != nil {
					t.Fatal(err)
				}
			}

			cause := errors.New("cancel native XZ work")

			timer := time.AfterFunc(10*time.Millisecond, func() {
				cancel(cause)
			})

			defer timer.Stop()

			started := time.Now()

			if phase == "write" {
				// The second write crosses the dictionary boundary and starts native work.
				_, err = writer.Write(data)
				if err == nil {
					_, err = writer.Write(data)
				}
			} else {
				err = writer.Close()
			}

			if !errors.Is(err, cause) {
				t.Fatalf("%s cancellation: %v", phase, err)
			}

			err = writer.Close()
			if !errors.Is(err, cause) || writer.codec != nil || time.Since(started) > 5*time.Second {
				t.Fatalf("cancellation cleanup: %v", err)
			}
		})
	}
}

func encodeXZ(t *testing.T, data []byte, level, workers int, extreme bool, chunk int) ([]byte, XZSettings) {
	t.Helper()

	var output bytes.Buffer

	writer, err := NewXZWriter(t.Context(), &output, level, workers, extreme, 2<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	settings := writer.XZSettings()

	// Force partial native header, payload and trailer writes as well as input chunks.
	writer.buffer = writer.buffer[:min(chunk, len(writer.buffer))]

	for len(data) != 0 {
		count := min(chunk, len(data))

		_, err = writer.Write(data[:count])
		if err != nil {
			t.Fatal(err)
		}

		data = data[count:]
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return output.Bytes(), settings
}

func decodeXZ(encoded []byte) ([]byte, error) {
	reader, err := NewReader(bytes.NewReader(encoded), XZ, 1)
	if err != nil {
		return nil, err
	}

	defer reader.Close()

	return io.ReadAll(reader)
}

func xzRandom(size int) []byte {
	data := make([]byte, size)
	random := rand.New(rand.NewPCG(1729, 42))

	for index := range data {
		data[index] = byte(random.Uint32())
	}

	return data
}

func checkXZFraming(t *testing.T, encoded, data []byte, dictionary int) {
	t.Helper()

	if len(encoded) < 56 || string(encoded[:6]) != "\xfd7zXZ\x00" || encoded[6] != 0 || encoded[7] != 4 || string(encoded[len(encoded)-2:]) != "YZ" {
		t.Fatal("invalid XZ stream framing or check ID")
	}

	footer := encoded[len(encoded)-12:]
	if crc32.ChecksumIEEE(encoded[6:8]) != binary.LittleEndian.Uint32(encoded[8:12]) || crc32.ChecksumIEEE(footer[4:10]) != binary.LittleEndian.Uint32(footer) || !bytes.Equal(footer[8:10], encoded[6:8]) {
		t.Fatal("invalid stream header/footer checksums or flags")
	}

	blockSize := (int(encoded[12]) + 1) * 4
	block := encoded[12 : 12+blockSize]

	if crc32.ChecksumIEEE(block[:blockSize-4]) != binary.LittleEndian.Uint32(block[blockSize-4:]) || block[1] != 0 || block[2] != 0x21 || block[3] != 1 {
		t.Fatal("invalid single-filter streaming block header")
	}

	property := uint32(block[4])

	actualDictionary := (uint32(2) | (property & 1)) << (property/2 + 11)
	if actualDictionary != uint32(dictionary) {
		t.Fatalf("dictionary property: %d, want %d", actualDictionary, dictionary)
	}

	indexSize := (int(binary.LittleEndian.Uint32(footer[4:])) + 1) * 4
	indexStart := len(encoded) - 12 - indexSize

	index := encoded[indexStart : len(encoded)-12]
	if index[0] != 0 || index[1] != 1 || crc32.ChecksumIEEE(index[:len(index)-4]) != binary.LittleEndian.Uint32(index[len(index)-4:]) {
		t.Fatal("invalid Index or record count")
	}

	unpadded, count := binary.Uvarint(index[2:])
	if count <= 0 {
		t.Fatal("invalid unpadded size")
	}

	uncompressed, count := binary.Uvarint(index[2+count:])
	if count <= 0 || uncompressed != uint64(len(data)) || (unpadded+3)&^3 != uint64(indexStart-12) {
		t.Fatal("invalid Index sizes")
	}

	compressed := int(unpadded) - blockSize - 8

	end := 12 + blockSize + compressed
	if encoded[end-1] != 0 {
		t.Fatal("missing LZMA2 end marker or unexpected extra trailer")
	}

	for _, value := range encoded[end : indexStart-8] {
		if value != 0 {
			t.Fatal("nonzero block padding")
		}
	}

	checksum := crc64.Checksum(data, crc64.MakeTable(crc64.ECMA))
	if binary.LittleEndian.Uint64(encoded[indexStart-8:indexStart]) != checksum {
		t.Fatal("invalid CRC64")
	}
}
