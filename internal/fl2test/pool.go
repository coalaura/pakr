// Package fl2test exercises the native pool with controlled worker startup.
package fl2test

/*
#cgo CFLAGS: -I${SRCDIR}/../../.native/src/fast-lzma2
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/../../.native/linux-amd64/lib/libfast-lzma2.a
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/../../.native/linux-arm64/lib/libfast-lzma2.a
#cgo windows,amd64 LDFLAGS: ${SRCDIR}/../../.native/windows-amd64/lib/libfast-lzma2.a
#cgo windows,arm64 LDFLAGS: ${SRCDIR}/../../.native/windows-arm64/lib/libfast-lzma2.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libfast-lzma2.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libfast-lzma2.a
#cgo linux LDFLAGS: -lpthread
#include "pool.h"
*/
import "C"

func queuedTimeout() string {
	return C.GoString(C.queued_timeout())
}

func queuedFinalization() string {
	return C.GoString(C.queued_finalization())
}
