package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

type sevenzipWorkerCase struct {
	name       string
	budget     uint64
	dictionary uint64
	workers    int
}

func TestSevenZipWorkerMemory(t *testing.T) {
	cases := []sevenzipWorkerCase{
		{name: "fallback", budget: 105 << 20, dictionary: 8 << 20, workers: 0},
		{name: "two", budget: 106 << 20, dictionary: 8 << 20, workers: 2},
		{name: "three", budget: 179 << 20, dictionary: 8 << 20, workers: 3},
		{name: "four", budget: 180 << 20, dictionary: 8 << 20, workers: 4},
		{name: "queue cap", budget: 1 << 30, dictionary: 8 << 20, workers: 6},
		{name: "large dictionary", budget: 1 << 30, dictionary: 64 << 20, workers: 0},
	}

	for _, fixture := range cases {
		t.Run(fixture.name, func(t *testing.T) {
			workers, capacity := sevenzipWorkers(8, fixture.budget, 16, 20<<20, fixture.dictionary, fixture.dictionary+1<<20)
			if workers != fixture.workers || capacity > 128<<20 {
				t.Fatalf("workers = %d, capacity = %d", workers, capacity)
			}
		})
	}

	workers, _ := sevenzipWorkers(4, 1<<30, 4, 20<<20, 8<<20, math.MaxUint64)
	if workers != 0 {
		t.Fatal("admitted unknown decoder memory")
	}
}

func TestSevenZipResetProperties(t *testing.T) {
	chunk := make([]byte, 6+(1<<16))
	copy(chunk, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0x5d})

	stream := make([]byte, 0, 17*len(chunk)+5)
	stream = append(stream, chunk...)
	stream = append(stream, 1, 0, 0, 42)

	position := len(stream)

	for range 16 {
		stream = append(stream, chunk...)
	}

	stream = append(stream, 0)
	stream[position] = 0xdf

	segments, _ := sevenzipSegments(t.Context(), bytes.NewReader(stream), 0, uint64(len(stream)), 17*(2<<20)+1)
	if segments != 17 {
		t.Fatalf("independent properties: %d segments", segments)
	}

	stream[position] = 0x9f

	segments, _ = sevenzipSegments(t.Context(), bytes.NewReader(stream), 0, uint64(len(stream)), 17*(2<<20)+1)
	if segments != 0 {
		t.Fatalf("inherited properties: %d segments", segments)
	}
}

func TestSevenZipDecoderCancellationCleanup(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "archive.7z")
	data := make([]byte, 1<<20)
	
	random := rand.NewChaCha8([32]byte{})
	random.Read(data)

	writer, err := Open7ZipWriter(t.Context(), filename, 3, 4, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	err = writer.WriteHeader(Header{Name: "payload", Size: 48 << 20, Mode: 0o644})
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

	parent, cancel := context.WithCancel(t.Context())
	defer cancel()

	ctx := &sevenZipCancelContext{Context: parent, cancel: cancel, remaining: -1}

	reader, err := Open7ZipReader(ctx, filename, 4, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()

	_, err = reader.Next()
	if err != nil {
		t.Fatal(err)
	}

	// Cancel inside the native callbacks rather than racing buffered reads.
	// The native test separately cancels inside lzma_code itself.
	ctx.remaining = 1

	_, err = io.CopyBuffer(io.Discard, reader, data)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("active extraction cancellation: %v", err)
	}

	reader.Close()

	if reader.archive != nil || reader.decoder != nil || reader.handle != 0 || reader.sevenzip != nil {
		t.Fatal("reader retained resources")
	}

	err = os.Remove(filename)
	if err != nil {
		t.Fatalf("remove input after cancellation: %v", err)
	}

	reader.Close()
}
