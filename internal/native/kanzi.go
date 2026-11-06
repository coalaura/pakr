package native

/*
#include "kanzi.h"
*/
import "C"

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/cgo"
	"strings"
	"sync"
	"unsafe"

	"github.com/coalaura/pakr/internal/membudget"
)

const (
	Kanzi            = 13
	kanziMiB         = uint64(1 << 20)
	kanziFixedMemory = 2 * kanziMiB
)

type kanziLevel struct {
	block     int
	transform string
	entropy   string
}

type KanziSettings struct {
	BlockSize int
	Jobs      int
	Memory    uint64
}

type kanziIO struct {
	mutex  sync.Mutex
	ctx    context.Context
	source io.Reader
	target io.Writer
	err    error
	read   uint64
}

type KanziStream struct {
	Settings KanziSettings
	stream   *C.pakr_knz
	handle   cgo.Handle
	callback *kanziIO
	size     int64
	total    int64
	err      error
	encode   bool
}

var kanziLevels = [...]kanziLevel{
	{},
	{block: 4 << 20, transform: "LZX", entropy: "NONE"},
	{block: 4 << 20, transform: "DNA+LZ", entropy: "HUFFMAN"},
	{block: 4 << 20, transform: "TEXT+UTF+PACK+MM+LZX", entropy: "HUFFMAN"},
	{block: 4 << 20, transform: "TEXT+UTF+EXE+PACK+MM+ROLZ", entropy: "NONE"},
	{block: 4 << 20, transform: "TEXT+UTF+BWT+RANK+ZRLT", entropy: "ANS0"},
	{block: 8 << 20, transform: "TEXT+UTF+BWT+SRT+ZRLT", entropy: "FPAQ"},
	{block: 16 << 20, transform: "LZP+TEXT+UTF+BWT+LZP", entropy: "CM"},
	{block: 16 << 20, transform: "EXE+RLT+TEXT+UTF+DNA", entropy: "TPAQ"},
	{block: 32 << 20, transform: "EXE+RLT+TEXT+UTF+DNA", entropy: "TPAQX"},
}

func (state *kanziIO) failure() error {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.err != nil {
		return state.err
	}

	return state.ctx.Err()
}

func (stream *KanziStream) Write(data []byte) (int, error) {
	if stream.err != nil {
		return 0, stream.err
	}

	if stream.stream == nil || !stream.encode {
		return 0, errors.New("kanzi writer is closed")
	}

	if len(data) == 0 {
		return 0, nil
	}

	if stream.size >= 0 && int64(len(data)) > stream.size-stream.total {
		stream.err = errors.New("kanzi input exceeds declared size")
		return 0, stream.err
	}

	stream.err = stream.callback.failure()
	if stream.err != nil {
		return 0, stream.err
	}

	var message [256]C.char

	result := C.pakr_knz_write(stream.stream, unsafe.Pointer(&data[0]), C.size_t(len(data)), &message[0])

	stream.err = stream.nativeError(result, &message[0])
	if stream.err != nil {
		return 0, stream.err
	}

	stream.total += int64(len(data))

	return len(data), nil
}

func (stream *KanziStream) Read(data []byte) (int, error) {
	if stream.err != nil {
		return 0, stream.err
	}

	if stream.stream == nil || stream.encode {
		return 0, errors.New("kanzi reader is closed")
	}

	if len(data) == 0 {
		return 0, nil
	}

	stream.err = stream.callback.failure()
	if stream.err != nil {
		return 0, stream.err
	}

	var message [256]C.char

	count := C.pakr_knz_read(stream.stream, unsafe.Pointer(&data[0]), C.size_t(len(data)), &message[0])
	if count < 0 {
		stream.err = stream.nativeError(-1, &message[0])
		return 0, stream.err
	}

	stream.total += int64(count)
	if stream.size >= 0 && stream.total > stream.size {
		stream.err = errors.New("kanzi output exceeds declared size")
		return 0, stream.err
	}

	if count == 0 {
		stream.err = stream.finishRead()
		return 0, stream.err
	}

	return int(count), nil
}

func (stream *KanziStream) Close() error {
	if stream.stream == nil {
		return kanziCloseError(stream.err)
	}

	var message [256]C.char

	result := C.pakr_knz_close(stream.stream, &message[0])
	failure := stream.nativeError(result, &message[0])

	stream.stream = nil
	stream.handle.Delete()

	if stream.err == nil {
		stream.err = failure
	}

	if stream.err == nil && stream.encode && stream.size >= 0 && stream.total != stream.size {
		stream.err = errors.New("kanzi input does not match declared size")
	}

	return kanziCloseError(stream.err)
}

func (stream *KanziStream) nativeError(result C.int, message *C.char) error {
	err := stream.callback.failure()
	if err != nil {
		return err
	}

	if result < 0 {
		return fmt.Errorf("kanzi: %s", C.GoString(message))
	}

	return nil
}

func (stream *KanziStream) finishRead() error {
	err := stream.callback.failure()
	if err != nil {
		return err
	}

	if stream.size >= 0 && stream.total != stream.size {
		return errors.New("kanzi output does not match declared size")
	}

	consumed := uint64(C.pakr_knz_consumed(stream.stream))
	state := stream.callback

	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.read != consumed {
		return errors.New("trailing data after Kanzi stream")
	}

	var probe [1]byte

	count, err := io.ReadFull(state.source, probe[:])
	if count != 0 {
		return errors.New("trailing data after Kanzi stream")
	}

	if !errors.Is(err, io.EOF) {
		return err
	}

	return io.EOF
}

func NewKanziWriter(ctx context.Context, target io.Writer, level, threads int, memory uint64, size int64) (*KanziStream, error) {
	if level < 1 || level > 9 {
		return nil, errors.New("kanzi level must be between 1 and 9")
	}

	settings := kanziLevels[level]

	info := C.pakr_knz_info{block_size: C.int(settings.block), original_size: C.int64_t(size)}

	copy(unsafe.Slice((*byte)(unsafe.Pointer(&info.entropy[0])), len(info.entropy)), settings.entropy)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&info.transform[0])), len(info.transform)), settings.transform)

	state := &kanziIO{ctx: ctx, target: target}

	return openKanzi(state, &info, threads, memory, true)
}

func NewKanziReader(ctx context.Context, source io.Reader, threads int, memory uint64) (*KanziStream, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	buffer := bufio.NewReaderSize(source, 32<<10)

	header, err := buffer.Peek(16)
	if err != nil {
		return nil, kanziHeaderError(err)
	}

	if string(header[:4]) != "KANZ" || header[4]>>4 != 7 {
		return nil, errors.New("invalid or unsupported Kanzi header (expected bitstream version 7)")
	}

	sizeMask := int(header[14]&1)<<1 | int(header[15]>>7)

	header, err = buffer.Peek(20 + 2*sizeMask)
	if err != nil {
		return nil, kanziHeaderError(err)
	}

	var (
		info    C.pakr_knz_info
		message [256]C.char
	)

	result := C.pakr_knz_probe(unsafe.Pointer(&header[0]), C.size_t(len(header)), &info, &message[0])
	if result != 0 {
		return nil, fmt.Errorf("kanzi header: %s", C.GoString(&message[0]))
	}

	state := &kanziIO{ctx: ctx, source: buffer}
	return openKanzi(state, &info, threads, memory, false)
}

func openKanzi(state *kanziIO, info *C.pakr_knz_info, threads int, memory uint64, encode bool) (*KanziStream, error) {
	err := state.ctx.Err()
	if err != nil {
		return nil, err
	}

	block := uint64(info.block_size)
	entropy := C.GoString(&info.entropy[0])
	transform := C.GoString(&info.transform[0])

	perJob := kanziJobMemory(block, entropy, transform)
	budget := membudget.Resolve(memory)

	if budget < kanziFixedMemory+perJob {
		return nil, fmt.Errorf("kanzi needs at least %d MiB for one job with a %d MiB block; memory budget is %d MiB", (kanziFixedMemory+perJob+kanziMiB-1)/kanziMiB, block/kanziMiB, budget/kanziMiB)
	}

	jobs := int(min(uint64(min(max(threads, 1), 64)), (budget-kanziFixedMemory)/perJob))

	if info.original_size >= 0 {
		blocks := max(1, (uint64(info.original_size)+block-1)/block)
		jobs = min(jobs, int(blocks))
	}

	handle := cgo.NewHandle(state)

	var (
		message  [256]C.char
		encoding C.int
	)

	if encode {
		encoding = 1
	}

	native := C.pakr_knz_open(C.uintptr_t(handle), encoding, C.int(jobs), info, &message[0])
	if native == nil {
		handle.Delete()

		return nil, fmt.Errorf("kanzi: %s", C.GoString(&message[0]))
	}

	stream := &KanziStream{
		Settings: KanziSettings{BlockSize: int(block), Jobs: jobs, Memory: kanziFixedMemory + uint64(jobs)*perJob},
		stream:   native,
		handle:   handle,
		callback: state,
		size:     int64(info.original_size),
		encode:   encode,
	}

	return stream, nil
}

func kanziJobMemory(block uint64, entropy, transform string) uint64 {
	// Reserve simultaneous block/sequence buffers, growth copies, bitstreams and
	// a native thread stack. Models below include their heap-owned tables.
	memory := 16*block + 8*kanziMiB

	for name := range strings.SplitSeq(transform, "+") {
		switch name {
		case "TEXT":
			memory += min(block*2, 1<<30) + 16*kanziMiB
		case "UTF":
			memory += 17 * kanziMiB
		case "BWT", "BWTS":
			memory += 8*block + 4*kanziMiB
		case "ROLZ", "ROLZX":
			memory += 4*block + 80*kanziMiB
		case "LZ", "LZX", "LZP":
			memory += 4*block + 8*kanziMiB
		default:
			memory += kanziMiB
		}
	}

	if entropy == "CM" || entropy == "TPAQ" || entropy == "TPAQX" {
		// The pinned memory patch bounds binary payloads by the enclosing block
		// and invokes upstream's transformed-copy fallback on encode expansion.
		memory += 4*block + 2*kanziMiB
	} else {
		memory += 24 * kanziMiB
	}

	if entropy == "TPAQ" || entropy == "TPAQX" {
		states := uint64(1 << 22)

		switch {
		case block >= 64*kanziMiB:
			states = 1 << 28
		case block >= 16*kanziMiB:
			states = 1 << 27
		case block >= 4*kanziMiB:
			states = 1 << 26
		case block >= kanziMiB:
			states = 1 << 24
		}

		factor := uint64(1)

		if entropy == "TPAQX" {
			factor = 4
		}

		memory += factor*(states+64*kanziMiB+5*kanziMiB) + min(block, 64*kanziMiB) + 40*kanziMiB
	}

	return memory
}

func kanziCloseError(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

func kanziHeaderError(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}

	return fmt.Errorf("kanzi header: %w", err)
}

//export pakrKanziRead
func pakrKanziRead(handle C.uintptr_t, data unsafe.Pointer, size C.size_t) C.int64_t {
	state := cgo.Handle(handle).Value().(*kanziIO)

	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.err == nil {
		state.err = state.ctx.Err()
	}

	if state.err != nil {
		return -1
	}

	count, err := state.source.Read(unsafe.Slice((*byte)(data), int(size)))
	state.read += uint64(count)

	if err != nil && !errors.Is(err, io.EOF) {
		state.err = err
	}

	if count == 0 && state.err != nil {
		return -1
	}

	if count == 0 && err == nil {
		state.err = io.ErrNoProgress

		return -1
	}

	return C.int64_t(count)
}

//export pakrKanziWrite
func pakrKanziWrite(handle C.uintptr_t, data unsafe.Pointer, size C.size_t) C.int64_t {
	state := cgo.Handle(handle).Value().(*kanziIO)

	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.err == nil {
		state.err = state.ctx.Err()
	}

	if state.err != nil {
		return -1
	}

	count, err := state.target.Write(unsafe.Slice((*byte)(data), int(size)))
	if err == nil && count != int(size) {
		err = io.ErrShortWrite
	}

	state.err = err

	return C.int64_t(count)
}

//export pakrKanziCancelled
func pakrKanziCancelled(handle C.uintptr_t) C.int {
	state := cgo.Handle(handle).Value().(*kanziIO)
	if state.failure() != nil {
		return 1
	}

	return 0
}
