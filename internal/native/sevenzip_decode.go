package native

/*
#include "sevenzip_decode.h"
*/
import "C"

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"runtime/cgo"
	"unsafe"

	"github.com/coalaura/pakr/internal/membudget"
)

const sevenzipSegmentLimit = 32 << 20

type sevenzipReader struct {
	file    *os.File
	threads int
	memory  uint64
}

func (archive *Archive) enable7zipDecoder(path string, threads int, memory uint64) C.int {
	if threads == 1 {
		return C.archive_read_support_format_7zip(archive.archive)
	}

	file, err := os.Open(path)
	if err != nil {
		archive.err = err

		return C.ARCHIVE_FATAL
	}

	archive.sevenzip = &sevenzipReader{file: file, threads: threads, memory: memory}
	archive.handle = cgo.NewHandle(archive)
	archive.decoder = C.pakr_7z_decoder_new(C.uintptr_t(archive.handle))

	if archive.decoder == nil {
		archive.err = errors.New("allocate 7z decoder")

		return C.ARCHIVE_FATAL
	}

	return C.pakr_archive_read_support_7zip_decoder(archive.archive, unsafe.Pointer(archive.decoder), (*[0]byte)(C.pakr_7z_decoder_start), (*[0]byte)(C.pakr_7z_decoder_step), (*[0]byte)(C.pakr_7z_decoder_stop), (*[0]byte)(C.pakr_7z_decoder_error))
}

func Open7ZipReader(ctx context.Context, path string, threads int, memory uint64) (*Archive, error) {
	return openArchive(ctx, path, "7z", false, 3, threads, membudget.Resolve(memory))
}

//export pakr7zWorkers
func pakr7zWorkers(handle C.uintptr_t, offset C.int64_t, packed, unpacked C.uint64_t, property C.uchar, limit *C.uint64_t) C.uint {
	archive := cgo.Handle(handle).Value().(*Archive)
	reader := archive.sevenzip

	if property > 39 {
		return 0
	}

	dictionary := uint64(2|property&1) << (property/2 + 11)
	segments, largest := sevenzipSegments(archive.ctx, reader.file, int64(offset), uint64(packed), uint64(unpacked))
	decoderMemory := uint64(C.pakr_7z_decoder_memory(property))
	workers, capacity := sevenzipWorkers(reader.threads, reader.memory, segments, largest, dictionary, decoderMemory)
	*limit = C.uint64_t(capacity)

	return C.uint(workers)
}

func sevenzipWorkers(threads int, memory uint64, segments int, largest, dictionary, decoderMemory uint64) (int, uint64) {
	if decoderMemory == math.MaxUint64 {
		return 0, 0
	}

	workers := min(threads, segments, 8)

	for workers >= 2 {
		// The FL2 cap covers queued input and output. Reserve liblzma state and
		// dictionaries per worker, stacks, overhead and the fallback dictionary.
		reserve := uint64(16+8*workers)<<20 + dictionary + uint64(workers)*decoderMemory
		if memory > reserve {
			capacity := min(memory-reserve, 128<<20)
			minimum := max(uint64(workers)*largest+8<<20, (dictionary+dictionary/6)*2)

			if capacity >= minimum {
				return workers, capacity
			}
		}

		workers--
	}

	return 0, 0
}

// The scan is an acceleration hint, not a second archive parser. It skips chunk
// payloads and rejects long dependency chains before allocating segment buffers.
// Libarchive and the selected codec still validate the stream and its checksums.
func sevenzipSegments(ctx context.Context, source io.ReaderAt, offset int64, packed, unpacked uint64) (int, uint64) {
	if offset < 0 || packed > uint64(math.MaxInt64-offset) || unpacked < 1<<20 {
		return 0, 0
	}

	var (
		header        [6]byte
		position      uint64
		total         uint64
		segmentInput  uint64
		segmentOutput uint64
		largest       uint64
		compressed    uint64
	)

	segments := 0
	needsProperties := true

	for chunks := 0; position < packed && chunks < 1<<20; chunks++ {
		if ctx.Err() != nil {
			return 0, 0
		}

		count := min(uint64(len(header)), packed-position)
		read, _ := source.ReadAt(header[:count], offset+int64(position))

		if uint64(read) != count {
			return 0, 0
		}

		control := header[0]
		if control == 0 {
			if position+1 != packed || total != unpacked || compressed < 1<<20 {
				return 0, 0
			}

			return segments, max(largest, segmentInput+segmentOutput)
		}

		reset := control == 1 || control >= 0xe0
		if reset {
			largest = max(largest, segmentInput+segmentOutput)
			segmentInput = 0
			segmentOutput = 0
			segments++
			needsProperties = true
		} else if segments == 0 {
			return 0, 0
		}

		if count < 3 || (control > 2 && control < 0x80) {
			return 0, 0
		}

		headerSize := uint64(3)
		output := uint64(header[1])<<8 | uint64(header[2])
		output++
		input := output

		if control >= 0x80 {
			// A dictionary reset alone need not reset LZMA properties. Such a
			// segment cannot start a fresh decoder until properties are supplied.
			if needsProperties && control < 0xc0 {
				return 0, 0
			}

			needsProperties = false
			headerSize = 5

			if control >= 0xc0 {
				headerSize++
			}

			if count < headerSize {
				return 0, 0
			}

			output += uint64(control&31) << 16
			input = (uint64(header[3])<<8 | uint64(header[4])) + 1
			compressed += input
		}

		input += headerSize
		if input > packed-position || output > unpacked-total {
			return 0, 0
		}

		segmentInput += input
		segmentOutput += output

		if segmentInput > sevenzipSegmentLimit || segmentOutput > sevenzipSegmentLimit {
			return 0, 0
		}

		position += input
		total += output
	}

	return 0, 0
}
