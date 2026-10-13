package native

/*
#include <stdlib.h>
#include "parallel.h"
*/
import "C"

import (
	"errors"
	"io"
	"unsafe"
)

type ParallelReader struct {
	reader  unsafe.Pointer
	message [512]C.char
}

func (reader *ParallelReader) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	if reader.reader == nil {
		return 0, errors.New("read from closed parallel reader")
	}

	count := C.pakr_parallel_read(reader.reader, unsafe.Pointer(&data[0]), C.size_t(len(data)), &reader.message[0], C.size_t(len(reader.message)))
	if count < 0 {
		return 0, errors.New(C.GoString(&reader.message[0]))
	}

	if count == 0 {
		return 0, io.EOF
	}

	return int(count), nil
}

func (reader *ParallelReader) Close() error {
	C.pakr_parallel_close(reader.reader)

	reader.reader = nil

	return nil
}

func OpenParallel(path string, format, threads int) (*ParallelReader, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))

	reader := &ParallelReader{}

	reader.reader = C.pakr_parallel_open(name, C.int(format), C.int(threads), &reader.message[0], C.size_t(len(reader.message)))
	if reader.reader == nil {
		return nil, errors.New(C.GoString(&reader.message[0]))
	}

	return reader, nil
}
