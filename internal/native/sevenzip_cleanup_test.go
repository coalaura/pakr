package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type sevenZipCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (ctx *sevenZipCancelContext) Err() error {
	if ctx.remaining == 0 {
		ctx.cancel()
	} else if ctx.remaining > 0 {
		ctx.remaining--
	}

	return ctx.Context.Err()
}

// Force context.Cause through Err so cancellation can be placed inside the
// synchronous native encoder callback rather than racing a timer against Close.
func (ctx *sevenZipCancelContext) Value(key any) any {
	return nil
}

func TestSevenZipCancelledOperation(t *testing.T) {
	operations := []string{"write", "finalize"}

	for _, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()

			ctx := &sevenZipCancelContext{Context: parent, cancel: cancel, remaining: -1}
			filename := filepath.Join(t.TempDir(), "archive-\u65e5\u672c.7z")

			writer, err := Open7ZipWriter(ctx, filename, 3, 2, 1<<30)
			if err != nil {
				t.Fatal(err)
			}

			defer writer.Close()

			err = writer.WriteHeader(Header{Name: "payload", Size: 1})
			if err != nil {
				t.Fatal(err)
			}

			if operation == "write" {
				ctx.remaining = 0
				_, err = writer.Write([]byte{42})
			} else {
				_, err = writer.Write([]byte{42})
				if err != nil {
					t.Fatal(err)
				}

				// Allow Close's initial check, then cancel while finishing the encoder.
				ctx.remaining = 1
				err = writer.Close()
			}

			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s cancellation: %v", operation, err)
			}

			if operation == "write" {
				writeErr := err
				err = writer.Close()

				if !errors.Is(err, writeErr) {
					t.Fatalf("close lost write error: %v", err)
				}
			}

			assertSevenZipReleased(t, writer, filename)
		})
	}
}

func TestSevenZipFailedWriteClose(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "archive.7z")

	writer, err := Open7ZipWriter(t.Context(), filename, 3, 2, 1<<30)
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Close()

	// Writing without a header makes libarchive fatal before Close is called.
	_, writeErr := writer.Write([]byte{42})
	if writeErr == nil {
		t.Fatal("write without a header succeeded")
	}

	err = writer.Close()
	if !errors.Is(err, writeErr) {
		t.Fatalf("close lost write error: %v", err)
	}

	assertSevenZipReleased(t, writer, filename)
}

func TestSevenZipOpenFailure(t *testing.T) {
	directory := t.TempDir()

	writer, err := Open7ZipWriter(t.Context(), directory, 3, 2, 1<<30)
	if err == nil {
		writer.Close()
		t.Fatal("opened directory as output")
	}

	err = os.Remove(directory)
	if err != nil {
		t.Fatalf("remove failed output: %v", err)
	}
}

func assertSevenZipReleased(t *testing.T, writer *Archive, filename string) {
	t.Helper()

	if writer.archive != nil || writer.encoder != nil || writer.handle != 0 {
		t.Fatal("writer retained native resources")
	}

	err := os.Remove(filename)
	if err != nil {
		t.Fatalf("remove output immediately after close: %v", err)
	}

	// Keep another file open to catch a repeated close of a recycled descriptor.
	replacement, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}

	defer replacement.Close()

	err = writer.Close()
	if err != nil {
		t.Fatalf("repeated close: %v", err)
	}

	_, err = replacement.Write([]byte{42})
	if err != nil {
		t.Fatalf("repeated close damaged replacement: %v", err)
	}
}
