package native

/*
#include <libdeflate.h>
#include "levels.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Deflater owns a reusable worker-local libdeflate context. It is not concurrent-safe.
type Deflater struct {
	codec *C.struct_libdeflate_compressor
}

type Inflater struct {
	codec *C.struct_libdeflate_decompressor
}

func (codec *Deflater) Compress(output, data []byte) ([]byte, error) {
	bound := int(C.libdeflate_deflate_compress_bound(codec.codec, C.size_t(len(data))))
	if cap(output) < bound {
		output = make([]byte, bound)
	} else {
		output = output[:bound]
	}

	count := C.libdeflate_deflate_compress(codec.codec, unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)), unsafe.Pointer(unsafe.SliceData(output)), C.size_t(bound))
	if count == 0 {
		return nil, errors.New("libdeflate compression failed")
	}

	return output[:int(count)], nil
}

func (codec *Deflater) Close() {
	C.libdeflate_free_compressor(codec.codec)
	codec.codec = nil
}

func (codec *Inflater) Decompress(data, output []byte) error {
	var consumed C.size_t

	result := C.libdeflate_deflate_decompress_ex(codec.codec, unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)), unsafe.Pointer(unsafe.SliceData(output)), C.size_t(len(output)), &consumed, nil)
	if result != C.LIBDEFLATE_SUCCESS || int(consumed) != len(data) {
		return errors.New("invalid deflate stream or incorrect size")
	}

	return nil
}

func (codec *Inflater) Close() {
	C.libdeflate_free_decompressor(codec.codec)
	codec.codec = nil
}

func NewDeflater(level int) (*Deflater, error) {
	if level < 1 || level > 9 {
		return nil, errors.New("invalid deflate level")
	}

	codec := C.libdeflate_alloc_compressor(C.pakr_level(Libdeflate, C.int(level)))
	if codec == nil {
		return nil, errors.New("allocate libdeflate compressor")
	}

	return &Deflater{codec: codec}, nil
}

func NewInflater() (*Inflater, error) {
	codec := C.libdeflate_alloc_decompressor()
	if codec == nil {
		return nil, errors.New("allocate libdeflate decompressor")
	}

	return &Inflater{codec: codec}, nil
}

func CompressDeflate(data []byte, level int) ([]byte, error) {
	codec, err := NewDeflater(level)
	if err != nil {
		return nil, err
	}

	defer codec.Close()

	return codec.Compress(nil, data)
}

func DecompressDeflate(data, output []byte) error {
	codec, err := NewInflater()
	if err != nil {
		return err
	}

	defer codec.Close()

	return codec.Decompress(data, output)
}
