package pak

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

type extractionParentReader struct {
	replace func()
}

func (source extractionParentReader) Read([]byte) (int, error) {
	source.replace()

	return 0, io.ErrUnexpectedEOF
}

// Linux permits renaming a directory while its child file is open.
func TestExtractionCleanupUsesPinnedParent(t *testing.T) {
	depths := []int{1, 32, 33, 40}

	for _, depth := range depths {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			output := t.TempDir()

			root, err := os.OpenRoot(output)
			if err != nil {
				t.Fatal(err)
			}

			defer root.Close()

			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			copying := copier{ctx: ctx, buffer: make([]byte, 16)}

			extract := extractor{root: root, copying: &copying, budget: newExtractionBudget(ctx, nil, cancel)}
			defer extract.close()

			name := strings.Repeat("d/", depth) + "file"
			parent := path.Dir(name)

			source := extractionParentReader{replace: func() {
				err = root.Rename(parent, "original")
				if err != nil {
					t.Fatal(err)
				}

				err = root.Mkdir(parent, 0o755)
				if err != nil {
					t.Fatal(err)
				}

				err = root.WriteFile(name, []byte("replacement"), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}}

			err = extract.entry(native.Header{Name: name, Kind: native.Regular, Mode: 0o600, Size: 4}, source)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("payload error: %v", err)
			}

			_, err = root.Stat("original/file")
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete file retained in pinned parent: %v", err)
			}

			data, err := root.ReadFile(name)
			if err != nil || string(data) != "replacement" {
				t.Fatalf("replacement affected by cleanup: %q, %v", data, err)
			}

			assertExtractionHandles(t, output, min(depth, 32)+1)

			extract.close()

			assertExtractionHandles(t, output, 1)
		})
	}
}

func assertExtractionHandles(t *testing.T, output string, expected int) {
	t.Helper()

	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}

	count := 0

	for _, entry := range entries {
		target, linkErr := os.Readlink("/proc/self/fd/" + entry.Name())
		if linkErr == nil && (target == output || strings.HasPrefix(target, output+"/")) {
			count++
		}
	}

	if count != expected {
		t.Fatalf("open extraction handles: got %d, want %d", count, expected)
	}
}
