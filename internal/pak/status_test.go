package pak

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coalaura/plain/minimal"
)

type etaTestCase struct {
	name      string
	remaining int64
	rate      float64
	expected  string
}

func TestPackPreview(t *testing.T) {
	inputs := []string{"First", "Second", "Third", "Fourth", "Fifth"}

	var output bytes.Buffer

	log := minimal.New(minimal.WithTarget(&output), minimal.WithErrorTarget(&output))

	packPreview(log, inputs, "Archive.tar")

	expected := ":: pack\n   -> First\n   -> Second\n   -> Third\n   -> +2 more\n  > into Archive.tar\n"
	if output.String() != expected {
		t.Fatalf("unexpected preview: %q", output.String())
	}

	longPath := strings.Repeat("資料/", 40) + "MixedCase.txt"

	preview := previewPath(longPath)
	if len(preview) > previewPathLimit || !utf8.ValidString(preview) || !strings.HasSuffix(preview, "MixedCase.txt") {
		t.Fatalf("invalid shortened path: %q", preview)
	}

	preview = previewPath(strings.Repeat("\x80", previewPathLimit+1))
	if len(preview) > previewPathLimit {
		t.Fatalf("invalid path bytes were not bounded: %q", preview)
	}

	output.Reset()

	packPreview(log, []string{"line\nbreak\x1b.txt"}, "Archive.tar")

	if strings.Count(output.String(), "\n") != 1 || strings.ContainsRune(output.String(), '\x1b') {
		t.Fatalf("preview allowed terminal control characters: %q", output.String())
	}
}

func TestProgressCompletion(t *testing.T) {
	var output bytes.Buffer

	log := minimal.New(minimal.WithTarget(&output), minimal.WithErrorTarget(&output))

	progress := NewProgress(log, &output, "scan")
	defer progress.Close()

	progress.Phase("pack", 2048)
	progress.Add(2048)
	progress.File()
	progress.File()
	progress.Phase("finish", -1)

	if !bytes.Contains(progress.line(nil, time.Now()), []byte("finish")) {
		t.Fatal("missing finalization phase")
	}

	progress.Complete("packed", "MixedCase.tar", 1024)
	progress.Close()

	text := output.String()
	if !strings.Contains(text, "\r\x1b[2K:: packed 2 entries | 2.0 kib -> 1.0 kib (50.0%) | ") || !strings.HasSuffix(text, "s | MixedCase.tar\n") {
		t.Fatalf("unexpected completion: %q", text)
	}

	if strings.Count(text, "\n") != 1 {
		t.Fatalf("unexpected terminal lifecycle: %q", text)
	}
}

func TestProgressETA(t *testing.T) {
	started := time.Now().Add(-time.Hour)

	progress := &Progress{started: started, phaseStarted: started}
	progress.Phase("pack", 2<<20)

	if !progress.phaseStarted.After(started) {
		t.Fatal("transfer timing includes scanning")
	}

	now := progress.phaseStarted.Add(10 * time.Second)

	line := string(progress.line(nil, now))
	if !strings.HasSuffix(line, "eta --") {
		t.Fatalf("expected unknown ETA before any bytes, got %q", line)
	}

	progress.Add(1 << 20)

	line = string(progress.line(nil, now))
	if !strings.HasSuffix(line, "0.1 mib/s  0 entries  eta 10s") {
		t.Fatalf("unexpected transfer rate or ETA: %q", line)
	}

	progress.Add(2 << 20)

	line = string(progress.line(nil, now))
	if !strings.HasSuffix(line, "eta 0s") {
		t.Fatalf("expected zero ETA after exceeding total, got %q", line)
	}

	progress.Phase("finish", -1)

	line = string(progress.line(nil, now))
	if strings.Contains(line, "eta") {
		t.Fatalf("unexpected finalization ETA: %q", line)
	}

	progress.Phase("unpack", 0)

	line = string(progress.line(nil, now))
	if strings.Contains(line, "eta") {
		t.Fatalf("unexpected ETA without a total: %q", line)
	}
}

func TestAppendETA(t *testing.T) {
	cases := []etaTestCase{
		{name: "no rate", remaining: 100, rate: 0, expected: "--"},
		{name: "complete", remaining: 0, rate: 0, expected: "0s"},
		{name: "round up", remaining: 1, rate: 2, expected: "1s"},
		{name: "seconds", remaining: 59, rate: 1, expected: "59s"},
		{name: "minute", remaining: 60, rate: 1, expected: "1m 0s"},
		{name: "minutes", remaining: 83, rate: 1, expected: "1m 23s"},
		{name: "hours", remaining: 3723, rate: 1, expected: "1h 2m 3s"},
		{name: "overflow", remaining: math.MaxInt64, rate: 0.1, expected: "--"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			actual := string(appendETA(nil, test.remaining, test.rate))
			if actual != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestFailedProgressClearsLine(t *testing.T) {
	var output bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	options := Options{
		Format:   Format{Name: "tar", Container: "tar"},
		Output:   filepath.Join(t.TempDir(), "Archive.tar"),
		Log:      minimal.New(minimal.WithTarget(&output), minimal.WithErrorTarget(&output)),
		Progress: &output,
	}

	inputs := []string{t.TempDir()}

	err := Pack(ctx, inputs, options, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}

	if !strings.HasSuffix(output.String(), "\r\x1b[2K") || strings.Contains(output.String(), "packed ") {
		t.Fatalf("unexpected failed progress: %q", output.String())
	}
}
