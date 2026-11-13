package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSevenZipLevels(t *testing.T) {
	data := bytes.Repeat([]byte("7z level round trip\n"), 4096)

	for level := 1; level <= 9; level++ {
		t.Run(strconv.Itoa(level), func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "archive.7z")

			writer, err := Open7ZipWriter(t.Context(), filename, level, 2, 1<<30)
			if err != nil {
				t.Fatal(err)
			}

			defer writer.Close()

			header := Header{Name: "payload", Size: int64(len(data)), Mode: 0o640}

			err = writer.WriteHeader(header)
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

			reader, err := Open7ZipReader(t.Context(), filename, 2, 1<<30)
			if err != nil {
				t.Fatal(err)
			}

			defer reader.Close()

			_, err = reader.Next()
			if err != nil {
				t.Fatal(err)
			}

			decoded, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(data, decoded) {
				t.Fatalf("round trip: %v, decoded %d bytes", err, len(decoded))
			}

			_, err = reader.Next()
			if !errors.Is(err, io.EOF) {
				t.Fatalf("archive end: %v", err)
			}
		})
	}
}

func TestSevenZipAdmission(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "archive.7z")

	writer, err := Open7ZipWriter(t.Context(), filename, 9, 4, 16<<20)
	if err == nil {
		writer.Close()
		t.Fatal("encoder accepted insufficient memory")
	}

	_, err = os.Stat(filename)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("admission created destination: %v", err)
	}

	writer, err = Open7ZipWriter(t.Context(), filename, 3, 201, 8<<30)
	if err == nil {
		writer.Close()
		t.Fatal("encoder accepted unsupported worker count")
	}

	writer, err = Open7ZipWriter(t.Context(), filename, 3, 2, 0)
	if err != nil {
		t.Fatalf("automatic memory budget: %v", err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSevenZipCancelledClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	filename := filepath.Join(t.TempDir(), "archive.7z")

	writer, err := Open7ZipWriter(ctx, filename, 3, 4, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	header := Header{Name: "payload", Size: 1}

	err = writer.WriteHeader(header)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write([]byte{42})
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	err = writer.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("close after cancellation: %v", err)
	}
}

func TestSevenZipResetRoundTrip(t *testing.T) {
	data := make([]byte, 1<<20)

	random := rand.NewChaCha8([32]byte{})
	random.Read(data)

	filename := filepath.Join(t.TempDir(), "archive.7z")

	writer, err := Open7ZipWriter(t.Context(), filename, 3, 4, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	header := Header{Name: "payload", Size: 48 << 20, Mode: 0o644}

	err = writer.WriteHeader(header)
	if err != nil {
		t.Fatal(err)
	}

	for range 48 {
		_, err = writer.Write(data)
		if err != nil {
			t.Fatal(err)
		}
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	budgets := []uint64{32 << 20, 1 << 30}
	decoded := make([]byte, len(data))

	for _, budget := range budgets {
		reader, err := Open7ZipReader(t.Context(), filename, 4, budget)
		if err != nil {
			t.Fatal(err)
		}

		_, err = reader.Next()
		if err != nil {
			reader.Close()
			t.Fatal(err)
		}

		for range 48 {
			_, err = io.ReadFull(reader, decoded)
			if err != nil || !bytes.Equal(data, decoded) {
				reader.Close()
				t.Fatalf("reset stream at budget %d: %v", budget, err)
			}
		}

		_, err = reader.Next()
		if !errors.Is(err, io.EOF) {
			reader.Close()
			t.Fatalf("reset stream end: %v", err)
		}

		err = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reader, err := Open7ZipReader(ctx, filename, 4, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = reader.Next()
	if err != nil {
		reader.Close()
		t.Fatal(err)
	}

	cancel()

	_, err = io.CopyBuffer(io.Discard, reader, decoded)
	reader.Close()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reset decoder cancellation: %v", err)
	}

	file, err := os.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}

	var damaged [1]byte

	_, err = file.ReadAt(damaged[:], 4096)
	if err != nil {
		file.Close()
		t.Fatal(err)
	}

	damaged[0] ^= 0x80

	_, err = file.WriteAt(damaged[:], 4096)
	file.Close()

	if err != nil {
		t.Fatal(err)
	}

	for _, budget := range budgets {
		reader, err = Open7ZipReader(t.Context(), filename, 4, budget)
		if err != nil {
			t.Fatal(err)
		}

		_, err = reader.Next()
		if err == nil {
			_, err = io.CopyBuffer(io.Discard, reader, decoded)
		}

		reader.Close()

		if err == nil {
			t.Fatalf("corrupt reset stream accepted at budget %d", budget)
		}
	}
}

func TestSevenZipSegmentScan(t *testing.T) {
	// Payload bytes are intentionally opaque: this tests only chunk framing.
	chunk := make([]byte, 6+(1<<16))
	copy(chunk, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0x5d})

	stream := make([]byte, 0, 17*len(chunk)+1)

	for range 17 {
		stream = append(stream, chunk...)
	}

	stream = append(stream, 0)

	segments, largest := sevenzipSegments(t.Context(), bytes.NewReader(stream), 0, uint64(len(stream)), 17*(2<<20))
	if segments != 17 || largest != uint64(len(chunk))+(2<<20) {
		t.Fatalf("segments = %d, largest = %d", segments, largest)
	}

	truncated := stream[:len(stream)-1]

	segments, _ = sevenzipSegments(t.Context(), bytes.NewReader(truncated), 0, uint64(len(stream)), 17*(2<<20))
	if segments != 0 {
		t.Fatal("accepted truncated framing")
	}

	for index := 1; index < 17; index++ {
		stream[index*len(chunk)] = 0xdf
	}

	segments, _ = sevenzipSegments(t.Context(), bytes.NewReader(stream), 0, uint64(len(stream)), 17*(2<<20))
	if segments != 0 {
		t.Fatal("accepted unbounded dependency chain")
	}
}
