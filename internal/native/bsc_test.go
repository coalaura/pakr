package native

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/adler32"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestBSCStreams(t *testing.T) {
	sizes := []int{0, 1, 63, 64, 4096, 1 << 20, 3<<20 + 37}
	workers := []int{1, 4}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := testData(size)

			var reference []byte

			for _, threads := range workers {
				compressed := encodeBSC(t, data, 1, threads, 317)
				if reference != nil && !bytes.Equal(reference, compressed) {
					t.Fatal("output depends on worker scheduling")
				}

				reference = compressed

				actual := decodeBSC(t, compressed, threads)
				if !bytes.Equal(data, actual) {
					t.Fatal("round-trip content mismatch")
				}
			}

			whole := encodeBSC(t, data, 1, 4, max(1, size))
			if !bytes.Equal(reference, whole) {
				t.Fatal("output depends on Write boundaries")
			}
		})
	}
}

func TestBSCLevels(t *testing.T) {
	data := testData(256 << 10)

	for level := 1; level <= 9; level++ {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			compressed := encodeBSC(t, data, level, 4, len(data))
			serial := encodeBSC(t, data, level, 1, 317)

			if !bytes.Equal(compressed, serial) {
				t.Fatal("level output depends on thread count or Write boundaries")
			}

			if !bytes.Equal(data, decodeBSC(t, compressed, 4)) {
				t.Fatal("level round trip failed")
			}

			tiny := encodeBSC(t, []byte("tiny"), level, 1, 1)
			if string(decodeBSC(t, tiny, 1)) != "tiny" {
				t.Fatal("tiny round trip failed")
			}
		})
	}
}

func TestBSCReorderedBlocks(t *testing.T) {
	data := testData(3<<20 + 17)
	encoded := encodeBSC(t, data, 1, 4, len(data))
	blocks := splitBSC(t, encoded)
	reordered := bytes.Clone(encoded[:8])

	for _, block := range slices.Backward(blocks) {
		reordered = append(reordered, block...)
	}

	if !bytes.Equal(data, decodeBSC(t, reordered, 4)) {
		t.Fatal("physical block order affected output")
	}
}

func TestBSCIncompressible(t *testing.T) {
	random := rand.New(rand.NewPCG(1729, 340))
	data := make([]byte, 1<<20+17)

	for index := range data {
		data[index] = byte(random.Uint32())
	}

	levels := []int{1, 3, 9}

	for _, level := range levels {
		encoded := encodeBSC(t, data, level, 4, len(data))
		if !bytes.Equal(data, decodeBSC(t, encoded, 4)) {
			t.Fatalf("incompressible fallback changed input at level %d", level)
		}
	}
}

func TestBSCOfficialFixtures(t *testing.T) {
	// Pristine official 3.4.0: -e0/-e1/-e2 -cp -T1, including preceding contexts.
	fixtures := []string{
		"YnNjMQEAAAAAAAAAAAAAAAECWgAAADQIAABhSA8AAQAAAIYJ753LG1jmjwWDKgEAAAAAggCJOy9bD1nXzQ4Kumy0b46TN6nW2jakDEvRBmKoNcV1vU/X5TmaDHR1LzH807rFemj92IbnAPgA",
		"YnNjMQEAAAAAAAAAAAAAAAECWAAAADQIAAAhSA8AAQAAAIYJ752OHJHlSQXTJQEAAAAAggCJOy9bD1nXzQ4Kumy0b46TN6nW2mCk/CPIPnmXzEg21bSz+EPDkFSJyBmq/fh/xDxKqNynAA==",
		"YnNjMQEAAAAAAAAAAAAAAAECWAAAADQIAABBSA8AAQAAAIYJ751UGmzR9ASHJgEAAAAAggCJOy9bD1nXzQ4Kumy0b46TN6nW2mKkzFi83T0a1w/mo+TSIOAgQDb/cxx8+ki3Pz1wiHudAA==",
	}

	data := bytes.Repeat([]byte("official bsc fixture\n"), 100)

	for _, fixture := range fixtures {
		encoded, err := base64.StdEncoding.DecodeString(fixture)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(data, decodeBSC(t, encoded, 4)) {
			t.Fatal("official fixture mismatch")
		}
	}
}

func TestBSCChecksummedMalformedPayload(t *testing.T) {
	encoded := encodeBSC(t, testData(4096), 3, 1, 4096)

	for offset := 46; offset < len(encoded); offset++ {
		data := bytes.Clone(encoded)
		data[offset] ^= 255
		binary.LittleEndian.PutUint32(data[38:], adler32.Checksum(data[46:]))
		binary.LittleEndian.PutUint32(data[42:], adler32.Checksum(data[18:42]))

		reader, err := NewBSCReader(t.Context(), bytes.NewReader(data), int64(len(data)), 1, 64<<20)
		if err != nil {
			continue
		}

		actual, err := io.ReadAll(reader)
		reader.Close()

		if err == nil && !bytes.Equal(actual, testData(4096)) {
			t.Fatalf("accepted corrupt output after mutation at %d", offset)
		}
	}
}

func TestBSCMalformed(t *testing.T) {
	encoded := encodeBSC(t, testData(2<<20+1), 1, 4, 32<<10)

	for length := range len(encoded) {
		reader, err := NewBSCReader(t.Context(), bytes.NewReader(encoded[:length]), int64(length), 4, 256<<20)
		if err == nil {
			_, err = io.Copy(io.Discard, reader)
			reader.Close()
		}

		if err == nil {
			t.Fatalf("accepted truncation at %d", length)
		}
	}

	mutations := []func([]byte){
		func(data []byte) {
			data[0] = 'x'
		},
		func(data []byte) {
			binary.LittleEndian.PutUint32(data[4:], 0xffffffff)
		},
		func(data []byte) {
			binary.LittleEndian.PutUint32(data[4:], 0)
		},
		func(data []byte) {
			binary.LittleEndian.PutUint64(data[8:], 1)
		},
		func(data []byte) {
			binary.LittleEndian.PutUint64(data[8:], 0xffffffffffffffff)
		},
		func(data []byte) {
			data[16] = 0
		},
		func(data []byte) {
			data[17] = 3
		},
		func(data []byte) {
			data[18] ^= 1
		},
		func(data []byte) {
			data[46] ^= 1
		},
		func(data []byte) {
			binary.LittleEndian.PutUint32(data[22:], 0xffffffff)
			binary.LittleEndian.PutUint32(data[42:], adler32.Checksum(data[18:42]))
		},
		func(data []byte) {
			packed := int(binary.LittleEndian.Uint32(data[18:]))
			binary.LittleEndian.PutUint64(data[18+packed:], 0)
		},
	}

	for index, mutate := range mutations {
		data := bytes.Clone(encoded)
		mutate(data)

		reader, err := NewBSCReader(t.Context(), bytes.NewReader(data), int64(len(data)), 4, 256<<20)
		if err == nil {
			_, err = io.Copy(io.Discard, reader)
			reader.Close()
		}

		if err == nil {
			t.Fatalf("accepted malformed archive %d", index)
		}
	}
}

func TestBSCMemoryAndCancellation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "output-*")
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	_, err = NewBSCWriter(t.Context(), file, 9, 4, 128<<20)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected memory rejection: %v", err)
	}

	writer, err := NewBSCWriter(t.Context(), file, 1, 16, 64<<20)
	if err != nil {
		t.Fatal(err)
	}

	if len(writer.blocks.slots) != 1 {
		t.Fatalf("expected one worker, got %d", len(writer.blocks.slots))
	}

	writer.Close()

	ctx, cancel := context.WithCancel(t.Context())

	writer, err = NewBSCWriter(ctx, file, 1, 4, 256<<20)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write(testData(2 << 20))
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	if !errors.Is(writer.Close(), context.Canceled) || writer.blocks.slots != nil {
		t.Fatal("cancelled writer did not release workers")
	}

	encoded := encodeBSC(t, testData(2<<20), 1, 4, 32<<10)

	_, err = NewBSCReader(t.Context(), bytes.NewReader(encoded), int64(len(encoded)), 4, 1)
	if err == nil {
		t.Fatal("decoder accepted impossible memory budget")
	}

	ctx, cancel = context.WithCancel(t.Context())

	reader, err := NewBSCReader(ctx, bytes.NewReader(encoded), int64(len(encoded)), 4, 256<<20)
	if err != nil {
		t.Fatal(err)
	}

	buffer := make([]byte, 1)

	_, err = reader.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	_, err = reader.Read(buffer)
	if !errors.Is(err, context.Canceled) || reader.blocks.slots != nil {
		t.Fatal("cancelled reader did not release workers")
	}
}

func FuzzBSCReader(f *testing.F) {
	f.Add(encodeBSC(f, testData(4096), 1, 1, 4096))
	f.Add([]byte("bsc1\x00\x00\x00\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}

		reader, err := NewBSCReader(t.Context(), bytes.NewReader(data), int64(len(data)), 2, 64<<20)
		if err != nil {
			return
		}

		defer reader.Close()

		io.Copy(io.Discard, reader)
	})
}

func encodeBSC(t testing.TB, data []byte, level, threads, chunk int) []byte {
	t.Helper()

	file, err := os.Create(filepath.Join(t.TempDir(), "stream.bsc"))
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	writer, err := NewBSCWriter(t.Context(), file, level, threads, 8<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	for len(data) > 0 {
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

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	return encoded
}

func decodeBSC(t testing.TB, encoded []byte, threads int) []byte {
	t.Helper()

	reader, err := NewBSCReader(t.Context(), bytes.NewReader(encoded), int64(len(encoded)), threads, 8<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	actual, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	return actual
}

func splitBSC(t testing.TB, encoded []byte) [][]byte {
	t.Helper()

	count := int(binary.LittleEndian.Uint32(encoded[4:]))
	blocks := make([][]byte, 0, count)

	for position := 8; position < len(encoded); {
		length := 10 + int(binary.LittleEndian.Uint32(encoded[position+10:]))
		blocks = append(blocks, encoded[position:position+length])
		position += length
	}

	return blocks
}
