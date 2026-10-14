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
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/liblz4.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbrotlienc.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbrotlidec.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libbrotlicommon.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libdeflate.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libisal.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libz.a

#cgo CPPFLAGS: -DLIBARCHIVE_STATIC
#cgo linux windows LDFLAGS: -lpakr_parallel -larchive -lzstd -llzma -lbz2 -llz4 -lbrotlienc -lbrotlidec -lbrotlicommon -ldeflate -lisal -lz
#cgo LDFLAGS: -lm -lstdc++
#cgo linux LDFLAGS: -lpthread
#cgo windows LDFLAGS: -lbcrypt
#include "codec.h"
*/
import "C"

import (
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
	BufferSize = 1 << 20
)

type Writer struct {
	codec  *C.pakr_codec
	target io.Writer
	buffer []byte
	err    error
}

type Reader struct {
	codec     *C.pakr_codec
	source    io.Reader
	buffer    []byte
	input     []byte
	err       error
	format    int
	threads   int
	ended     bool
	sourceErr error
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
		chunk := data[:min(len(data), BufferSize)]

		consumed, produced, _, err := step(writer.codec, chunk, writer.buffer, false)
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

	defer C.pakr_codec_free(writer.codec)

	defer func() {
		writer.codec = nil
	}()

	if writer.err != nil {
		return writer.err
	}

	for {
		_, produced, ended, err := step(writer.codec, nil, writer.buffer, true)
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

			if reader.format == Brotli || reader.format == Deflate || reader.format == XZ {
				reader.err = errors.New("unexpected data after compressed stream")

				return 0, reader.err
			}

			C.pakr_codec_free(reader.codec)

			codec, err := newCodec(reader.format, false, 3, reader.threads)

			reader.codec = codec
			reader.err = err
			reader.ended = false

			if err != nil {
				return 0, err
			}
		}

		consumed, produced, ended, err := step(reader.codec, reader.input, data, errors.Is(reader.sourceErr, io.EOF))

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
	codec, err := newCodec(format, true, level, threads)
	if err != nil {
		return nil, err
	}

	return &Writer{codec: codec, target: target, buffer: make([]byte, 2*BufferSize)}, nil
}

func NewReader(source io.Reader, format, threads int) (*Reader, error) {
	codec, err := newCodec(format, false, 3, threads)
	if err != nil {
		return nil, err
	}

	return &Reader{codec: codec, source: source, buffer: make([]byte, BufferSize), format: format, threads: threads}, nil
}

func CompressDeflate(data []byte, level int) ([]byte, error) {
	bound := C.pakr_deflate_bound(C.size_t(len(data)))

	output := make([]byte, int(bound))

	count := C.pakr_deflate(unsafe.Pointer(unsafe.SliceData(data[:])), C.size_t(len(data)), unsafe.Pointer(&output[0]), bound, C.int(level))
	if count == 0 {
		return nil, errors.New("libdeflate compression failed")
	}

	return output[:int(count)], nil
}

func DecompressDeflate(data, output []byte) error {
	result := C.pakr_inflate(unsafe.Pointer(unsafe.SliceData(data[:])), C.size_t(len(data)), unsafe.Pointer(unsafe.SliceData(output[:])), C.size_t(len(output)))
	if result != 0 {
		return errors.New("invalid deflate stream or incorrect uncompressed size")
	}

	return nil
}

func newCodec(format int, encode bool, level, threads int) (*C.pakr_codec, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid codec level or thread count")
	}

	encoding := C.int(0)

	if encode {
		encoding = 1
	}

	codec := C.pakr_codec_new(C.int(format), encoding, C.int(level), C.int(threads))
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

func step(codec *C.pakr_codec, input, output []byte, finish bool) (int, int, bool, error) {
	inputSize := C.size_t(len(input))
	outputSize := C.size_t(len(output))

	finishing := C.int(0)

	if finish {
		finishing = 1
	}

	// Explicit slice expressions let CGO check only bytes, including embedded arrays and empty slices.
	status := C.pakr_codec_step(codec, unsafe.Pointer(unsafe.SliceData(input[:])), &inputSize, unsafe.Pointer(unsafe.SliceData(output[:])), &outputSize, finishing)
	if status < 0 {
		return int(inputSize), 0, false, fmt.Errorf("native codec: %s", C.GoString(C.pakr_codec_error(codec)))
	}

	return int(inputSize), int(outputSize), status == 1, nil
}
