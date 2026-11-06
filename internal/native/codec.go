package native

/*
#cgo CFLAGS: -O3
#cgo linux,amd64 CPPFLAGS: -I${SRCDIR}/../../.native/linux-amd64/include
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/../../.native/linux-amd64/lib
#cgo linux,arm64 CPPFLAGS: -I${SRCDIR}/../../.native/linux-arm64/include
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/../../.native/linux-arm64/lib
#cgo windows,amd64 CPPFLAGS: -I${SRCDIR}/../../.native/windows-amd64/include
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../.native/windows-amd64/lib
#cgo windows,arm64 CPPFLAGS: -I${SRCDIR}/../../.native/windows-arm64/include
#cgo windows,arm64 LDFLAGS: -L${SRCDIR}/../../.native/windows-arm64/lib

// Link Darwin archives directly: Zig rebases -L paths beneath the macOS SDK sysroot.
#cgo darwin,amd64 CPPFLAGS: -I${SRCDIR}/../../.native/darwin-amd64/include
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libpakr_parallel.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libarchive.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libzstd.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/liblzma.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libbz2.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libbzip3.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/liblz4.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libbrotlienc.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libbrotlidec.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libbrotlicommon.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libdeflate.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libisal.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libz.a
#cgo darwin,arm64 CPPFLAGS: -I${SRCDIR}/../../.native/darwin-arm64/include
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libpakr_parallel.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libarchive.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libzstd.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/liblzma.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbz2.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbzip3.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/liblz4.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbrotlienc.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbrotlidec.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbrotlicommon.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libdeflate.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libisal.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libz.a

#cgo CPPFLAGS: -DLIBARCHIVE_STATIC
#cgo linux windows LDFLAGS: -lpakr_parallel -larchive -lzstd -llzma -lbz2 -lbzip3 -llz4 -lbrotlienc -lbrotlidec -lbrotlicommon -ldeflate -lisal -lz
#cgo LDFLAGS: -lm -lstdc++
#cgo linux LDFLAGS: -lpthread
#cgo windows LDFLAGS: -lbcrypt
#include "codec.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"unsafe"
)

const (
	Gzip = 1 + iota
	Zstd
	Bzip2
	XZ
	LZ4
	Brotli
	Deflate
	Lzip       = 11
	BufferSize = 1 << 20
)

type Writer struct {
	ctx    context.Context
	codec  *C.pakr_codec
	sizes  *[2]C.size_t
	target io.Writer
	buffer []byte
	err    error
}

type Reader struct {
	ctx       context.Context
	memory    uint64
	codec     *C.pakr_codec
	sizes     *[2]C.size_t
	source    io.Reader
	buffer    []byte
	input     []byte
	err       error
	format    int
	threads   int
	ended     bool
	sourceErr error
	single    bool
}

func (writer *Writer) parameters() (int, int, bool) {
	return int(C.pakr_codec_parameter(writer.codec, 0)), int(C.pakr_codec_parameter(writer.codec, 1)), C.pakr_codec_parameter(writer.codec, 2) != 0
}

func (writer *Writer) Write(data []byte) (int, error) {
	if writer.err != nil {
		return 0, writer.err
	}

	if writer.codec == nil {
		return 0, errors.New("write to closed codec")
	}

	var written int

	for len(data) > 0 {
		if writer.ctx != nil {
			writer.err = context.Cause(writer.ctx)
			if writer.err != nil {
				return written, writer.err
			}
		}

		chunk := data[:min(len(data), BufferSize)]

		if writer.ctx != nil {
			chunk = chunk[:min(len(chunk), 64<<10)]
		}

		consumed, produced, _, err := step(writer.codec, writer.sizes, chunk, writer.buffer, false)
		written += consumed

		data = data[consumed:]

		if err != nil {
			writer.err = err

			return written, err
		}

		err = writer.flush(produced)
		if err != nil {
			writer.err = err

			return written, err
		}

		if consumed == 0 && produced == 0 {
			writer.err = io.ErrNoProgress

			return written, writer.err
		}
	}

	return written, nil
}

func (writer *Writer) Close() error {
	if writer.codec == nil {
		return writer.err
	}

	defer writer.release()

	return writer.finish()
}

func (writer *Writer) release() {
	C.pakr_codec_free(writer.codec)
	writer.codec = nil
}

func (writer *Writer) finish() error {

	if writer.err != nil {
		return writer.err
	}

	for {
		if writer.ctx != nil {
			writer.err = context.Cause(writer.ctx)
			if writer.err != nil {
				return writer.err
			}
		}

		_, produced, ended, err := step(writer.codec, writer.sizes, nil, writer.buffer, true)
		if err != nil {
			writer.err = err

			return err
		}

		err = writer.flush(produced)
		if err != nil {
			writer.err = err

			return err
		}

		if ended {
			return nil
		}
	}
}

func (writer *Writer) flush(size int) error {
	if size == 0 {
		return nil
	}

	count, err := writer.target.Write(writer.buffer[:size])
	if err == nil && count != size {
		return io.ErrShortWrite
	}

	return err
}

func (reader *Reader) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	if reader.err != nil {
		return 0, reader.err
	}

	if reader.codec == nil {
		return 0, errors.New("read from closed codec")
	}

	for {
		if reader.ctx != nil {
			reader.err = context.Cause(reader.ctx)
			if reader.err != nil {
				return 0, reader.err
			}
		}

		if len(reader.input) == 0 && reader.sourceErr == nil {
			count, err := reader.source.Read(reader.buffer)

			reader.input = reader.buffer[:count]
			reader.sourceErr = err

			if count == 0 && err == nil {
				return 0, nil
			}
		}

		if reader.ended {
			if len(reader.input) == 0 {
				reader.err = reader.sourceErr

				return 0, reader.err
			}

			if reader.single || reader.format == Brotli || reader.format == Deflate || reader.format == XZ {
				reader.err = errors.New("unexpected data after compressed stream")

				return 0, reader.err
			}

			C.pakr_codec_free(reader.codec)

			var (
				codec *C.pakr_codec
				err   error
			)

			if reader.format == Lzip {
				codec, err = newLzipCodec(false, 3, reader.memory, -1)
			} else {
				codec, err = newCodec(reader.format, false, 3, reader.threads, false)
			}

			reader.codec = codec
			reader.err = err
			reader.ended = false

			if err != nil {
				return 0, err
			}
		}

		output := data

		if reader.ctx != nil {
			output = output[:min(len(output), 64<<10)]
		}

		consumed, produced, ended, err := step(reader.codec, reader.sizes, reader.input, output, errors.Is(reader.sourceErr, io.EOF))

		reader.input = reader.input[consumed:]
		reader.ended = ended
		reader.err = err

		if err != nil || produced > 0 {
			return produced, err
		}

		if !ended && consumed == 0 && reader.sourceErr != nil {
			reader.err = reader.sourceErr
			if errors.Is(reader.err, io.EOF) {
				reader.err = io.ErrUnexpectedEOF
			}

			return 0, reader.err
		}
	}
}

func (reader *Reader) Close() error {
	C.pakr_codec_free(reader.codec)

	reader.codec = nil

	return nil
}

func NewWriter(target io.Writer, format, level, threads int) (*Writer, error) {
	return NewWriterExtreme(target, format, level, threads, false)
}

func NewWriterExtreme(target io.Writer, format, level, threads int, extreme bool) (*Writer, error) {
	if format == Lzip && threads > 0 {
		return NewLzipWriter(context.Background(), target, level, 0, -1)
	}

	codec, err := newCodec(format, true, level, threads, extreme)
	if err != nil {
		return nil, err
	}

	bound := int(C.pakr_codec_bound(codec, BufferSize))

	return &Writer{codec: codec, sizes: new([2]C.size_t{}), target: target, buffer: make([]byte, bound)}, nil
}

func NewReader(source io.Reader, format, threads int) (*Reader, error) {
	if format == Lzip && threads > 0 {
		return NewLzipReader(context.Background(), source, 0)
	}

	codec, err := newCodec(format, false, 3, threads, false)
	if err != nil {
		return nil, err
	}

	return &Reader{codec: codec, sizes: new([2]C.size_t{}), source: source, buffer: make([]byte, BufferSize), format: format, threads: threads}, nil
}

func newCodec(format int, encode bool, level, threads int, extreme bool) (*C.pakr_codec, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid codec level or thread count")
	}

	encoding := C.int(0)

	if encode {
		encoding = 1
	}

	extra := C.int(0)

	if extreme {
		extra = 1
	}

	codec := C.pakr_codec_new(C.int(format), encoding, C.int(level), C.int(threads), extra)
	if codec == nil {
		return nil, errors.New("native codec allocation failed")
	}

	message := C.GoString(C.pakr_codec_error(codec))
	if message != "" {
		C.pakr_codec_free(codec)

		return nil, errors.New(message)
	}

	return codec, nil
}

func step(codec *C.pakr_codec, sizes *[2]C.size_t, input, output []byte, finish bool) (int, int, bool, error) {
	// Reuse pointer-free storage: CGO out-parameters otherwise escape on every call.
	sizes[0] = C.size_t(len(input))
	sizes[1] = C.size_t(len(output))

	finishing := C.int(0)

	if finish {
		finishing = 1
	}

	// Explicit slice expressions let CGO check only bytes, including embedded arrays and empty slices.
	status := C.pakr_codec_step(codec, unsafe.Pointer(unsafe.SliceData(input[:])), &sizes[0], unsafe.Pointer(unsafe.SliceData(output[:])), &sizes[1], finishing)
	if status < 0 {
		return int(sizes[0]), 0, false, fmt.Errorf("native codec: %s", C.GoString(C.pakr_codec_error(codec)))
	}

	return int(sizes[0]), int(sizes[1]), status == 1, nil
}
