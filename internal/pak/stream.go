package pak

import (
	"context"
	"io"
)

const streamEmptyReadLimit = 100

type streamChunk struct {
	data []byte
	err  error
}

// streamReader overlaps a sequential decoder with its consumer. Exactly two
// buffers circulate between the producer, queue and consumer, bounding read-ahead.
// The source must finish Read without consumer assistance; Close joins that read.
type streamReader struct {
	ctx     context.Context
	cancel  context.CancelFunc
	source  io.Reader
	free    chan []byte
	ready   chan streamChunk
	done    chan struct{}
	pending streamChunk
	buffer  []byte
}

func (reader *streamReader) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	chunk, err := reader.next()
	count := copy(data, chunk)
	reader.pending.data = chunk[count:]

	if len(reader.pending.data) != 0 {
		err = nil
	}

	return count, err
}

func (reader *streamReader) Close() error {
	reader.cancel()
	<-reader.done

	return nil
}

// next lends a chunk until the next Read or next call, avoiding a copy for raw streams.
func (reader *streamReader) next() ([]byte, error) {
	err := reader.ctx.Err()
	if err != nil {
		return nil, err
	}

	if len(reader.pending.data) == 0 {
		if reader.pending.err != nil {
			return nil, reader.pending.err
		}

		if reader.buffer != nil {
			reader.free <- reader.buffer
			reader.buffer = nil
		}

		select {
		case <-reader.ctx.Done():
			return nil, reader.ctx.Err()
		case reader.pending = <-reader.ready:
		}

		reader.buffer = reader.pending.data[:cap(reader.pending.data)]
	}

	data := reader.pending.data
	reader.pending.data = nil

	return data, reader.pending.err
}

func (reader *streamReader) run() {
	defer close(reader.done)

	for {
		var buffer []byte

		select {
		case <-reader.ctx.Done():
			return
		case buffer = <-reader.free:
		}

		count, err := reader.read(buffer)

		select {
		case <-reader.ctx.Done():
			return
		case reader.ready <- streamChunk{data: buffer[:count], err: err}:
		}

		if err != nil {
			return
		}
	}
}

func (reader *streamReader) read(buffer []byte) (int, error) {
	for range streamEmptyReadLimit {
		err := reader.ctx.Err()
		if err != nil {
			return 0, err
		}

		count, err := reader.source.Read(buffer)
		if count != 0 || err != nil {
			return count, err
		}
	}

	return 0, io.ErrNoProgress
}

func newStreamReader(ctx context.Context, source io.Reader, bufferSize int) *streamReader {
	ctx, cancel := context.WithCancel(ctx)
	
	reader := &streamReader{
		ctx:    ctx,
		cancel: cancel,
		source: source,
		free:   make(chan []byte, 2),
		ready:  make(chan streamChunk, 2),
		done:   make(chan struct{}),
	}

	for range 2 {
		reader.free <- make([]byte, bufferSize)
	}

	go reader.run()

	return reader
}
