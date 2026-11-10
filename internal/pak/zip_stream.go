package pak

import (
	"bytes"
	"context"
	"io"
)

const zipStreamChunks = 8

type zipChunk struct {
	data       []byte
	compressed []byte
	err        error
}

type zipStream struct {
	ctx   context.Context
	ready chan zipChunk
	free  chan zipChunk
}

type zipStreamEncoder struct {
	stream *zipStream
	codec  io.WriteCloser
	output bytes.Buffer
}

type zipStreamWriter struct {
	target io.Writer
	data   []byte
}

func (stream *zipStream) send(chunk zipChunk) error {
	select {
	case stream.ready <- chunk:
		return nil
	case <-stream.ctx.Done():
		return stream.ctx.Err()
	}
}

func (stream *zipStream) take() (zipChunk, error) {
	select {
	case chunk := <-stream.free:
		return chunk, nil
	case <-stream.ctx.Done():
		return zipChunk{}, stream.ctx.Err()
	}
}

func (stream *zipStream) write(target io.Writer, compressed *zipStreamWriter) error {
	for {
		var (
			chunk zipChunk
			ok    bool
		)

		select {
		case chunk, ok = <-stream.ready:
		case <-stream.ctx.Done():
			return stream.ctx.Err()
		}

		if !ok {
			return stream.ctx.Err()
		}

		if chunk.err != nil {
			return chunk.err
		}

		// Feed the original bytes through archive/zip for its CRC and size accounting,
		// while its compressor emits the corresponding worker-produced deflate bytes.
		compressed.data = chunk.compressed
		_, err := target.Write(chunk.data)
		compressed.data = nil

		if err != nil {
			return err
		}

		stream.free <- chunk
	}
}

func (encoder *zipStreamEncoder) Write(data []byte) (int, error) {
	chunk, err := encoder.stream.take()
	if err != nil {
		return 0, err
	}

	chunk.data = append(chunk.data[:0], data...)

	encoder.output = *bytes.NewBuffer(chunk.compressed[:0])
	_, err = encoder.codec.Write(data)

	chunk.compressed = encoder.output.Bytes()
	encoder.output = bytes.Buffer{}

	if err != nil {
		return 0, err
	}

	err = encoder.stream.send(chunk)
	if err != nil {
		return 0, err
	}

	return len(data), nil
}

func (encoder *zipStreamEncoder) finish(prior error) error {
	if prior != nil {
		return closeWithError(encoder.codec, prior)
	}

	chunk, err := encoder.stream.take()
	if err != nil {
		return closeWithError(encoder.codec, err)
	}

	chunk.data = chunk.data[:0]
	encoder.output = *bytes.NewBuffer(chunk.compressed[:0])

	err = encoder.codec.Close()

	chunk.compressed = encoder.output.Bytes()
	encoder.output = bytes.Buffer{}

	if err != nil {
		return err
	}

	return encoder.stream.send(chunk)
}

func (writer *zipStreamWriter) Write(data []byte) (int, error) {
	count, err := writer.target.Write(writer.data)
	if err == nil && count != len(writer.data) {
		err = io.ErrShortWrite
	}

	if err != nil {
		return 0, err
	}

	return len(data), nil
}

func (writer *zipStreamWriter) Close() error {
	return nil
}

func newZipStream(ctx context.Context, free chan zipChunk) *zipStream {
	// A fixed number of reusable chunks bounds memory and compressor look-ahead
	// independently of entry size. All queue waits observe the operation's context.
	if free == nil {
		free = make(chan zipChunk, zipStreamChunks)

		for range zipStreamChunks {
			free <- zipChunk{}
		}
	}

	// Retain each worker's chunks across entries, rather than allocating another
	// full reorder window while the previous entry's storage awaits collection.
	stream := &zipStream{
		ctx:   ctx,
		ready: make(chan zipChunk, zipStreamChunks),
		free:  free,
	}

	return stream
}
