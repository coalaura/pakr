package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

type streamTestSource struct {
	data  []byte
	err   error
	empty int
}

type streamTestTarget struct {
	err    error
	short  bool
	cancel context.CancelFunc
}

type streamTestGate struct {
	started chan struct{}
	release chan struct{}
}

func (source *streamTestSource) Read(data []byte) (int, error) {
	if source.empty > 0 {
		source.empty--

		return 0, nil
	}

	count := copy(data, source.data)
	source.data = source.data[count:]

	if len(source.data) == 0 {
		return count, source.err
	}

	return count, nil
}

func (target streamTestTarget) Write(data []byte) (int, error) {
	if target.cancel != nil {
		target.cancel()
	}

	if target.err != nil {
		return 0, target.err
	}

	if target.short {
		return len(data) - 1, nil
	}

	return len(data), nil
}

func (source streamTestGate) Read([]byte) (int, error) {
	close(source.started)
	<-source.release

	return 0, io.EOF
}

func TestStreamReaderDelivery(t *testing.T) {
	data := make([]byte, 65539)
	
	random := rand.NewChaCha8([32]byte{1})
	_, _ = random.Read(data)

	failure := errors.New("source failed")
	errorsToTest := []error{io.EOF, failure}

	for _, terminal := range errorsToTest {
		t.Run(terminal.Error(), func(t *testing.T) {
			source := &streamTestSource{data: data, err: terminal, empty: 3}

			reader := newStreamReader(t.Context(), source, 1024)
			defer reader.Close()

			var actual bytes.Buffer

			// Exercise partial Read followed by borrowed chunks, including data plus error.
			prefix := make([]byte, 13)

			_, err := io.ReadFull(reader, prefix)
			if err != nil {
				t.Fatal(err)
			}

			actual.Write(prefix)

			copying := copier{ctx: t.Context(), buffer: make([]byte, 17)}
			count, err := copying.copy(&actual, reader)

			if errors.Is(terminal, io.EOF) {
				terminal = nil
			}

			if !errors.Is(err, terminal) || count != int64(len(data)-len(prefix)) || !bytes.Equal(actual.Bytes(), data) {
				t.Fatalf("delivery: bytes=%d, error=%v, equal=%v", count, err, bytes.Equal(actual.Bytes(), data))
			}
		})
	}
}

func TestStreamReaderPartialFinalChunk(t *testing.T) {
	failure := errors.New("final error")
	source := &streamTestSource{data: []byte("final bytes"), err: failure}

	reader := newStreamReader(t.Context(), source, 32)
	defer reader.Close()

	var actual bytes.Buffer

	buffer := make([]byte, 3)

	for {
		count, err := reader.Read(buffer)
		actual.Write(buffer[:count])

		if err != nil {
			if !errors.Is(err, failure) || actual.String() != "final bytes" {
				t.Fatalf("partial terminal chunk: %q, %v", actual.String(), err)
			}

			break
		}
	}
}

func TestStreamReaderFailures(t *testing.T) {
	failure := errors.New("write failed")
	targets := []streamTestTarget{{err: failure}, {short: true}, {}}
	expected := []error{failure, io.ErrShortWrite, context.Canceled}

	for index, target := range targets {
		ctx, cancel := context.WithCancel(t.Context())

		if index == 2 {
			target.cancel = cancel
		}

		reader := newStreamReader(ctx, bytes.NewReader(make([]byte, 8192)), 64)

		copying := copier{ctx: ctx}

		_, err := copying.copy(target, reader)
		if !errors.Is(err, expected[index]) {
			t.Fatalf("copy failure: got %v, want %v", err, expected[index])
		}

		closeStreamTest(t, reader)
		cancel()
	}

	source := &streamTestSource{empty: streamEmptyReadLimit}

	reader := newStreamReader(t.Context(), source, 64)
	defer reader.Close()

	_, err := reader.next()
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("empty reads: %v", err)
	}
}

func TestStreamReaderCancellationAndJoin(t *testing.T) {
	source := streamTestGate{started: make(chan struct{}), release: make(chan struct{})}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reader := newStreamReader(ctx, source, 64)

	<-source.started
	cancel()

	_, err := reader.next()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting consumer: %v", err)
	}

	select {
	case <-reader.done:
		t.Fatal("worker ended before its source read completed")
	default:
	}

	close(source.release)
	closeStreamTest(t, reader)
	closeStreamTest(t, reader)
}

func TestStreamFrames(t *testing.T) {
	data := make([]byte, 4<<20+37)

	random := rand.NewChaCha8([32]byte{2})
	_, _ = random.Read(data)
	
	names := []string{"lz4", "zst", "br", "tar.lz4", "tar.zst", "tar.br"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			encoded := encodeStreamTest(t, format, data)
			if len(encoded) < native.BufferSize {
				t.Fatal("fixture does not exercise the streaming path")
			}

			unpackStreamTest(t, format, encoded, data, false)
			unpackStreamTest(t, format, encoded[:len(encoded)-1], nil, true)

			if format.Codec == native.Brotli {
				trailing := append(bytes.Clone(encoded), 0)
				unpackStreamTest(t, format, trailing, nil, true)

				return
			}

			corrupt := bytes.Clone(encoded)
			corrupt[len(corrupt)-1] ^= 1

			unpackStreamTest(t, format, corrupt, nil, true)

			if format.Container == "" {
				joined := make([]byte, 0, 2*len(encoded)+11)
				joined = append(joined, encoded...)
				joined = binary.LittleEndian.AppendUint32(joined, 0x184d2a50)
				joined = binary.LittleEndian.AppendUint32(joined, 3)
				joined = append(joined, 's', 'k', 'p')
				joined = append(joined, encoded...)

				unpackStreamTest(t, format, joined, bytes.Repeat(data, 2), false)
				unpackStreamTest(t, format, joined[:len(encoded)+10], nil, true)
			}
		})
	}
}

func closeStreamTest(t *testing.T, reader *streamReader) {
	t.Helper()

	closed := make(chan struct{})

	go func() {
		reader.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("stream worker did not terminate")
	}
}

func encodeStreamTest(t *testing.T, format Format, data []byte) []byte {
	t.Helper()

	var encoded bytes.Buffer

	writer, err := native.NewWriter(&encoded, format.Codec, 3, 4)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	var (
		target  io.Writer = writer
		archive *tar.Writer
	)

	if format.Container == "tar" {
		archive = tar.NewWriter(writer)
		target = archive

		header := tar.Header{Name: "payload", Mode: 0o600, Size: int64(len(data))}

		err = archive.WriteHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = target.Write(data)
	if err != nil {
		t.Fatal(err)
	}

	if archive != nil {
		err = archive.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}

func unpackStreamTest(t *testing.T, format Format, encoded, expected []byte, reject bool) {
	t.Helper()

	base := t.TempDir()
	input := filepath.Join(base, "payload."+format.Name)

	err := os.WriteFile(input, encoded, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(base, "output")
	options := Options{Format: format, Output: output, Threads: 4}
	
	err = Unpack(t.Context(), input, options, nil)

	if reject {
		if err == nil {
			t.Fatal("invalid stream accepted")
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}

		actual, readErr := os.ReadFile(filepath.Join(output, "payload"))
		if readErr != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("extracted content: %v, equal=%v", readErr, bytes.Equal(actual, expected))
		}
	}

	err = os.Remove(input)
	if err != nil {
		t.Fatalf("input handle retained: %v", err)
	}
}
