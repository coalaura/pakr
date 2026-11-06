package native

/*
#include "codec.h"
*/
import "C"

import (
	"context"
	"errors"
	"io"

	"github.com/coalaura/pakr/internal/membudget"
)

// NewLzipWriter uses size only to avoid dictionary space that the input cannot reference.
// A negative size means unknown; the input is always streamed as one member.
func NewLzipWriter(ctx context.Context, target io.Writer, level int, memory uint64, size int64) (*Writer, error) {
	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	codec, err := newLzipCodec(true, level, membudget.Resolve(memory), size)
	if err != nil {
		return nil, err
	}

	return &Writer{ctx: ctx, codec: codec, sizes: new([2]C.size_t{}), target: target, buffer: make([]byte, BufferSize)}, nil
}

func NewLzipReader(ctx context.Context, source io.Reader, memory uint64) (*Reader, error) {
	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	memory = membudget.Resolve(memory)

	codec, err := newLzipCodec(false, 3, memory, -1)
	if err != nil {
		return nil, err
	}

	return &Reader{ctx: ctx, memory: memory, codec: codec, sizes: new([2]C.size_t{}), source: source, buffer: make([]byte, BufferSize), format: Lzip, threads: 1}, nil
}

func newLzipCodec(encode bool, level int, memory uint64, size int64) (*C.pakr_codec, error) {
	if level < 1 || level > 9 {
		return nil, errors.New("invalid lzip compression level")
	}

	encoding := C.int(0)

	if encode {
		encoding = 1
	}

	codec := C.pakr_codec_new_lzip(encoding, C.int(level), C.uint64_t(memory), C.int64_t(size))
	if codec == nil {
		return nil, errors.New("native lzip allocation failed")
	}

	message := C.GoString(C.pakr_codec_error(codec))
	if message != "" {
		C.pakr_codec_free(codec)

		return nil, errors.New(message)
	}

	return codec, nil
}
