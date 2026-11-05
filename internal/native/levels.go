package native

/*
#include "levels.h"
*/
import "C"

const (
	Libdeflate = 8
	SevenZip   = 9
	ISAL       = 10
)

// NativeLevel reports the exact backend parameter selected by the public scale.
// ISA-L returns -1 for levels handled by zlib-ng instead.
func NativeLevel(backend, level int) int {
	return int(C.pakr_level(C.int(backend), C.int(level)))
}
