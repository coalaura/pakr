package native

import (
	"io"
	"time"
)

const codecStallTimeout = time.Minute

type codecProgress struct {
	waiting time.Time
}

func (progress *codecProgress) wait(now time.Time, pending bool) error {
	if !pending {
		return io.ErrNoProgress
	}

	if progress.waiting.IsZero() {
		progress.waiting = now
	}

	if now.Sub(progress.waiting) >= codecStallTimeout {
		return io.ErrNoProgress
	}

	return nil
}
