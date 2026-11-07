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

func NewLZMAWriter(ctx context.Context, target io.Writer, level int, memory uint64) (*Writer, error) {
	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	if level < 1 || level > 9 {
		return nil, errors.New("invalid LZMA-Alone compression level")
	}

	codec := C.pakr_codec_new_lzma(C.int(level), C.uint64_t(membudget.Resolve(memory)))
	if codec == nil {
		return nil, errors.New("native LZMA-Alone allocation failed")
	}

	message := C.GoString(C.pakr_codec_error(codec))
	if message != "" {
		C.pakr_codec_free(codec)

		return nil, errors.New(message)
	}

	return &Writer{ctx: ctx, codec: codec, sizes: new([2]C.size_t{}), target: target, buffer: make([]byte, BufferSize)}, nil
}
