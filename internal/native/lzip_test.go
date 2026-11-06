package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

type lzipTestWriter struct {
	io.Writer
}

func TestLzipLevels(t *testing.T) {
	for level := 1; level <= 9; level++ {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			sizes := []int{0, 1, 4097, 64<<10 + 17}

			if level == 1 || level == 3 || level == 9 {
				sizes = append(sizes, 4<<20+17)
			}

			for _, size := range sizes {
				data := testData(size)

				encoded := encodeLzip(t, data, level)
				trailer := encoded[len(encoded)-20:]

				if string(encoded[:5]) != "LZIP\x01" || binary.LittleEndian.Uint32(trailer) != crc32.ChecksumIEEE(data) || binary.LittleEndian.Uint64(trailer[4:]) != uint64(size) || binary.LittleEndian.Uint64(trailer[12:]) != uint64(len(encoded)) {
					t.Fatalf("invalid framing at size %d", size)
				}

				var source io.Reader = bytes.NewReader(encoded)

				if size < 8192 {
					source = iotest.OneByteReader(source)
				}

				source = iotest.DataErrReader(source)

				reader, err := NewLzipReader(context.Background(), source, 1<<30)
				if err != nil {
					t.Fatal(err)
				}

				var decoded bytes.Buffer

				_, err = io.CopyBuffer(lzipTestWriter{&decoded}, reader, make([]byte, 317))

				reader.Close()

				if err != nil || !bytes.Equal(decoded.Bytes(), data) {
					t.Fatalf("size %d: round trip: %v", size, err)
				}
			}
		})
	}
}

func TestLzipIntegrity(t *testing.T) {
	encoded := encodeLzip(t, []byte("lzip integrity check\n"), 3)

	for length := range len(encoded) {
		err := decodeLzipError(t, encoded[:length], 1<<30)
		if err == nil {
			t.Fatalf("accepted truncation at %d", length)
		}
	}

	cases := map[string]int{"header": 0, "version": 4, "payload": 6, "CRC32": len(encoded) - 20, "uncompressed size": len(encoded) - 16, "member size": len(encoded) - 8}

	for name, offset := range cases {
		t.Run(name, func(t *testing.T) {
			corrupt := bytes.Clone(encoded)
			corrupt[offset] ^= 1

			err := decodeLzipError(t, corrupt, 1<<30)
			if err == nil {
				t.Fatal("accepted corruption")
			}
		})
	}

	for dictionary := range 256 {
		exponent := dictionary & 31

		valid := exponent >= 12 && exponent <= 29 && (exponent > 12 || dictionary>>5 == 0)
		if valid {
			continue
		}

		corrupt := bytes.Clone(encoded)
		corrupt[5] = byte(dictionary)

		err := decodeLzipError(t, corrupt, 1<<30)
		if err == nil || !strings.Contains(err.Error(), "dictionary size") {
			t.Fatalf("dictionary %d: %v", dictionary, err)
		}
	}

	suffixes := [][]byte{{0}, []byte("junk after stream"), []byte("LZIP\x01"), encoded[:len(encoded)-1]}

	for _, suffix := range suffixes {
		joined := append(bytes.Clone(encoded), suffix...)

		err := decodeLzipError(t, joined, 1<<30)
		if err == nil {
			t.Fatal("accepted trailing data or incomplete next member")
		}
	}
}

func TestLzipMembersAndMemory(t *testing.T) {
	first := encodeLzip(t, nil, 1)
	second := encodeLzip(t, []byte("member"), 9)

	joined := append(first, second...)
	joined = append(joined, second...)

	reader, err := NewLzipReader(context.Background(), iotest.OneByteReader(bytes.NewReader(joined)), 8<<20)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := io.ReadAll(reader)

	reader.Close()

	if err != nil || string(decoded) != "membermember" {
		t.Fatalf("concatenated members: %q, %v", decoded, err)
	}

	// The budget must apply again when a later member declares a larger dictionary.
	second[5] = 29

	err = decodeLzipError(t, append(first, second...), 8<<20)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("decoder memory preflight: %v", err)
	}

	writer, err := NewLzipWriter(context.Background(), io.Discard, 9, 8<<20, -1)
	if err == nil {
		writer.Close()
		t.Fatal("accepted encoder above memory budget")
	}

	writer, err = NewLzipWriter(context.Background(), io.Discard, 9, 8<<20, 1)
	if err != nil {
		t.Fatalf("tiny input dictionary not reduced: %v", err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLzipCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	writer, err := NewLzipWriter(ctx, io.Discard, 3, 1<<30, -1)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewLzipReader(ctx, bytes.NewReader(encodeLzip(t, []byte("data"), 3)), 1<<30)
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}

	_, err = writer.Write([]byte("started"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reader.Read(make([]byte, 1))
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	_, err = writer.Write([]byte("data"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("write: %v", err)
	}

	err = writer.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("close: %v", err)
	}

	_, err = io.Copy(io.Discard, reader)

	reader.Close()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read: %v", err)
	}
}

func TestLzipOfficialFixture(t *testing.T) {
	// Generated independently with lzip 1.25 -c -3 from "official lzip fixture\n".
	encoded, err := hex.DecodeString("4c5a4950010c003799acd264898bcccf809ea6e5a999d2a484289cfe8b1e202de69ffe799000390751ea16000000000000003a00000000000000")
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewLzipReader(t.Context(), bytes.NewReader(bytes.Repeat(encoded, 2)), 8<<20)
	if err != nil {
		t.Fatal(err)
	}

	actual, err := io.ReadAll(reader)

	reader.Close()

	if err != nil || string(actual) != "official lzip fixture\nofficial lzip fixture\n" {
		t.Fatalf("independent fixture: %q, %v", actual, err)
	}
}

func encodeLzip(t testing.TB, data []byte, level int) []byte {
	t.Helper()

	var encoded bytes.Buffer

	writer, err := NewLzipWriter(context.Background(), &encoded, level, 1<<30, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	for offset := 0; offset < len(data); {
		length := min(8191, len(data)-offset)

		_, err = writer.Write(data[offset : offset+length])
		if err != nil {
			t.Fatal(err)
		}

		offset += length
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}

func decodeLzipError(t *testing.T, encoded []byte, memory uint64) error {
	t.Helper()

	reader, err := NewLzipReader(context.Background(), bytes.NewReader(encoded), memory)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.Copy(io.Discard, reader)

	reader.Close()

	return err
}
