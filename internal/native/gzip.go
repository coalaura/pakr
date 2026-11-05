package native

/*
#include "gzip.h"
#include <bzlib.h>

static size_t pakr_bzip2_compress(void *input, unsigned int size, void *output, unsigned int capacity, int level) {
	unsigned int count = capacity;
	int status = BZ2_bzBuffToBuffCompress(output, &count, input, size, level, 0, 30);
	return status == BZ_OK ? count : 0;
}
*/
import "C"

import (
	"errors"
	"io"
	"unsafe"
)

type MemberWriter struct {
	target  io.Writer
	slots   []gzipSlot
	used    int
	head    int
	level   int
	format  int
	chunk   int
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
	format int
	level  int
}

func (writer *MemberWriter) Write(data []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("write to closed member stream")
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

		count := min(len(data), writer.chunk-len(slot.input))
		slot.input = append(slot.input, data[:count]...)

		data = data[count:]
		written += count

		if len(slot.input) == writer.chunk {
			writer.submit(slot)

			if writer.used == len(writer.slots) {
				writer.err = writer.flushOne()
				if writer.err != nil {
					return written, writer.err
				}
			}
		}
	}

	return written, nil
}

func (writer *MemberWriter) Close() error {
	if writer.closed {
		return writer.err
	}

	writer.closed = true

	if writer.err == nil {
		slot := &writer.slots[(writer.head+writer.used)%len(writer.slots)]
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
		if slot.work != nil {
			close(slot.work)

			<-slot.done

			C.pakr_gzip_free(slot.codec)
		}
	}

	return writer.err
}

func (writer *MemberWriter) slot() (*gzipSlot, error) {
	slot := &writer.slots[(writer.head+writer.used)%len(writer.slots)]
	if slot.work != nil {
		return slot, nil
	}

	if writer.format == Gzip {
		slot.codec = C.pakr_gzip_new(C.int(writer.level))
		if slot.codec == nil {
			return nil, errors.New("allocate gzip compressor")
		}
	}

	// Each worker owns reusable buffers; only the calling goroutine writes output.
	slot.input = make([]byte, 0, writer.chunk)
	slot.output = make([]byte, writer.chunk+writer.chunk/100+1024)
	slot.format = writer.format
	slot.level = writer.level
	slot.work = make(chan struct{})
	slot.done = make(chan struct{}, 1)

	go slot.run()

	return slot, nil
}

func (writer *MemberWriter) submit(slot *gzipSlot) {
	slot.work <- struct{}{}

	writer.used++
	writer.started = true
}

func (writer *MemberWriter) flush() error {
	for writer.used > 0 {
		writer.err = writer.flushOne()
	}

	return writer.err
}

func (writer *MemberWriter) flushOne() error {
	slot := &writer.slots[writer.head]

	<-slot.done

	err := writer.err
	if err == nil {
		if slot.count == 0 {
			err = errors.New("member compression failed")
		} else {
			var count int

			count, err = writer.target.Write(slot.output[:slot.count])
			if err == nil && count != slot.count {
				err = io.ErrShortWrite
			}
		}
	}

	slot.input = slot.input[:0]
	writer.head = (writer.head + 1) % len(writer.slots)
	writer.used--

	return err
}

func (slot *gzipSlot) run() {
	for range slot.work {
		if slot.format == Bzip2 {
			slot.count = int(C.pakr_bzip2_compress(unsafe.Pointer(unsafe.SliceData(slot.input)), C.uint(len(slot.input)), unsafe.Pointer(unsafe.SliceData(slot.output)), C.uint(len(slot.output)), C.int(slot.level)))
		} else {
			slot.count = int(C.pakr_gzip_compress(slot.codec, unsafe.Pointer(unsafe.SliceData(slot.input)), C.size_t(len(slot.input)), unsafe.Pointer(unsafe.SliceData(slot.output)), C.size_t(len(slot.output))))
		}

		slot.done <- struct{}{}
	}

	slot.done <- struct{}{}
}

func NewGzipWriter(target io.Writer, level, threads int) (*MemberWriter, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid gzip level or thread count")
	}

	// Standard concatenated gzip members permit independent bounded-memory workers.
	return &MemberWriter{target: target, slots: make([]gzipSlot, min(threads, 16)), level: level, format: Gzip, chunk: BufferSize}, nil
}

func NewBzip2Writer(target io.Writer, level, threads int) (*MemberWriter, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid bzip2 level or thread count")
	}

	// Multiple full blocks amortize stream overhead; buffers and libbz2 state stay below 256 MiB.
	chunk := 4 * level * 100000
	workers := min(threads, (256<<20)/(2*chunk+level*800000+1<<20))

	return &MemberWriter{target: target, slots: make([]gzipSlot, max(1, workers)), level: level, format: Bzip2, chunk: chunk}, nil
}
