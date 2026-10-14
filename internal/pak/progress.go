package pak

import (
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coalaura/plain/minimal"
)

const (
	progressFilled = "========================"
	progressEmpty  = "                        "
)

type Progress struct {
	bytes    atomic.Int64
	files    atomic.Int64
	mutex    sync.Mutex
	phase    string
	total    int64
	log      *minimal.Minimal
	output   io.Writer
	done     chan struct{}
	finished chan struct{}
	started  time.Time
}

func (progress *Progress) Add(count int) {
	if progress != nil {
		progress.bytes.Add(int64(count))
	}
}

func (progress *Progress) File() {
	if progress != nil {
		progress.files.Add(1)
	}
}

func (progress *Progress) Phase(phase string, total int64) {
	if progress == nil {
		return
	}

	progress.mutex.Lock()
	progress.phase = phase

	if total >= 0 {
		progress.total = total
	}

	progress.mutex.Unlock()
}

func (progress *Progress) Close() {
	if progress == nil || progress.done == nil {
		return
	}

	close(progress.done)
	<-progress.finished

	progress.done = nil
}

func (progress *Progress) Complete(action, destination string, size int64) {
	if progress == nil {
		return
	}

	progress.Close()

	if progress.log == nil {
		return
	}

	var storage [256]byte

	inputSize := progress.bytes.Load()

	buffer := append(storage[:0], action...)
	buffer = append(buffer, ' ')
	buffer = appendEntries(buffer, progress.files.Load())
	buffer = append(buffer, " | "...)
	buffer = appendSize(buffer, inputSize)

	if size >= 0 {
		buffer = append(buffer, " -> "...)
		buffer = appendSize(buffer, size)
		buffer = append(buffer, " ("...)

		if inputSize > 0 {
			ratio := float64(size) / float64(inputSize) * 100

			buffer = strconv.AppendFloat(buffer, ratio, 'f', 1, 64)
			buffer = append(buffer, '%')
		} else {
			buffer = append(buffer, "n/a"...)
		}

		buffer = append(buffer, ')')
	}

	buffer = append(buffer, " | "...)
	buffer = strconv.AppendFloat(buffer, time.Since(progress.started).Seconds(), 'f', 1, 64)
	buffer = append(buffer, "s | "...)
	buffer = append(buffer, displayPath(destination)...)

	_ = progress.log.Successf("%s\n", buffer)
}

func (progress *Progress) render() {
	defer close(progress.finished)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Reuse the line buffer for every refresh.
	buffer := make([]byte, 0, 256)

	for {
		buffer = progress.line(buffer[:0])
		_, _ = progress.output.Write(buffer)

		select {
		case <-progress.done:
			_, _ = io.WriteString(progress.output, "\r\x1b[2K")

			return
		case <-ticker.C:
		}
	}
}

func (progress *Progress) line(buffer []byte) []byte {
	progress.mutex.Lock()
	phase := progress.phase
	total := progress.total
	progress.mutex.Unlock()

	buffer = append(buffer, "\r\x1b[2K["...)

	count := progress.bytes.Load()

	if total > 0 {
		fraction := min(float64(count)/float64(total), 1)
		filled := int(fraction * 24)

		buffer = append(buffer, progressFilled[:filled]...)
		buffer = append(buffer, progressEmpty[:24-filled]...)
		buffer = append(buffer, "] "...)
		buffer = strconv.AppendFloat(buffer, fraction*100, 'f', 1, 64)
		buffer = append(buffer, "%  "...)
	} else {
		buffer = append(buffer, "working] "...)
	}

	buffer = append(buffer, phase...)

	if phase == "scan" || phase == "read" {
		return buffer
	}

	buffer = append(buffer, "  "...)

	if total == 0 {
		buffer = appendSize(buffer, count)
		buffer = append(buffer, "  "...)
	}

	rate := float64(count) / time.Since(progress.started).Seconds() / (1 << 20)

	buffer = strconv.AppendFloat(buffer, rate, 'f', 1, 64)
	buffer = append(buffer, " mib/s  "...)
	buffer = appendEntries(buffer, progress.files.Load())

	return buffer
}

func NewProgress(log *minimal.Minimal, output io.Writer, phase string) *Progress {
	if log == nil && output == nil {
		return nil
	}

	progress := &Progress{log: log, output: output, phase: phase, started: time.Now()}

	if output != nil {
		progress.done = make(chan struct{})
		progress.finished = make(chan struct{})

		go progress.render()
	}

	return progress
}

func appendSize(buffer []byte, size int64) []byte {
	if size < 1024 {
		buffer = strconv.AppendInt(buffer, size, 10)

		return append(buffer, " b"...)
	}

	value := float64(size)
	units := [...]string{"b", "kib", "mib", "gib", "tib", "pib", "eib"}
	unit := 0

	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}

	buffer = strconv.AppendFloat(buffer, value, 'f', 1, 64)
	buffer = append(buffer, ' ')

	return append(buffer, units[unit]...)
}

func appendEntries(buffer []byte, count int64) []byte {
	buffer = strconv.AppendInt(buffer, count, 10)

	if count == 1 {
		return append(buffer, " entry"...)
	}

	return append(buffer, " entries"...)
}
