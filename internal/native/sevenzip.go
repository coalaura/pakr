package native

/*
#include "sevenzip.h"
*/
import "C"

import (
	"context"
	"runtime/cgo"

	"github.com/coalaura/pakr/internal/membudget"
)

// Open7ZipWriter retains libarchive's container writer and admits the codec before opening the destination.
func Open7ZipWriter(ctx context.Context, path string, level, threads int, memory uint64) (*Archive, error) {
	return openArchive(ctx, path, "7z", true, level, threads, membudget.Resolve(memory))
}

//export pakrArchiveCancelled
func pakrArchiveCancelled(handle C.uintptr_t) C.int {
	archive := cgo.Handle(handle).Value().(*Archive)
	if archive.err == nil {
		archive.err = context.Cause(archive.ctx)
	}

	if archive.err != nil {
		return 1
	}

	return 0
}
