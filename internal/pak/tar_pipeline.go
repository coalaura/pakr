package pak

import (
	"io"

	"github.com/coalaura/pakr/internal/native"
)

type tarBuffer interface {
	io.Writer
	Available() int
	AvailableBuffer() []byte
	Flush() error
}

type tarBlock struct {
	data []byte
	err  error
}

type tarPipeline struct {
	data  []byte
	ready chan []byte
	free  chan tarBlock
	done  chan error
}

func (pipeline *tarPipeline) Available() int {
	return cap(pipeline.data) - len(pipeline.data)
}

func (pipeline *tarPipeline) AvailableBuffer() []byte {
	return pipeline.data[len(pipeline.data):]
}

func (pipeline *tarPipeline) Write(data []byte) (int, error) {
	written := 0

	for len(data) > 0 {
		if pipeline.Available() == 0 {
			err := pipeline.Flush()
			if err != nil {
				return written, err
			}
		}

		count := min(len(data), pipeline.Available())
		pipeline.data = append(pipeline.data, data[:count]...)

		written += count
		data = data[count:]
	}

	return written, nil
}

func (pipeline *tarPipeline) Flush() error {
	if len(pipeline.data) == 0 {
		return nil
	}

	// Ownership moves to the worker until its Write returns. Taking the
	// other block here also bounds the producer's lead to one block.
	pipeline.ready <- pipeline.data
	next := <-pipeline.free
	pipeline.data = next.data

	return next.err
}

// Close waits for submitted writes only. On error the producer discards
// its unfinished block; the success path explicitly flushes the footer.
func (pipeline *tarPipeline) Close() error {
	close(pipeline.ready)

	return <-pipeline.done
}

func (pipeline *tarPipeline) drain(target io.Writer) {
	var err error

	for block := range pipeline.ready {
		if err == nil {
			var written int

			written, err = target.Write(block)
			if err == nil && written != len(block) {
				err = io.ErrShortWrite
			}
		}

		pipeline.free <- tarBlock{data: block[:0], err: err}
	}

	pipeline.done <- err
}

func newTarPipeline(target io.Writer) *tarPipeline {
	pipeline := &tarPipeline{
		data:  make([]byte, 0, native.BufferSize),
		ready: make(chan []byte, 1),
		free:  make(chan tarBlock, 1),
		done:  make(chan error),
	}

	pipeline.free <- tarBlock{data: make([]byte, 0, native.BufferSize)}
	
	go pipeline.drain(target)

	return pipeline
}
