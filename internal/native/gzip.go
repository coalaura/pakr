package native

/*
#include "gzip.h"
*/
import "C"

import (
	"errors"
	"io"
	"unsafe"
)

type GzipWriter struct {
	target  io.Writer
	slots   []gzipSlot
	used    int
	level   int
	err     error
	started bool
	closed  bool
}

type gzipSlot struct {
	codec  *C.pakr_gzip
	input  []byte
	output []byte
	work   chan struct{}
	done   chan struct{}
	count  int
}

func (writer *GzipWriter) Write(data []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("write to closed gzip stream")
	}

	if writer.err != nil {
		return 0, writer.err
	}

	var written int

	for len(data) > 0 {
		slot, err := writer.slot()
		if err != nil {
			writer.err = err

			return written, err
		}

		count := min(len(data), BufferSize-len(slot.input))
		slot.input = append(slot.input, data[:count]...)

		data = data[count:]
		written += count

		if len(slot.input) == BufferSize {
			writer.submit(slot)

			if writer.used == len(writer.slots) {
				writer.err = writer.flush()
				if writer.err != nil {
					return written, writer.err
				}
			}
		}
	}

	return written, nil
}

func (writer *GzipWriter) Close() error {
	if writer.closed {
		return writer.err
	}

	writer.closed = true

	if writer.err == nil {
		slot := &writer.slots[writer.used]
		if len(slot.input) > 0 || !writer.started {
			slot, writer.err = writer.slot()
			if writer.err == nil {
				writer.submit(slot)
			}
		}
	}

	flushErr := writer.flush()

	if writer.err == nil {
		writer.err = flushErr
	}

	for index := range writer.slots {
		slot := &writer.slots[index]
		if slot.codec != nil {
			close(slot.work)

			<-slot.done

			C.pakr_gzip_free(slot.codec)
		}
	}

	return writer.err
}

func (writer *GzipWriter) slot() (*gzipSlot, error) {
	slot := &writer.slots[writer.used]
	if slot.codec != nil {
		return slot, nil
	}

	slot.codec = C.pakr_gzip_new(C.int(writer.level))
	if slot.codec == nil {
		return nil, errors.New("allocate gzip compressor")
	}

	// Each worker owns reusable buffers; only the calling goroutine writes output.
	slot.input = make([]byte, 0, BufferSize)
	slot.output = make([]byte, 2*BufferSize)
	slot.work = make(chan struct{})
	slot.done = make(chan struct{}, 1)

	go slot.run()

	return slot, nil
}

func (writer *GzipWriter) submit(slot *gzipSlot) {
	slot.work <- struct{}{}

	writer.used++
	writer.started = true
}

func (writer *GzipWriter) flush() error {
	err := writer.err

	for index := 0; index < writer.used; index++ {
		slot := &writer.slots[index]

		<-slot.done

		if err == nil {
			if slot.count == 0 {
				err = errors.New("gzip compression failed")
			} else {
				var count int

				count, err = writer.target.Write(slot.output[:slot.count])
				if err == nil && count != slot.count {
					err = io.ErrShortWrite
				}
			}
		}

		slot.input = slot.input[:0]
	}

	writer.used = 0

	return err
}

func (slot *gzipSlot) run() {
	for range slot.work {
		slot.count = int(C.pakr_gzip_compress(slot.codec, unsafe.Pointer(unsafe.SliceData(slot.input)), C.size_t(len(slot.input)), unsafe.Pointer(unsafe.SliceData(slot.output)), C.size_t(len(slot.output))))
		slot.done <- struct{}{}
	}

	slot.done <- struct{}{}
}

func NewGzipWriter(target io.Writer, level, threads int) (*GzipWriter, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid gzip level or thread count")
	}

	// Standard concatenated gzip members permit independent bounded-memory workers.
	return &GzipWriter{target: target, slots: make([]gzipSlot, min(threads, 16)), level: level}, nil
}
