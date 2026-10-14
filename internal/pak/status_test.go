package pak

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/coalaura/plain/minimal"
)

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

	if !bytes.Contains(progress.line(nil), []byte("finish")) {
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
