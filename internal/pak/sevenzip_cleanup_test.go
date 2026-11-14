package pak

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type sevenZipOutputContext struct {
	context.Context
	testing   *testing.T
	directory string
	action    func()
	triggered bool
}

func (ctx *sevenZipOutputContext) Err() error {
	if !ctx.triggered {
		entries, err := os.ReadDir(ctx.directory)
		if err != nil {
			ctx.testing.Fatal(err)
		}

		if len(entries) != 0 {
			ctx.triggered = true
			ctx.action()
		}
	}

	return ctx.Context.Err()
}

func TestSevenZipPackFailureCleanup(t *testing.T) {
	failures := []string{"cancel", "source"}

	for _, failure := range failures {
		t.Run(failure, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "payload")

			err := os.WriteFile(source, []byte{42}, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			parent, cancel := context.WithCancel(t.Context())
			defer cancel()

			directory := t.TempDir()

			ctx := &sevenZipOutputContext{
				Context:   parent,
				testing:   t,
				directory: directory,
				action:    cancel,
			}

			wantErr := context.Canceled

			if failure == "source" {
				wantErr = os.ErrNotExist

				ctx.action = func() {
					err := os.Remove(source)
					if err != nil {
						t.Fatal(err)
					}
				}
			}

			format, err := ParseFormat("7z")
			if err != nil {
				t.Fatal(err)
			}

			options := Options{
				Format:  format,
				Output:  filepath.Join(directory, "archive.7z"),
				Level:   3,
				Threads: 2,
				Memory:  1 << 30,
			}

			// Trigger only after collection and creation of the temporary output.
			err = Pack(ctx, []string{source}, options, io.Discard)
			if !ctx.triggered || !errors.Is(err, wantErr) {
				t.Fatalf("pack failure: triggered=%v, error=%v", ctx.triggered, err)
			}

			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed pack published output or leaked staging file: %v, %v", entries, err)
			}
		})
	}
}
