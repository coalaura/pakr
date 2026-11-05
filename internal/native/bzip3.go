package native

/*
#include <libbz3.h>

static int32_t pakr_bz3_step(struct bz3_state *state, uint8_t *buffer, size_t capacity, int32_t size, int32_t original, int encode) {
	int32_t result = encode ? bz3_encode_block(state, buffer, original) : bz3_decode_block(state, buffer, capacity, size, original);
	return bz3_last_error(state) == BZ3_OK ? result : -1;
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"unsafe"
)

const (
	Bzip3              = 8
	DefaultBzip3Memory = 2 << 30
	bzip3Minimum       = 65 << 10
	bzip3Maximum       = 511 << 20
	bzip3Reserve       = 1 << 20
)

type Bzip3Writer struct {
	blocks  *bzip3Blocks
	target  io.Writer
	err     error
	started bool
	closed  bool
}

type Bzip3Reader struct {
	blocks   *bzip3Blocks
	source   io.Reader
	err      error
	inputErr error
	offset   int
	ready    bool
	closed   bool
}

type bzip3Blocks struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	slots  []bzip3Slot
	wait   sync.WaitGroup
	block  int
	bound  int
	head   int
	used   int
	encode bool
	closed bool
}

type bzip3Slot struct {
	state    *C.struct_bz3_state
	buffer   []byte
	work     chan struct{}
	done     chan struct{}
	original int
	size     int
	err      error
}

var bzip3Sizes = [...]int{1 << 20, 4 << 20, 16 << 20, 32 << 20, 64 << 20, 128 << 20, 256 << 20, 384 << 20, bzip3Maximum}

func (writer *Bzip3Writer) Write(data []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("write to closed bzip3 stream")
	}

	if writer.err != nil {
		return 0, writer.err
	}

	blocks := writer.blocks
	written := 0

	for len(data) > 0 {
		err := context.Cause(blocks.ctx)
		if err != nil {
			return written, writer.fail(err)
		}

		if blocks.used == len(blocks.slots) {
			err = writer.flushOne()
			if err != nil {
				return written, writer.fail(err)
			}
		}

		slot := &blocks.slots[(blocks.head+blocks.used)%len(blocks.slots)]
		if slot.buffer == nil {
			capacity := blocks.bound

			if !writer.started {
				capacity = bzip3Minimum
			}

			slot.buffer = make([]byte, capacity)
		}

		if slot.original == len(slot.buffer) {
			buffer := make([]byte, blocks.bound)
			copy(buffer, slot.buffer)
			slot.buffer = buffer
		}

		count := min(len(data), blocks.block-slot.original, len(slot.buffer)-slot.original)
		copy(slot.buffer[slot.original:], data[:count])
		slot.original += count
		written += count
		data = data[count:]

		if slot.original == blocks.block {
			err = writer.start(false)
			if err == nil {
				err = blocks.submit(slot)
			}

			if err != nil {
				return written, writer.fail(err)
			}
		}
	}

	return written, nil
}

func (writer *Bzip3Writer) Close() error {
	if writer.closed {
		return writer.err
	}

	writer.closed = true
	blocks := writer.blocks
	defer blocks.close()

	if writer.err != nil {
		return writer.err
	}

	writer.err = context.Cause(blocks.ctx)
	if writer.err != nil {
		return writer.err
	}

	writer.err = writer.start(true)
	if writer.err != nil {
		return writer.err
	}

	if blocks.used < len(blocks.slots) {
		slot := &blocks.slots[(blocks.head+blocks.used)%len(blocks.slots)]
		if slot.original > 0 {
			writer.err = blocks.submit(slot)
		}
	}

	for blocks.used > 0 && writer.err == nil {
		writer.err = writer.flushOne()
	}

	return writer.err
}

func (writer *Bzip3Writer) start(final bool) error {
	if writer.started {
		return nil
	}

	blocks := writer.blocks
	if final && blocks.slots[0].original <= bzip3Minimum {
		// Delay the header until the first full block or Close. Tiny streams need
		// only the minimum decoder state, independently of Write chunking.
		blocks.block = bzip3Minimum
		blocks.bound = int(C.bz3_bound(C.size_t(blocks.block)))
		blocks.slots = blocks.slots[:1]
	}

	var header [9]byte

	copy(header[:], "BZ3v1")
	binary.LittleEndian.PutUint32(header[5:], uint32(blocks.block))

	writer.started = true

	return bzip3Write(writer.target, header[:])
}

func (writer *Bzip3Writer) flushOne() error {
	blocks := writer.blocks

	slot, err := blocks.first()
	if err != nil {
		return err
	}

	var header [8]byte

	binary.LittleEndian.PutUint32(header[:], uint32(slot.size))
	binary.LittleEndian.PutUint32(header[4:], uint32(slot.original))

	err = bzip3Write(writer.target, header[:])
	if err == nil {
		err = bzip3Write(writer.target, slot.buffer[:slot.size])
	}

	blocks.release()

	return err
}

func (writer *Bzip3Writer) fail(err error) error {
	writer.err = err
	writer.blocks.cancel(err)
	writer.blocks.close()

	return err
}

func (reader *Bzip3Reader) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	if reader.err != nil {
		return 0, reader.err
	}

	if reader.closed {
		return 0, errors.New("read from closed bzip3 stream")
	}

	blocks := reader.blocks

	for {
		err := context.Cause(blocks.ctx)
		if err != nil {
			return 0, reader.fail(err)
		}

		if reader.ready {
			slot := &blocks.slots[blocks.head]
			count := copy(data, slot.buffer[reader.offset:slot.original])

			reader.offset += count
			if reader.offset == slot.original {
				reader.ready = false
				reader.offset = 0
				blocks.release()
			}

			if count > 0 {
				return count, nil
			}
		}

		for blocks.used < len(blocks.slots) && reader.inputErr == nil {
			reader.inputErr = reader.fill()
			if reader.inputErr != nil && !errors.Is(reader.inputErr, io.EOF) {
				return 0, reader.fail(reader.inputErr)
			}
		}

		if blocks.used == 0 {
			return 0, reader.fail(reader.inputErr)
		}

		_, err = blocks.first()
		if err != nil {
			return 0, reader.fail(err)
		}

		reader.ready = true
	}
}

func (reader *Bzip3Reader) Close() error {
	if !reader.closed {
		reader.closed = true
		reader.blocks.close()
	}

	if errors.Is(reader.err, io.EOF) {
		return nil
	}

	return reader.err
}

func (reader *Bzip3Reader) fill() error {
	blocks := reader.blocks

	err := context.Cause(blocks.ctx)
	if err != nil {
		return err
	}

	var header [8]byte

	_, err = io.ReadFull(reader.source, header[:])
	if err != nil {
		return err
	}

	size := int(binary.LittleEndian.Uint32(header[:]))
	original := int(binary.LittleEndian.Uint32(header[4:]))

	if size < 8 || size > blocks.bound || original > blocks.block {
		return errors.New("invalid bzip3 block lengths")
	}

	slot := &blocks.slots[(blocks.head+blocks.used)%len(blocks.slots)]
	if slot.buffer == nil {
		slot.buffer = make([]byte, blocks.bound)
	}

	_, err = io.ReadFull(reader.source, slot.buffer[:size])
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}

		return fmt.Errorf("truncated bzip3 block: %w", err)
	}

	slot.size = size
	slot.original = original

	err = bzip3Validate(slot.buffer[:size], original, blocks.bound)
	if err != nil {
		return err
	}

	return blocks.submit(slot)
}

func (reader *Bzip3Reader) fail(err error) error {
	reader.err = err
	reader.blocks.close()

	return err
}

func (blocks *bzip3Blocks) submit(slot *bzip3Slot) error {
	err := context.Cause(blocks.ctx)
	if err != nil {
		return err
	}

	if len(slot.buffer) < blocks.bound {
		buffer := make([]byte, blocks.bound)

		copy(buffer, slot.buffer[:slot.original])
		slot.buffer = buffer
	}

	if slot.state == nil {
		slot.state = C.bz3_new(C.int32_t(blocks.block))
		if slot.state == nil {
			return errors.New("allocate bzip3 native state")
		}

		if len(blocks.slots) > 1 {
			slot.work = make(chan struct{})
			slot.done = make(chan struct{}, 1)

			blocks.wait.Add(1)

			go blocks.run(slot)
		}
	}

	blocks.used++

	if slot.work == nil {
		blocks.process(slot)
	} else {
		slot.work <- struct{}{}
	}

	return nil
}

func (blocks *bzip3Blocks) first() (*bzip3Slot, error) {
	slot := &blocks.slots[blocks.head]
	if slot.done != nil {
		select {
		case <-slot.done:
		case <-blocks.ctx.Done():
			return nil, context.Cause(blocks.ctx)
		}
	}

	err := context.Cause(blocks.ctx)
	if err != nil {
		return nil, err
	}

	return slot, slot.err
}

func (blocks *bzip3Blocks) release() {
	slot := &blocks.slots[blocks.head]
	slot.original = 0
	slot.size = 0

	blocks.head = (blocks.head + 1) % len(blocks.slots)
	blocks.used--
}

func (blocks *bzip3Blocks) run(slot *bzip3Slot) {
	defer blocks.wait.Done()

	for range slot.work {
		blocks.process(slot)
		slot.done <- struct{}{}
	}
}

func (blocks *bzip3Blocks) process(slot *bzip3Slot) {
	slot.err = context.Cause(blocks.ctx)
	if slot.err != nil {
		return
	}

	encode := 0

	if blocks.encode {
		encode = 1
	}

	result := int(C.pakr_bz3_step(slot.state, (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(slot.buffer))), C.size_t(len(slot.buffer)), C.int32_t(slot.size), C.int32_t(slot.original), C.int(encode)))
	if result < 0 {
		slot.err = fmt.Errorf("bzip3: %s", C.GoString(C.bz3_strerror(slot.state)))
	} else if blocks.encode {
		slot.size = result
	} else if result != slot.original {
		slot.err = errors.New("bzip3 decoded length mismatch")
	}

	if slot.err != nil {
		blocks.cancel(slot.err)
	}
}

func (blocks *bzip3Blocks) close() {
	if blocks.closed {
		return
	}

	blocks.closed = true
	blocks.cancel(context.Canceled)

	for index := range blocks.slots {
		if blocks.slots[index].work != nil {
			close(blocks.slots[index].work)
		}
	}

	// Native block calls cannot be interrupted. Join before freeing their state.
	blocks.wait.Wait()

	for index := range blocks.slots {
		slot := &blocks.slots[index]
		if slot.state != nil {
			C.bz3_free(slot.state)
			slot.state = nil
		}

		slot.buffer = nil
	}
}

func NewBzip3Writer(ctx context.Context, target io.Writer, level, threads int, memory uint64) (*Bzip3Writer, error) {
	if level < 1 || level > len(bzip3Sizes) {
		return nil, errors.New("bzip3 level must be from 1 to 9")
	}

	blocks, err := newBzip3Blocks(ctx, bzip3Sizes[level-1], threads, memory, true)
	if err != nil {
		return nil, err
	}

	return &Bzip3Writer{blocks: blocks, target: target}, nil
}

func NewBzip3Reader(ctx context.Context, source io.Reader, threads int, memory uint64) (*Bzip3Reader, error) {
	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	var header [9]byte

	_, err = io.ReadFull(source, header[:])
	if err != nil {
		return nil, fmt.Errorf("bzip3 file header: %w", err)
	}

	if string(header[:5]) != "BZ3v1" {
		return nil, errors.New("invalid or unsupported bzip3 signature")
	}

	block := int(binary.LittleEndian.Uint32(header[5:]))
	if block == bzip3Minimum {
		threads = min(threads, 1)
	}

	blocks, err := newBzip3Blocks(ctx, block, threads, memory, false)
	if err != nil {
		return nil, err
	}

	return &Bzip3Reader{blocks: blocks, source: source}, nil
}

func newBzip3Blocks(ctx context.Context, block, threads int, memory uint64, encode bool) (*bzip3Blocks, error) {
	if block < bzip3Minimum || block > bzip3Maximum || threads < 1 || threads > 256 {
		return nil, errors.New("invalid bzip3 block size or worker count")
	}

	if memory == 0 {
		memory = DefaultBzip3Memory
	}

	bound := int(C.bz3_bound(C.size_t(block)))

	// Each slot is both input and ordered output: no separate result queue.
	// Include upstream's worst-case libsais scratch (2*block), plus overhead.
	perWorker := uint64(C.bz3_min_memory_needed(C.int32_t(block))) + uint64(bound) + 2*uint64(block) + bzip3Reserve
	if memory < perWorker+bzip3Reserve {
		return nil, fmt.Errorf("bzip3 %d-byte blocks require at least %d bytes of codec memory; budget is %d (raise --memory or choose a lower level)", block, perWorker+bzip3Reserve, memory)
	}

	workers := min(uint64(threads), (memory-bzip3Reserve)/perWorker)

	ctx, cancel := context.WithCancelCause(ctx)

	return &bzip3Blocks{ctx: ctx, cancel: cancel, slots: make([]bzip3Slot, int(workers)), block: block, bound: bound, encode: encode}, nil
}

func bzip3Validate(data []byte, original, bound int) error {
	index := int32(binary.LittleEndian.Uint32(data[4:]))
	if index == -1 {
		if len(data) != original+8 || original > 64 {
			return errors.New("invalid bzip3 literal block")
		}

		return nil
	}

	if len(data) < 13 || index < 0 || data[8]&^byte(6) != 0 {
		return errors.New("invalid bzip3 block header")
	}

	position := 9
	bwtSize := original

	for bit := byte(2); bit <= 4; bit <<= 1 {
		if data[8]&bit == 0 {
			continue
		}

		if len(data) < position+8 {
			return errors.New("truncated bzip3 transform header")
		}

		size := int(binary.LittleEndian.Uint32(data[position:]))
		if size > bound {
			return errors.New("invalid bzip3 transform length")
		}

		if bit == 2 || data[8]&2 == 0 {
			bwtSize = size
		}

		position += 4
	}

	if int(index) > bwtSize {
		return errors.New("invalid bzip3 BWT index")
	}

	return nil
}

func bzip3Write(target io.Writer, data []byte) error {
	count, err := target.Write(data)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}

	return err
}
