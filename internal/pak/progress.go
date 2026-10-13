package pak

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

const (
	progressFilled = "========================"
	progressEmpty  = "                        "
)

type Progress struct {
	bytes    atomic.Int64
	files    atomic.Int64
	total    int64
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

func (progress *Progress) Close() {
	if progress == nil {
		return
	}

	close(progress.done)

	<-progress.finished
}

func (progress *Progress) render() {
	defer close(progress.finished)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-progress.done:
			fmt.Fprint(progress.output, "\r\x1b[2K")

			return
		case <-ticker.C:
			count := progress.bytes.Load()
			rate := float64(count) / time.Since(progress.started).Seconds() / (1 << 20)

			if progress.total > 0 {
				fraction := min(float64(count)/float64(progress.total), 1)
				filled := int(fraction * 24)

				fmt.Fprintf(progress.output, "\r\x1b[2K[%s%s] %5.1f%%  %.1f MiB/s  %d files", progressFilled[:filled], progressEmpty[:24-filled], fraction*100, rate, progress.files.Load())
			} else {
				fmt.Fprintf(progress.output, "\r\x1b[2K[working] %.1f MiB  %.1f MiB/s  %d files", float64(count)/(1<<20), rate, progress.files.Load())
			}
		}
	}
}

func NewProgress(output io.Writer, total int64) *Progress {
	progress := &Progress{total: total, output: output, done: make(chan struct{}), finished: make(chan struct{}), started: time.Now()}

	go progress.render()

	return progress
}
