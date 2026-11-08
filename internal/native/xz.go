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

type XZSettings struct {
	Backend    string
	Preset     int
	Dictionary int
	Workers    int
	Memory     uint64
}

func (writer *Writer) XZSettings() XZSettings {
	if writer.codec == nil {
		return XZSettings{}
	}

	backend := "liblzma"

	if C.pakr_codec_parameter(writer.codec, 3) != 0 {
		backend = "Fast LZMA2"
	}

	return XZSettings{
		Backend:    backend,
		Preset:     int(C.pakr_codec_parameter(writer.codec, 5)),
		Dictionary: int(C.pakr_codec_parameter(writer.codec, 4)),
		Workers:    int(C.pakr_codec_parameter(writer.codec, 1)),
		Memory:     uint64(C.pakr_codec_memory(writer.codec)),
	}
}

func NewXZWriter(ctx context.Context, target io.Writer, level, threads int, extreme bool, memory uint64) (*Writer, error) {
	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	if level < 1 || level > 9 || threads < 1 || threads > 200 {
		return nil, errors.New("XZ worker count must be between 1 and 200 and level between 1 and 9")
	}

	extra := C.int(0)

	if extreme {
		extra = 1
	}

	codec := C.pakr_codec_new_xz(C.int(level), C.int(threads), extra, C.uint64_t(membudget.Resolve(memory)))
	if codec == nil {
		return nil, errors.New("native XZ allocation failed")
	}

	message := C.GoString(C.pakr_codec_error(codec))
	if message != "" {
		C.pakr_codec_free(codec)

		return nil, errors.New(message)
	}

	err = context.Cause(ctx)
	if err != nil {
		C.pakr_codec_free(codec)

		return nil, err
	}

	return &Writer{ctx: ctx, codec: codec, sizes: new([2]C.size_t{}), target: target, buffer: make([]byte, BufferSize)}, nil
}
