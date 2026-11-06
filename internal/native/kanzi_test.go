package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
	"testing/iotest"
)

type kanziFailWriter struct {
	err error
}

func (writer kanziFailWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func TestKanziLevels(t *testing.T) {
	inputs := [][]byte{nil, []byte("a"), bytes.Repeat([]byte("Kanzi streaming compression and native transforms\n"), 2048)}

	for level := 1; level <= 9; level++ {
		for index, input := range inputs {
			t.Run(fmt.Sprintf("level%d/input%d", level, index), func(t *testing.T) {
				encoded := encodeKanzi(t, input, level, 4, int64(len(input)))

				decoded := decodeKanzi(t, encoded, 4)
				if !bytes.Equal(decoded, input) {
					t.Fatal("round trip differs")
				}
			})
		}
	}
}

func TestKanziParallelUnknownSize(t *testing.T) {
	input := make([]byte, 9<<20)
	random := rand.NewChaCha8([32]byte{42})

	_, err := random.Read(input)
	if err != nil {
		t.Fatal(err)
	}

	encoded := encodeKanzi(t, input, 3, 4, -1)

	workers := []int{1, 4}

	for _, jobs := range workers {
		decoded := decodeKanzi(t, encoded, jobs)
		if !bytes.Equal(decoded, input) {
			t.Fatal("parallel random round trip differs")
		}
	}
}

func TestKanziRejectsDamagedStreams(t *testing.T) {
	input := bytes.Repeat([]byte("checksum verification\n"), 4096)
	encoded := encodeKanzi(t, input, 3, 1, int64(len(input)))

	for length := range len(encoded) {
		reader, err := NewKanziReader(context.Background(), bytes.NewReader(encoded[:length]), 1, 1<<30)
		if err == nil {
			_, err = io.Copy(io.Discard, reader)
			reader.Close()
		}

		if err == nil {
			t.Fatalf("accepted truncated stream at %d/%d", length, len(encoded))
		}
	}

	for index := range encoded {
		corrupt := bytes.Clone(encoded)
		corrupt[index] ^= 0x80

		reader, err := NewKanziReader(context.Background(), bytes.NewReader(corrupt), 1, 1<<30)
		if err == nil {
			_, err = io.Copy(io.Discard, reader)
			reader.Close()
		}

		// Header padding and final bit padding are not checksummed upstream.
		if err == nil && !bytes.Equal(decodeKanzi(t, corrupt, 1), input) {
			t.Fatalf("undetected data corruption at %d", index)
		}
	}

	trailing := append(bytes.Clone(encoded), 0)

	reader, err := NewKanziReader(context.Background(), bytes.NewReader(trailing), 1, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	_, err = io.Copy(io.Discard, reader)
	if err == nil {
		t.Fatal("accepted trailing data")
	}
}

func TestKanziAdmissionAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	writer, err := NewKanziWriter(ctx, io.Discard, 3, 16, 200<<20, -1)
	if err != nil {
		t.Fatal(err)
	}

	if writer.Settings.Jobs != 1 {
		t.Fatalf("admitted %d workers instead of 1", writer.Settings.Jobs)
	}

	cancel()

	_, err = writer.Write([]byte("cancelled"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("write cancellation: %v", err)
	}

	writer.Close()

	_, err = NewKanziWriter(context.Background(), io.Discard, 9, 16, 256<<20, -1)
	if err == nil {
		t.Fatal("accepted insufficient encode budget")
	}

	encoded := encodeKanzi(t, []byte("budget"), 9, 1, -1)

	_, err = NewKanziReader(context.Background(), bytes.NewReader(encoded), 16, 256<<20)
	if err == nil {
		t.Fatal("accepted insufficient decode budget")
	}

	_, err = NewKanziReader(ctx, bytes.NewReader(encoded), 1, 64<<30)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reader cancellation: %v", err)
	}
}

func TestKanziDeclaredSize(t *testing.T) {
	var encoded bytes.Buffer

	writer, err := NewKanziWriter(context.Background(), &encoded, 1, 1, 1<<30, 2)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err == nil {
		t.Fatal("accepted incomplete input")
	}

	reader, err := NewKanziReader(context.Background(), bytes.NewReader(encoded.Bytes()), 1, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	_, err = io.Copy(io.Discard, reader)
	if err == nil {
		t.Fatal("accepted output smaller than declared size")
	}
}

func TestKanziIOFailures(t *testing.T) {
	failure := errors.New("injected I/O failure")

	_, err := NewKanziReader(context.Background(), iotest.ErrReader(failure), 1, 1<<30)
	if !errors.Is(err, failure) {
		t.Fatalf("header read failure: %v", err)
	}

	writer, err := NewKanziWriter(context.Background(), kanziFailWriter{err: failure}, 3, 4, 1<<30, -1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write([]byte("buffered output"))
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if !errors.Is(err, failure) {
		t.Fatalf("output failure: %v", err)
	}

	input := bytes.Repeat([]byte("short reads and buffered cancellation\n"), 1024)
	encoded := encodeKanzi(t, input, 3, 4, -1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reader, err := NewKanziReader(ctx, iotest.OneByteReader(bytes.NewReader(encoded)), 4, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	var first [1]byte

	_, err = io.ReadFull(reader, first[:])
	if err != nil || first[0] != input[0] {
		t.Fatalf("short reads: %q, %v", first, err)
	}

	cancel()

	_, err = reader.Read(first[:])
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("buffered cancellation: %v", err)
	}
}

func encodeKanzi(t *testing.T, input []byte, level, jobs int, size int64) []byte {
	t.Helper()

	var encoded bytes.Buffer

	writer, err := NewKanziWriter(context.Background(), &encoded, level, jobs, 64<<30, size)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.Copy(writer, bytes.NewReader(input))
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}

func decodeKanzi(t *testing.T, encoded []byte, jobs int) []byte {
	t.Helper()

	reader, err := NewKanziReader(context.Background(), bytes.NewReader(encoded), jobs, 64<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	return decoded
}
