package native

/*
#include "seekable.h"
*/
import "C"

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"unsafe"
)

const (
	seekableMagic   = 0x8f92eab1
	seekTableMagic  = 0x184d2a5e
	seekFrameSize   = 8 << 20
	seekBufferLimit = 16 << 20
)

type SeekableWriter struct {
	target     io.Writer
	table      *os.File
	frame      *Writer
	compressed uint32
	decoded    uint32
	frames     uint32
	level      int
	threads    int
	err        error
	closed     bool
}

type frameCounter struct {
	owner *SeekableWriter
}

type seekFrame struct {
	offset     int64
	compressed uint32
	decoded    uint32
	checksum   uint32
}

type seekWorker struct {
	jobs   chan seekFrame
	pipe   *io.PipeReader
	output *io.PipeWriter
}

type SeekableReader struct {
	source    io.ReaderAt
	table     *bufio.Reader
	workers   []seekWorker
	pending   sync.WaitGroup
	frames    uint32
	scheduled uint32
	finished  uint32
	offset    int64
	checksums bool
	err       error
	closed    bool
}

func (counter frameCounter) Write(data []byte) (int, error) {
	count, err := counter.owner.target.Write(data)
	counter.owner.compressed += uint32(count)

	return count, err
}

func (writer *SeekableWriter) Write(data []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("write to closed seekable stream")
	}

	written := 0

	for len(data) > 0 && writer.err == nil {
		if writer.frame == nil {
			writer.frame, writer.err = NewWriter(frameCounter{owner: writer}, Zstd, writer.level, writer.threads)
			if writer.err != nil {
				break
			}
		}

		count := min(len(data), seekFrameSize-int(writer.decoded))
		count, writer.err = writer.frame.Write(data[:count])
		writer.decoded += uint32(count)
		written += count
		data = data[count:]

		if writer.err == nil && writer.decoded == seekFrameSize {
			writer.err = writer.finishFrame()
		}
	}

	return written, writer.err
}

func (writer *SeekableWriter) Close() error {
	if writer.closed {
		return writer.err
	}

	writer.closed = true

	defer os.Remove(writer.table.Name())
	defer writer.table.Close()

	if writer.err == nil && (writer.frame != nil || writer.frames == 0) {
		if writer.frame == nil {
			writer.frame, writer.err = NewWriter(frameCounter{owner: writer}, Zstd, writer.level, writer.threads)
		}

		if writer.err == nil {
			writer.err = writer.finishFrame()
		}
	}

	if writer.frame != nil {
		writer.err = errors.Join(writer.err, writer.frame.Close())
	}

	if writer.err != nil {
		return writer.err
	}

	var header [8]byte

	binary.LittleEndian.PutUint32(header[:4], seekTableMagic)
	binary.LittleEndian.PutUint32(header[4:], writer.frames*8+9)

	writer.err = writeBytes(writer.target, header[:])
	if writer.err == nil {
		_, writer.err = writer.table.Seek(0, io.SeekStart)
	}

	if writer.err == nil {
		_, writer.err = io.CopyN(writer.target, writer.table, int64(writer.frames)*8)
	}

	var footer [9]byte

	binary.LittleEndian.PutUint32(footer[:4], writer.frames)
	binary.LittleEndian.PutUint32(footer[5:], seekableMagic)

	if writer.err == nil {
		writer.err = writeBytes(writer.target, footer[:])
	}

	return writer.err
}

func (writer *SeekableWriter) finishFrame() error {
	err := writer.frame.Close()
	writer.frame = nil

	if err != nil {
		return err
	}

	if writer.frames >= (math.MaxUint32-9)/8 {
		return errors.New("seek table exceeds the format's 32-bit size limit")
	}

	var entry [8]byte

	binary.LittleEndian.PutUint32(entry[:4], writer.compressed)
	binary.LittleEndian.PutUint32(entry[4:], writer.decoded)

	err = writeBytes(writer.table, entry[:])

	writer.compressed = 0
	writer.decoded = 0
	writer.frames++

	return err
}

func (reader *SeekableReader) Read(data []byte) (int, error) {
	if reader.closed {
		return 0, errors.New("read from closed seekable stream")
	}

	for reader.finished < reader.frames && reader.err == nil {
		worker := &reader.workers[int(reader.finished)%len(reader.workers)]

		count, err := worker.pipe.Read(data)
		if errors.Is(err, io.EOF) {
			worker.pipe.Close()
			worker.pipe = nil

			reader.finished++

			if reader.scheduled < reader.frames {
				reader.err = reader.schedule(worker)
			}

			if count > 0 {
				return count, reader.err
			}
		} else {
			reader.err = err

			return count, err
		}
	}

	if reader.err != nil {
		return 0, reader.err
	}

	return 0, io.EOF
}

func (reader *SeekableReader) Close() error {
	if reader.closed {
		return nil
	}

	reader.closed = true

	for index := range reader.workers {
		worker := &reader.workers[index]
		if worker.pipe != nil {
			worker.pipe.Close()
		}

		close(worker.jobs)
	}

	reader.pending.Wait()

	return nil
}

func (reader *SeekableReader) schedule(worker *seekWorker) error {
	frame, err := readSeekFrame(reader.table, reader.checksums)
	if err != nil {
		return err
	}

	frame.offset = reader.offset
	reader.offset += int64(frame.compressed)

	worker.pipe, worker.output = io.Pipe()
	worker.jobs <- frame

	reader.scheduled++

	return nil
}

func (reader *SeekableReader) run(worker *seekWorker) {
	var (
		compressed []byte
		decoded    []byte
	)

	buffer := make([]byte, BufferSize)

	for frame := range worker.jobs {
		output := worker.output

		var err error

		if frame.compressed <= seekBufferLimit && frame.decoded <= seekBufferLimit {
			compressed = growBytes(compressed, int(frame.compressed))
			decoded = growBytes(decoded, int(frame.decoded))

			_, err = reader.source.ReadAt(compressed, frame.offset)
			if err == nil {
				status := C.pakr_zstd_frame(unsafe.Pointer(unsafe.SliceData(compressed)), C.size_t(len(compressed)), unsafe.Pointer(unsafe.SliceData(decoded)), C.size_t(len(decoded)))
				if status != 0 {
					err = errors.New("invalid seekable zstd frame or size")
				}
			}

			if err == nil {
				err = checkSeekHash(decoded, frame.checksum, reader.checksums)
			}

			if err == nil {
				err = writeBytes(output, decoded)
			}
		} else {
			err = reader.streamFrame(frame, output, buffer)
		}

		output.CloseWithError(err)
	}
}

func (reader *SeekableReader) streamFrame(frame seekFrame, target io.Writer, buffer []byte) error {
	codec, err := NewReader(io.NewSectionReader(reader.source, frame.offset, int64(frame.compressed)), Zstd, 1)
	if err != nil {
		return err
	}

	defer codec.Close()

	codec.single = true

	state := C.pakr_hash_new()
	if state == nil {
		return errors.New("allocate seekable checksum state")
	}

	defer C.pakr_hash_free(state)

	var total uint64

	for {
		count, readErr := codec.Read(buffer)

		total += uint64(count)
		if total > uint64(frame.decoded) {
			return errors.New("seekable frame exceeds declared size")
		}

		if count > 0 {
			C.pakr_hash_update(state, unsafe.Pointer(unsafe.SliceData(buffer)), C.size_t(count))

			err = writeBytes(target, buffer[:count])
			if err != nil {
				return err
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return readErr
		}
	}

	if total != uint64(frame.decoded) || reader.checksums && uint32(C.pakr_hash_digest(state)) != frame.checksum {
		return errors.New("seekable frame size or checksum mismatch")
	}

	return nil
}

func NewSeekableWriter(target io.Writer, level, threads int) (*SeekableWriter, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid seekable level or thread count")
	}

	// Only the index is spooled: memory stays bounded even for multi-terabyte streams.
	table, err := os.CreateTemp("", "pakr-seek-table-*")
	if err != nil {
		return nil, err
	}

	return &SeekableWriter{target: target, table: table, level: level, threads: threads}, nil
}

// OpenSeekable returns nil, nil when the input is an ordinary Zstandard stream.
func OpenSeekable(source io.ReaderAt, size int64, threads int) (*SeekableReader, error) {
	if size < 9 {
		return nil, nil
	}

	var footer [9]byte

	_, err := source.ReadAt(footer[:], size-9)
	if err != nil {
		return nil, err
	}

	if binary.LittleEndian.Uint32(footer[5:]) != seekableMagic {
		return nil, nil
	}

	if footer[4]&0x7c != 0 {
		return nil, errors.New("reserved seek table descriptor bits are set")
	}

	checksums := footer[4]&0x80 != 0
	entrySize := int64(8)

	if checksums {
		entrySize = 12
	}

	frames := binary.LittleEndian.Uint32(footer[:4])

	tableSize := int64(frames)*entrySize + 9
	start := size - tableSize - 8

	if start < 0 || tableSize > math.MaxUint32 {
		return nil, errors.New("invalid seek table size")
	}

	var header [8]byte

	_, err = source.ReadAt(header[:], start)
	if err != nil {
		return nil, err
	}

	if binary.LittleEndian.Uint32(header[:4]) != seekTableMagic || int64(binary.LittleEndian.Uint32(header[4:])) != tableSize {
		return nil, errors.New("invalid seek table header")
	}

	table := bufio.NewReader(io.NewSectionReader(source, start+8, tableSize-9))

	var offset int64

	for range frames {
		frame, err := readSeekFrame(table, checksums)
		if err != nil {
			return nil, err
		}

		offset += int64(frame.compressed)
		if frame.compressed == 0 || offset > start {
			return nil, errors.New("invalid seekable frame bounds")
		}

		if frame.compressed > seekBufferLimit || frame.decoded > seekBufferLimit {
			threads = 1
		}
	}

	if offset != start {
		return nil, errors.New("seek table does not cover the compressed stream")
	}

	table.Reset(io.NewSectionReader(source, start+8, tableSize-9))

	reader := &SeekableReader{source: source, table: table, frames: frames, checksums: checksums, workers: make([]seekWorker, min(max(1, threads), 8, int(frames)))}

	for index := range reader.workers {
		worker := &reader.workers[index]
		worker.jobs = make(chan seekFrame)

		reader.pending.Go(func() {
			reader.run(worker)
		})

		err = reader.schedule(worker)
		if err != nil {
			// Close only workers which have been initialized.
			reader.workers = reader.workers[:index+1]
			reader.Close()

			return nil, err
		}
	}

	return reader, nil
}

func readSeekFrame(source io.Reader, checksums bool) (seekFrame, error) {
	var data [12]byte

	size := 8

	if checksums {
		size = 12
	}

	_, err := io.ReadFull(source, data[:size])
	return seekFrame{compressed: binary.LittleEndian.Uint32(data[:4]), decoded: binary.LittleEndian.Uint32(data[4:8]), checksum: binary.LittleEndian.Uint32(data[8:])}, err
}

func checkSeekHash(data []byte, checksum uint32, enabled bool) error {
	if !enabled {
		return nil
	}

	state := C.pakr_hash_new()
	if state == nil {
		return errors.New("allocate seekable checksum state")
	}

	defer C.pakr_hash_free(state)

	C.pakr_hash_update(state, unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)))

	if uint32(C.pakr_hash_digest(state)) != checksum {
		return errors.New("seekable checksum mismatch")
	}

	return nil
}

func growBytes(buffer []byte, size int) []byte {
	if cap(buffer) < size {
		return make([]byte, size)
	}

	return buffer[:size]
}

func writeBytes(target io.Writer, data []byte) error {
	count, err := target.Write(data)
	if err == nil && count != len(data) {
		return io.ErrShortWrite
	}

	return err
}
