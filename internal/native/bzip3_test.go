package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

type bzip3Fixture struct {
	encoded string
	plain   string
}

type bzip3ShortWriter struct{}

type bzip3FaultReader struct {
	err error
}

// Generated independently with the official 1.5.5 CLI: bzip3 -c -b 1 -j 1.
var bzip3Fixtures = []bzip3Fixture{
	{encoded: "425a33763100001000", plain: ""},
	{encoded: "425a337631000010001f0000001700000033727352ffffffff6f6666696369616c20627a69703320666978747572650a", plain: "official bzip3 fixture\n"},
	{encoded: "425a3376310000100062000000800c0000c16b04d2150000000277000000f50195e0ddcd81b589d70363178da8159c0c38b757414ea30eb9e1d5561a2910703a7747f3e3ee76a917ed8657577ed51f127cf00779c96df0eed01ce49d4b1c65fd39423fbde85c53952dc9a739767390d2d731c3", plain: strings.Repeat("BZIP3 interoperability fixture.\n", 100)},
}

func (bzip3ShortWriter) Write(data []byte) (int, error) {
	return len(data) / 2, nil
}

func (reader bzip3FaultReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func TestBzip3Fixtures(t *testing.T) {
	for index := range bzip3Fixtures {
		fixture := &bzip3Fixtures[index]

		compressed, err := hex.DecodeString(fixture.encoded)
		if err != nil {
			t.Fatal(err)
		}

		actual := decodeBzip3(t, compressed, 3)
		if string(actual) != fixture.plain {
			t.Fatalf("official fixture %d mismatch", index)
		}
	}
}

func TestBzip3Streams(t *testing.T) {
	sizes := []int{0, 1, 63, 64, bzip3Minimum, bzip3Minimum + 1, 1 << 20, 3<<20 + 37}
	workers := []int{1, 4}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := testData(size)

			var reference []byte

			for _, threads := range workers {
				compressed := encodeBzip3(t, data, 1, threads, 317)
				if reference != nil && !bytes.Equal(reference, compressed) {
					t.Fatal("output depends on scheduling")
				}

				reference = compressed

				actual := decodeBzip3(t, compressed, threads)
				if !bytes.Equal(data, actual) {
					t.Fatal("round-trip content mismatch")
				}
			}

			whole := encodeBzip3(t, data, 1, 4, max(1, size))
			if !bytes.Equal(reference, whole) {
				t.Fatal("output depends on Write boundaries")
			}
		})
	}
}

func TestBzip3Levels(t *testing.T) {
	for level := 1; level <= 9; level++ {
		compressed := encodeBzip3(t, []byte("tiny"), level, 4, 1)

		declared := binary.LittleEndian.Uint32(compressed[5:])
		if declared != bzip3Minimum {
			t.Fatalf("level %d: tiny block size %d", level, declared)
		}

		actual := decodeBzip3(t, compressed, 4)
		if string(actual) != "tiny" {
			t.Fatal("tiny content mismatch")
		}

		writer, err := NewBzip3Writer(t.Context(), io.Discard, level, 256, 8<<30)
		if err != nil {
			t.Fatal(err)
		}

		if writer.blocks.block != bzip3Sizes[level-1] {
			t.Fatalf("level %d block size", level)
		}

		t.Logf("level %d: block=%d workers within 8GiB=%d", level, writer.blocks.block, len(writer.blocks.slots))

		err = writer.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestBzip3MemoryBudget(t *testing.T) {
	_, err := NewBzip3Writer(t.Context(), io.Discard, 9, 256, 0)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected memory rejection: %v", err)
	}

	_, err = NewBzip3Writer(t.Context(), io.Discard, 1, 1, 1)
	if err == nil {
		t.Fatal("accepted insufficient budget")
	}

	var header [9]byte

	copy(header[:], "BZ3v1")
	binary.LittleEndian.PutUint32(header[5:], bzip3Maximum)

	_, err = NewBzip3Reader(t.Context(), bytes.NewReader(header[:]), 1, 0)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected decoder memory rejection: %v", err)
	}

	writer, err := NewBzip3Writer(t.Context(), io.Discard, 3, 256, 256<<20)
	if err != nil {
		t.Fatal(err)
	}

	if len(writer.blocks.slots) != 1 {
		t.Fatalf("memory did not cap workers: %d", len(writer.blocks.slots))
	}

	writer.Close()
}

func TestBzip3Malformed(t *testing.T) {
	compressed, err := hex.DecodeString(bzip3Fixtures[2].encoded)
	if err != nil {
		t.Fatal(err)
	}

	for length := range len(compressed) {
		if length == 9 {
			// A header alone is a valid empty stream; there is no end marker.
			continue
		}

		rejectBzip3(t, compressed[:length])
	}

	positions := []int{0, 4, 5, 8, 9, 12, 13, 16, 17, 21, 25, 29, len(compressed) - 1}

	for _, position := range positions {
		corrupt := bytes.Clone(compressed)
		corrupt[position] ^= 0xff

		if position == 5 {
			binary.LittleEndian.PutUint32(corrupt[5:], bzip3Minimum-1)
		}

		rejectBzip3(t, corrupt)
	}

	corrupt := append(bytes.Clone(compressed), 0)
	rejectBzip3(t, corrupt)

	lengths := []uint32{0, 1, 7, 0x7fffffff, 0xffffffff}

	for _, length := range lengths {
		corrupt = bytes.Clone(compressed)

		binary.LittleEndian.PutUint32(corrupt[9:], length)
		rejectBzip3(t, corrupt)
	}

	// Low-level validation must not read stale bytes in reused worker buffers.
	corrupt = bytes.Clone(compressed)

	binary.LittleEndian.PutUint32(corrupt[9:], 8)
	corrupt = corrupt[:25]

	rejectBzip3(t, corrupt)
}

func TestBzip3FailureCleanup(t *testing.T) {
	writer, err := NewBzip3Writer(t.Context(), bzip3ShortWriter{}, 1, 4, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write([]byte("short"))
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if !errors.Is(err, io.ErrShortWrite) || !errors.Is(writer.Close(), io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	writer, err = NewBzip3Writer(ctx, io.Discard, 1, 4, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write(testData(3 << 20))
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	err = writer.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel writer: %v", err)
	}

	for index := range writer.blocks.slots {
		if writer.blocks.slots[index].state != nil {
			t.Fatal("native state leaked")
		}
	}

	compressed := encodeBzip3(t, testData(3<<20+37), 1, 4, 1<<20)

	ctx, cancel = context.WithCancel(t.Context())

	reader, err := NewBzip3Reader(ctx, bytes.NewReader(compressed), 4, 0)
	if err != nil {
		t.Fatal(err)
	}

	var single [1]byte

	_, err = reader.Read(single[:])
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	_, err = reader.Read(single[:])
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel reader: %v", err)
	}

	reader.Close()

	fault := errors.New("input fault")
	source := io.MultiReader(bytes.NewReader(compressed[:17]), bzip3FaultReader{err: fault})

	reader, err = NewBzip3Reader(t.Context(), source, 4, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.Copy(io.Discard, reader)

	reader.Close()

	if !errors.Is(err, fault) {
		t.Fatalf("input fault: %v", err)
	}
}

func encodeBzip3(t testing.TB, data []byte, level, threads, chunk int) []byte {
	t.Helper()

	var output bytes.Buffer

	writer, err := NewBzip3Writer(t.Context(), &output, level, threads, 8<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	for len(data) > 0 {
		count := min(chunk, len(data))
		written, err := writer.Write(data[:count])

		if err != nil || written != count {
			t.Fatalf("write: %d, %v", written, err)
		}

		data = data[count:]
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return output.Bytes()
}

func decodeBzip3(t testing.TB, data []byte, threads int) []byte {
	t.Helper()

	reader, err := NewBzip3Reader(t.Context(), iotest.HalfReader(bytes.NewReader(data)), threads, 8<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	var (
		output bytes.Buffer
		buffer [311]byte
	)

	for {
		count, err := reader.Read(buffer[:])

		output.Write(buffer[:count])

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	err = reader.Close()
	if err != nil {
		t.Fatal(err)
	}

	return output.Bytes()
}

func rejectBzip3(t *testing.T, data []byte) {
	t.Helper()

	reader, err := NewBzip3Reader(t.Context(), bytes.NewReader(data), 4, 64<<20)
	if err == nil {
		_, err = io.Copy(io.Discard, reader)
		reader.Close()
	}

	if err == nil {
		t.Fatalf("accepted malformed stream %x", data)
	}
}
