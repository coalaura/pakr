package native

/*
#cgo linux windows LDFLAGS: -lwim
#cgo windows LDFLAGS: -lntdll
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/../../.native/darwin-amd64/lib/libwim.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../.native/darwin-arm64/lib/libwim.a
#include "wim.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

const (
	WIMXpress = 1
	WIMLZX    = 2
	WIMLZMS   = 3
)

type WIMEntry struct {
	Path       string
	Name       string
	Size       uint64
	Group      uint64
	Modified   time.Time
	Mode       uint32
	Attributes uint32
	Reparse    uint32
	Hash       [20]byte
}

type WIMSource struct {
	Path   string
	Target string
}

type WIM struct {
	Progress func(uint64, int)
	Scan     func(string) (bool, error)
	ctx      context.Context
	wim      *C.WIMStruct
	handle   cgo.Handle
	err      error
	entry    func(WIMEntry) error
	blob     func([20]byte, uint64, []byte, int) error
}

var (
	wimGate      = make(chan struct{}, 1)
	wimInit      sync.Once
	wimInitError error
)

func (session *WIM) Close() {
	if session.wim != nil {
		C.pakr_wim_free(session.wim)
	}

	session.handle.Delete()
	<-wimGate
}

func (session *WIM) Create(codec, level int) error {
	return session.result(C.pakr_wim_create(C.int(codec), C.uint(level), &session.wim, C.uintptr_t(session.handle)))
}

func (session *WIM) Open(path string) error {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))

	return session.result(C.pakr_wim_open(name, &session.wim, C.uintptr_t(session.handle)))
}

func (session *WIM) Images() int {
	return int(C.pakr_wim_images(session.wim))
}

func (session *WIM) Capture(sources []WIMSource) error {
	native := make([]C.pakr_wim_source, len(sources))

	for index := range sources {
		native[index].source = C.CString(sources[index].Path)
		native[index].target = C.CString(sources[index].Target)
	}

	defer func() {
		for index := range native {
			C.free(unsafe.Pointer(native[index].source))
			C.free(unsafe.Pointer(native[index].target))
		}
	}()

	return session.result(C.pakr_wim_capture(session.wim, unsafe.SliceData(native), C.size_t(len(native))))
}

func (session *WIM) Write(path string, solid bool, threads int) error {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))

	flag := C.int(0)

	if solid {
		flag = 1
	}

	return session.result(C.pakr_wim_write(session.wim, name, flag, C.uint(threads)))
}

func (session *WIM) Tree(image int, metadataLimit uint64, visit func(WIMEntry) error) error {
	session.entry = visit

	defer func() {
		session.entry = nil
	}()

	return session.result(C.pakr_wim_tree(session.wim, C.int(image), C.uint64_t(metadataLimit), C.uintptr_t(session.handle)))
}

func (session *WIM) Read(hashes [][20]byte, visit func([20]byte, uint64, []byte, int) error) error {
	session.blob = visit

	defer func() {
		session.blob = nil
	}()

	return session.result(C.pakr_wim_read(session.wim, (*C.uchar)(unsafe.Pointer(unsafe.SliceData(hashes))), C.size_t(len(hashes)), C.uintptr_t(session.handle)))
}

func (session *WIM) result(status C.int) error {
	if session.err != nil {
		return session.err
	}

	err := session.ctx.Err()
	if err != nil {
		return err
	}

	return wimError(status)
}

func NewWIM(ctx context.Context) (*WIM, error) {
	// Compression defaults are process-global in wimlib. Hold this gate through
	// the whole job; wimlib still runs its own compressor threads within a job.
	select {
	case wimGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	wimInit.Do(func() {
		wimInitError = wimError(C.pakr_wim_init())
	})

	if wimInitError != nil {
		<-wimGate

		return nil, wimInitError
	}

	session := &WIM{ctx: ctx}
	session.handle = cgo.NewHandle(session)

	return session, nil
}

func WIMMemory(codec, chunk, level int) uint64 {
	return uint64(C.pakr_wim_memory(C.int(codec), C.uint(chunk), C.uint(level)))
}

//export goWIMProgress
func goWIMProgress(handle C.uintptr_t, completed C.uint64_t, threads C.uint, path *C.char, scan C.int) C.int {
	session := cgo.Handle(handle).Value().(*WIM)
	if session.err != nil || session.ctx.Err() != nil {
		return 1
	}

	if scan != 0 && session.Scan != nil {
		var exclude bool

		exclude, session.err = session.Scan(C.GoString(path))
		if session.err != nil {
			return 1
		}

		if exclude {
			return 2
		}
	}

	if threads != 0 && session.Progress != nil {
		session.Progress(uint64(completed), int(threads))
	}

	return 0
}

//export goWIMUnsupported
func goWIMUnsupported(handle C.uintptr_t, path *C.char, reason *C.char) {
	session := cgo.Handle(handle).Value().(*WIM)
	session.err = fmt.Errorf("unsupported WIM metadata (%s): %s", C.GoString(reason), C.GoString(path))
}

//export goWIMEntry
func goWIMEntry(handle C.uintptr_t, entry *C.pakr_wim_entry) C.int {
	session := cgo.Handle(handle).Value().(*WIM)
	if session.err != nil || session.ctx.Err() != nil {
		return 1
	}

	value := WIMEntry{
		Path:       C.GoString(entry.path),
		Name:       C.GoString(entry.name),
		Size:       uint64(entry.size),
		Group:      uint64(entry.group),
		Modified:   time.Unix(int64(entry.modified), int64(entry.nanos)),
		Mode:       uint32(entry.mode),
		Attributes: uint32(entry.attributes),
		Reparse:    uint32(entry.reparse),
		Hash:       *(*[20]byte)(unsafe.Pointer(&entry.hash)),
	}

	session.err = session.entry(value)
	if session.err != nil {
		return 1
	}

	return 0
}

//export goWIMBlob
func goWIMBlob(handle C.uintptr_t, hash *C.uchar, offset C.uint64_t, data unsafe.Pointer, size C.size_t, event C.int) C.int {
	session := cgo.Handle(handle).Value().(*WIM)
	if session.err != nil || session.ctx.Err() != nil {
		return 1
	}

	// The callback consumes this borrowed native buffer synchronously.
	buffer := unsafe.Slice((*byte)(data), int(size))

	session.err = session.blob(*(*[20]byte)(unsafe.Pointer(hash)), uint64(offset), buffer, int(event))
	if session.err != nil {
		return 1
	}

	return 0
}

func wimError(status C.int) error {
	if status == 0 {
		return nil
	}

	message := C.pakr_wim_error(status)
	defer C.free(unsafe.Pointer(message))

	return fmt.Errorf("wimlib: %s (code %d)", C.GoString(message), int(status))
}
