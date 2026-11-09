package pak

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

type spoolLimitReader struct {
	remaining int64
	read      int64
}

func (reader *spoolLimitReader) Read(data []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}

	count := int(min(int64(len(data)), reader.remaining))
	clear(data[:count])

	reader.remaining -= int64(count)
	reader.read += int64(count)

	return count, nil
}

func TestStdinSpoolLimit(t *testing.T) {
	maximums := []uint64{0, 8, DefaultMaxOutput, math.MaxUint64}
	expected := []int64{DefaultMaxOutput + DefaultMaxOutput/8 + 256<<20, 9 + 256<<20, DefaultMaxOutput + DefaultMaxOutput/8 + 256<<20, DefaultMaxOutput + DefaultMaxOutput/8 + 256<<20}

	for index, maximum := range maximums {
		actual := stdinSpoolLimit(&ExtractionLimits{MaxOutput: maximum})
		if actual != expected[index] {
			t.Fatalf("max-output %d: got %d, want %d", maximum, actual, expected[index])
		}
	}

	if stdinSpoolLimit(nil) != expected[0] {
		t.Fatal("default spool limit differs from disabled payload limit")
	}
}

func TestStdinSpoolingBoundAndCleanup(t *testing.T) {
	temporary := t.TempDir()
	output := t.TempDir()

	t.Setenv("TMPDIR", temporary)
	t.Setenv("TMP", temporary)
	t.Setenv("TEMP", temporary)

	names := []string{"zip", "zipx", "7z", "rar", "bsc", "tar.bsc", "wim", "esd", "cab", "iso", "cpio", "cpio.gz", "ar", "deb", "lha", "rpm", "Z", "lzma", "unknown", "detected-zip"}
	limits := &ExtractionLimits{MaxOutput: 1}
	limit := stdinSpoolLimit(limits)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var (
				format Format
				err    error
			)

			if name != "unknown" && name != "detected-zip" {
				format, err = ParseFormat(name)
				if err != nil {
					t.Fatal(err)
				}
			}

			// An unbounded spool attempts another read and gets ErrUnexpectedEOF.
			generated := &spoolLimitReader{remaining: limit + 1}

			var (
				source io.Reader = generated
				prefix string
			)

			if name == "detected-zip" {
				prefix = "PK\x03\x04"
				generated.remaining -= int64(len(prefix))
				source = io.MultiReader(strings.NewReader(prefix), generated)
			}

			err = Unpack(t.Context(), "-", Options{Format: format, Output: output, Limits: limits}, source)
			if !errors.Is(err, ErrExtractionLimit) || !strings.Contains(err.Error(), "stdin exceeds spool limit") {
				t.Fatalf("spool limit not enforced: %v", err)
			}

			if generated.read+int64(len(prefix)) != limit+1 {
				t.Fatalf("read beyond spool detection byte: %d", generated.read)
			}

			assertSpoolEmpty(t, temporary)
			assertSpoolEmpty(t, output)
		})
	}
}

func TestStdinSpoolingFailureCleanup(t *testing.T) {
	temporary := t.TempDir()
	output := t.TempDir()

	t.Setenv("TMPDIR", temporary)
	t.Setenv("TMP", temporary)
	t.Setenv("TEMP", temporary)

	format, _ := ParseFormat("zip")
	options := Options{Format: format, Output: output}
	source := &spoolLimitReader{remaining: 1024}

	err := Unpack(t.Context(), "-", options, source)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error lost: %v", err)
	}

	assertSpoolEmpty(t, temporary)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = Unpack(ctx, "-", options, bytes.NewReader(nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}

	assertSpoolEmpty(t, temporary)

	err = Unpack(t.Context(), "-", options, strings.NewReader("invalid zip"))
	if err == nil {
		t.Fatal("invalid archive accepted")
	}

	assertSpoolEmpty(t, temporary)
}

func assertSpoolEmpty(t *testing.T, directory string) {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Fatalf("spooling left files in %s: %v", directory, entries)
	}
}
