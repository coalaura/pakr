package native

import (
	"errors"
	"io"
	"testing"
	"time"
)

func TestCodecProgress(t *testing.T) {
	var progress codecProgress

	started := time.Now()

	err := progress.wait(started, false)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("unjustified pending: %v", err)
	}

	for elapsed := time.Duration(0); elapsed < codecStallTimeout; elapsed += 20 * time.Millisecond {
		err = progress.wait(started.Add(elapsed), true)
		if err != nil {
			t.Fatalf("genuine async wait: %v", err)
		}
	}

	err = progress.wait(started.Add(codecStallTimeout), true)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("permanent stall: %v", err)
	}

	progress = codecProgress{}

	err = progress.wait(started.Add(2*codecStallTimeout), true)
	if err != nil {
		t.Fatalf("new wait after progress: %v", err)
	}
}
