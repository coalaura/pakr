package native

/*
#include <stdlib.h>
#include <libbsc/libbsc.h>
#include <libbsc/filters.h>
#include "bsc.h"
*/
import "C"

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"
	"unsafe"

	"github.com/coalaura/pakr/internal/membudget"
)

const (
	BSC           = 12
	bscHeaderSize = 28
	bscMaximum    = 2047 << 20
	bscReserve    = 16 << 20
	bscFeatures   = C.LIBBSC_FEATURE_FASTMODE
)

type BSCWriter struct {
	blocks *bscBlocks
	target io.WriteSeeker
	start  int64
	offset int64
	count  uint32
	err    error
	closed bool
}

type BSCReader struct {
	blocks *bscBlocks
	index  []bscEntry
	size   int64
	next   int
	offset int
	err    error
	ready  bool
	closed bool
}

type bscSettings struct {
	block    int
	packed   int
	coder    int
	hash     int
	match    int
	reorder  bool
	contexts bool
}

type bscEntry struct {
	input    int64
	output   int64
	packed   int32
	original int32
	record   byte
	contexts byte
}

type bscSlot struct {
	workspace *C.pakr_bsc_workspace
	input     []byte
	output    []byte
	entry     bscEntry
	work      chan struct{}
	done      chan struct{}
	err       error
	last      bool
}

type bscBlocks struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	source   io.ReaderAt
	slots    []bscSlot
	settings bscSettings
	wait     sync.WaitGroup
	head     int
	used     int
	closed   bool
}

var (
	bscLevels = [...]bscSettings{
		{block: 1 << 20, coder: 3, hash: 15, match: 72},
		{block: 4 << 20, coder: 3, hash: 15, match: 72},
		{block: 8 << 20, coder: 1, hash: 15, match: 72},
		{block: 16 << 20, coder: 1, hash: 16, match: 72},
		{block: 25 << 20, coder: 1, hash: 16, match: 72},
		{block: 32 << 20, coder: 2, hash: 17, match: 72},
		{block: 48 << 20, coder: 2, hash: 18, match: 72},
		{block: 64 << 20, coder: 2, hash: 18, match: 72, contexts: true},
		{block: 64 << 20, coder: 2, hash: 18, match: 128, reorder: true, contexts: true},
	}

	bscInitialize = sync.OnceValue(func() error {
		return bscError("initialize", int(C.pakr_bsc_init(bscFeatures)))
	})

	bscMemoryBudget = membudget.Resolve
)

func (writer *BSCWriter) Write(data []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("write to closed bsc stream")
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
		original := int(slot.entry.original)

		if len(slot.input) == original {
			capacity := blocks.settings.block

			if slot.input == nil {
				capacity = min(capacity, 64<<10)
			}

			buffer := make([]byte, capacity)
			copy(buffer, slot.input)
			slot.input = buffer
		}

		count := copy(slot.input[original:], data)
		slot.entry.original += int32(count)
		written += count
		data = data[count:]

		if int(slot.entry.original) == blocks.settings.block {
			err = blocks.submit(slot)
			if err != nil {
				return written, writer.fail(err)
			}
		}
	}

	return written, nil
}

func (writer *BSCWriter) Close() error {
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

	if blocks.used < len(blocks.slots) {
		slot := &blocks.slots[(blocks.head+blocks.used)%len(blocks.slots)]
		if slot.entry.original > 0 {
			writer.err = blocks.submit(slot)
		}
	}

	for blocks.used > 0 && writer.err == nil {
		writer.err = writer.flushOne()
	}

	if writer.err == nil {
		writer.err = writer.finish()
	}

	return writer.err
}

func (writer *BSCWriter) finish() error {
	end, err := writer.target.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	_, err = writer.target.Seek(writer.start+4, io.SeekStart)
	if err != nil {
		return err
	}

	var count [4]byte

	binary.LittleEndian.PutUint32(count[:], writer.count)

	err = bscWrite(writer.target, count[:])
	if err != nil {
		return err
	}

	_, err = writer.target.Seek(end, io.SeekStart)
	return err
}

func (writer *BSCWriter) flushOne() error {
	blocks := writer.blocks

	slot, err := blocks.first()
	if err != nil {
		return err
	}

	if writer.count == math.MaxInt32 || writer.offset > math.MaxInt64-int64(slot.entry.original) {
		return errors.New("bsc stream exceeds format limits")
	}

	var header [10]byte

	binary.LittleEndian.PutUint64(header[:], uint64(writer.offset))
	header[8] = slot.entry.record
	header[9] = slot.entry.contexts

	err = bscWrite(writer.target, header[:])
	if err == nil {
		err = bscWrite(writer.target, slot.output[:slot.entry.packed])
	}

	writer.offset += int64(slot.entry.original)
	writer.count++

	blocks.release()

	return err
}

func (writer *BSCWriter) fail(err error) error {
	writer.err = err
	writer.blocks.cancel(err)
	writer.blocks.close()

	return err
}

func (reader *BSCReader) Size() int64 {
	return reader.size
}

func (reader *BSCReader) Read(data []byte) (int, error) {
	if reader.err != nil {
		return 0, reader.err
	}

	if reader.closed {
		return 0, errors.New("read from closed bsc stream")
	}

	if len(data) == 0 {
		return 0, nil
	}

	blocks := reader.blocks

	for {
		err := context.Cause(blocks.ctx)
		if err != nil {
			return 0, reader.fail(err)
		}

		if reader.ready {
			slot := &blocks.slots[blocks.head]
			count := copy(data, slot.output[reader.offset:slot.entry.original])

			reader.offset += count
			if reader.offset == int(slot.entry.original) {
				reader.ready = false
				reader.offset = 0

				blocks.release()
			}

			return count, nil
		}

		for blocks.used < len(blocks.slots) && reader.next < len(reader.index) {
			slot := &blocks.slots[(blocks.head+blocks.used)%len(blocks.slots)]
			slot.entry = reader.index[reader.next]
			slot.last = reader.next+len(blocks.slots) >= len(reader.index)

			reader.next++

			err = blocks.submit(slot)
			if err != nil {
				return 0, reader.fail(err)
			}
		}

		if blocks.used == 0 {
			return 0, reader.fail(io.EOF)
		}

		_, err = blocks.first()
		if err != nil {
			return 0, reader.fail(err)
		}

		reader.ready = true
	}
}

func (reader *BSCReader) Close() error {
	if !reader.closed {
		reader.closed = true
		reader.blocks.close()
		reader.index = nil
	}

	if errors.Is(reader.err, io.EOF) {
		return nil
	}

	return reader.err
}

func (reader *BSCReader) fail(err error) error {
	reader.err = err
	reader.blocks.close()
	reader.index = nil

	return err
}

func (blocks *bscBlocks) submit(slot *bscSlot) error {
	err := context.Cause(blocks.ctx)
	if err != nil {
		return err
	}

	if slot.work == nil && len(blocks.slots) > 1 {
		slot.work = make(chan struct{})
		slot.done = make(chan struct{}, 1)
		blocks.wait.Add(1)
		go blocks.run(slot)
	}

	blocks.used++

	if slot.work == nil {
		blocks.process(slot)
	} else {
		slot.work <- struct{}{}
	}

	return nil
}

func (blocks *bscBlocks) first() (*bscSlot, error) {
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

func (blocks *bscBlocks) release() {
	blocks.slots[blocks.head].entry = bscEntry{}
	blocks.head = (blocks.head + 1) % len(blocks.slots)
	blocks.used--
}

func (blocks *bscBlocks) run(slot *bscSlot) {
	defer blocks.wait.Done()

	for range slot.work {
		blocks.process(slot)
		slot.done <- struct{}{}
	}
}

func (blocks *bscBlocks) process(slot *bscSlot) {
	slot.err = context.Cause(blocks.ctx)
	if slot.err == nil && slot.workspace == nil && !slot.last {
		// Leave room for both Go buffers, growth overlap and preprocessing scratch
		// alongside retained native allocations within the existing worker budget.
		scratch := uint64(blocks.settings.block)*5 + bscReserve/2 + (uint64(4) << blocks.settings.hash)

		slot.workspace = C.pakr_bsc_create(C.size_t(scratch))
		if slot.workspace == nil {
			slot.err = bscError("workspace", C.LIBBSC_NOT_ENOUGH_MEMORY)
		}
	}

	if slot.err == nil {
		if blocks.source == nil {
			slot.err = blocks.compress(slot)
		} else {
			slot.err = blocks.decompress(slot)
		}
	}

	if slot.err != nil {
		blocks.cancel(slot.err)
	}

	if slot.last {
		C.pakr_bsc_destroy(slot.workspace)
		slot.workspace = nil
	}
}

func (blocks *bscBlocks) compress(slot *bscSlot) error {
	original := int(slot.entry.original)
	if len(slot.output) < original+bscHeaderSize {
		slot.output = make([]byte, original+bscHeaderSize)
	}

	settings := blocks.settings

	slot.entry.record = 1
	slot.entry.contexts = 1

	input := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(slot.input)))
	output := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(slot.output)))

	if settings.reorder {
		record := int(C.bsc_detect_recordsize(input, C.int(original), bscFeatures))
		if record < 1 || record > 127 {
			return fmt.Errorf("bsc record detection: %d", record)
		}

		slot.entry.record = byte(record)

		result := int(C.bsc_reorder_forward(input, C.int(original), C.int(record), bscFeatures))
		if result < 0 {
			return bscError("reorder", result)
		}
	}

	if settings.contexts {
		order := int(C.bsc_detect_contextsorder(input, C.int(original), bscFeatures))
		if order != 1 && order != 2 {
			return fmt.Errorf("bsc context detection: %d", order)
		}

		slot.entry.contexts = byte(order)

		if order == 2 {
			C.bsc_reverse_block(input, C.int(original), bscFeatures)
		}
	}

	// The separate input is preserved even when compression destroys its output.
	// Stored preprocessed data retains metadata so the reader reverses it too.
	result := int(C.pakr_bsc_compress(slot.workspace, input, output, C.int(original), C.int(settings.hash), C.int(settings.match), C.int(settings.coder), bscFeatures))
	if result == C.LIBBSC_NOT_COMPRESSIBLE {
		result = int(C.bsc_store(input, output, C.int(original), bscFeatures))
	}

	if result < bscHeaderSize || result > original+bscHeaderSize {
		return fmt.Errorf("bsc compress: invalid result %d", result)
	}

	slot.entry.packed = int32(result)

	return nil
}

func (blocks *bscBlocks) decompress(slot *bscSlot) error {
	if slot.input == nil {
		// Fixed slot capacities avoid retaining successively larger Go allocations.
		slot.input = make([]byte, blocks.settings.packed)
		slot.output = make([]byte, blocks.settings.block)
	}

	data := slot.input[:slot.entry.packed]

	_, err := blocks.source.ReadAt(data, slot.entry.input)
	if err != nil {
		return fmt.Errorf("bsc block payload: %w", err)
	}

	packed, original, hash, err := bscBlockInfo(data)
	if err != nil {
		return err
	}

	if packed != int(slot.entry.packed) || original != int(slot.entry.original) || hash > blocks.settings.hash {
		return errors.New("bsc block changed after indexing")
	}

	input := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(data)))
	output := (*C.uchar)(unsafe.Pointer(unsafe.SliceData(slot.output)))

	result := int(C.pakr_bsc_decompress(slot.workspace, input, C.int(packed), output, C.int(original), bscFeatures))
	if result != 0 {
		return bscError("decompress", result)
	}

	if slot.entry.contexts == 2 {
		C.bsc_reverse_block(output, C.int(original), bscFeatures)
	}

	if slot.entry.record > 1 {
		result = int(C.bsc_reorder_reverse(output, C.int(original), C.int(slot.entry.record), bscFeatures))
		if result != 0 {
			return bscError("reverse reordering", result)
		}
	}

	return nil
}

func (blocks *bscBlocks) close() {
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

	// Native calls finish before their worker-local buffers are released.
	blocks.wait.Wait()

	for index := range blocks.slots {
		C.pakr_bsc_destroy(blocks.slots[index].workspace)
	}

	blocks.slots = nil
}

func NewBSCWriter(ctx context.Context, target io.WriteSeeker, level, threads int, memory uint64) (*BSCWriter, error) {
	if level < 1 || level > len(bscLevels) {
		return nil, errors.New("bsc level must be from 1 to 9")
	}

	settings := bscLevels[level-1]

	blocks, err := newBSCBlocks(ctx, settings, threads, bscMemoryBudget(memory), 0)
	if err != nil {
		return nil, err
	}

	start, err := target.Seek(0, io.SeekCurrent)
	if err == nil {
		err = bscWrite(target, []byte{'b', 's', 'c', '1', 0, 0, 0, 0})
	}

	if err != nil {
		blocks.close()

		return nil, err
	}

	return &BSCWriter{blocks: blocks, target: target, start: start}, nil
}

func NewBSCReader(ctx context.Context, source io.ReaderAt, size int64, threads int, memory uint64) (*BSCReader, error) {
	if threads < 1 || threads > 256 {
		return nil, errors.New("invalid bsc worker count")
	}

	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	err = bscInitialize()
	if err != nil {
		return nil, err
	}

	var header [8]byte

	_, err = source.ReadAt(header[:], 0)
	if err != nil {
		return nil, fmt.Errorf("bsc file header: %w", err)
	}

	count := int64(binary.LittleEndian.Uint32(header[4:]))
	if string(header[:4]) != "bsc1" || count > math.MaxInt32 || size < 8 || count > (size-8)/39 {
		return nil, errors.New("invalid bsc signature or block count")
	}

	memory = bscMemoryBudget(memory)
	indexMemory := uint64(count) * uint64(unsafe.Sizeof(bscEntry{}))

	if memory < 2*bscReserve+12 || indexMemory > memory-2*bscReserve-12 {
		return nil, errors.New("bsc block index exceeds --memory budget")
	}

	index := make([]bscEntry, int(count))
	position := int64(8)
	settings := bscSettings{}

	var blockHeader [10 + bscHeaderSize]byte

	for number := range index {
		err = context.Cause(ctx)
		if err != nil {
			return nil, err
		}

		_, err = source.ReadAt(blockHeader[:], position)
		if err != nil {
			return nil, fmt.Errorf("bsc block header: %w", err)
		}

		packed, original, hash, infoErr := bscBlockInfo(blockHeader[10:])
		if infoErr != nil {
			return nil, infoErr
		}

		output := binary.LittleEndian.Uint64(blockHeader[:])
		record := blockHeader[8]
		contexts := blockHeader[9]

		if output > math.MaxInt64-uint64(original) || record < 1 || record > 127 || (contexts != 1 && contexts != 2) {
			return nil, errors.New("invalid bsc output offset or preprocessing metadata")
		}

		if position > size-10-int64(packed) {
			return nil, errors.New("truncated bsc block payload")
		}

		index[number] = bscEntry{
			input:    position + 10,
			output:   int64(output),
			packed:   int32(packed),
			original: int32(original),
			record:   record,
			contexts: contexts,
		}

		position += 10 + int64(packed)

		settings.block = max(settings.block, original)
		settings.packed = max(settings.packed, packed)
		settings.hash = max(settings.hash, hash)
	}

	if position != size {
		return nil, errors.New("bsc block count does not match file length")
	}

	slices.SortFunc(index, func(first, second bscEntry) int {
		if first.output < second.output {
			return -1
		}

		if first.output > second.output {
			return 1
		}

		return 0
	})

	decoded := int64(0)

	for number := range index {
		entry := &index[number]
		if entry.output != decoded {
			return nil, errors.New("missing, overlapping or duplicate bsc output region")
		}

		decoded += int64(entry.original)
	}

	threads = min(threads, max(1, len(index)))

	blocks, err := newBSCBlocks(ctx, settings, threads, memory, indexMemory)
	if err != nil {
		return nil, err
	}

	blocks.source = source

	return &BSCReader{blocks: blocks, index: index, size: decoded}, nil
}

func newBSCBlocks(ctx context.Context, settings bscSettings, threads int, memory, indexMemory uint64) (*bscBlocks, error) {
	if threads < 1 || threads > 256 {
		return nil, errors.New("invalid bsc worker count")
	}

	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	// Include native sorting scratch, both Go buffers, growth overlap, model and
	// filter scratch, the declared LZP table, and the complete block index.
	perWorker := 8*uint64(settings.block) + bscReserve + (uint64(4) << settings.hash)
	reserve := uint64(bscReserve) + indexMemory

	if memory < reserve || memory-reserve < perWorker {
		return nil, fmt.Errorf("bsc %d-byte blocks require at least %d bytes of codec memory; budget is %d (raise --memory or choose a lower level)", settings.block, reserve+perWorker, memory)
	}

	err = bscInitialize()
	if err != nil {
		return nil, err
	}

	workers := min(uint64(threads), (memory-reserve)/perWorker)
	ctx, cancel := context.WithCancelCause(ctx)

	return &bscBlocks{ctx: ctx, cancel: cancel, slots: make([]bscSlot, int(workers)), settings: settings}, nil
}

func bscBlockInfo(header []byte) (int, int, int, error) {
	if len(header) < bscHeaderSize {
		return 0, 0, 0, errors.New("truncated bsc native header")
	}

	packed := int(binary.LittleEndian.Uint32(header))
	original := int(binary.LittleEndian.Uint32(header[4:]))
	mode := binary.LittleEndian.Uint32(header[8:])

	if original < 1 || original > bscMaximum || packed < bscHeaderSize || packed > original+bscHeaderSize {
		return 0, 0, 0, errors.New("invalid bsc block lengths")
	}

	if mode == 0 && packed != original+bscHeaderSize || mode != 0 && (mode&31 == 0 || (mode>>5)&7 == 0 || packed <= bscHeaderSize+1) {
		return 0, 0, 0, errors.New("invalid bsc stored block or transform mode")
	}

	result := int(C.bsc_block_info((*C.uchar)(unsafe.Pointer(unsafe.SliceData(header))), bscHeaderSize, nil, nil, bscFeatures))
	if result != 0 {
		return 0, 0, 0, bscError("block header", result)
	}

	return packed, original, int((mode >> 16) & 255), nil
}

func bscError(operation string, result int) error {
	if result == 0 {
		return nil
	}

	return fmt.Errorf("bsc %s failed (libbsc error %d)", operation, result)
}

func bscWrite(target io.Writer, data []byte) error {
	count, err := target.Write(data)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}

	return err
}
