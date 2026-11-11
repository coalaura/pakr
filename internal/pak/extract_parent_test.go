package pak

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

func TestExtractionFileParent(t *testing.T) {
	depths := []int{0, 1, 32, 33, 40}

	for _, depth := range depths {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			defer root.Close()

			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			copying := copier{ctx: ctx, buffer: make([]byte, 16)}

			extract := extractor{root: root, copying: &copying, budget: newExtractionBudget(ctx, nil, cancel)}
			defer extract.close()

			prefix := strings.Repeat("d/", depth)
			modified := time.Unix(1700000000, 0)

			header := native.Header{Name: prefix + "file", Kind: native.Regular, Mode: 0o640, Modified: modified, Size: 4}

			err = extract.entry(header, strings.NewReader("data"))
			if err != nil {
				t.Fatal(err)
			}

			err = extract.entry(header, strings.NewReader("oops"))
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("exclusive creation: %v", err)
			}

			data, err := root.ReadFile(header.Name)
			if err != nil || string(data) != "data" {
				t.Fatalf("original contents: %q, %v", data, err)
			}

			info, err := root.Stat(header.Name)
			if err != nil {
				t.Fatal(err)
			}

			if !info.ModTime().Equal(modified) || runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
				t.Fatalf("file metadata: %v, %v", info.Mode(), info.ModTime())
			}

			header.Name = prefix + "incomplete"

			err = extract.entry(header, &errorReader{err: io.ErrUnexpectedEOF})
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("truncated payload: %v", err)
			}

			_, err = root.Stat(header.Name)
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete file retained: %v", err)
			}

			if len(extract.ancestors) > 32 {
				t.Fatalf("cached handles: %d", len(extract.ancestors))
			}
		})
	}
}

func TestExtractionImmediateParentReplacement(t *testing.T) {
	depths := []int{1, 32, 33, 40}

	for _, depth := range depths {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			defer root.Close()

			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			extract := extractor{root: root, copying: &copier{ctx: ctx}, budget: newExtractionBudget(ctx, nil, cancel)}
			defer extract.close()

			name := strings.Repeat("d/", depth) + "first"
			parent := path.Dir(name)

			err = extract.entry(native.Header{Name: name, Kind: native.Regular, Mode: 0o600}, bytes.NewReader(nil))
			if err != nil {
				t.Fatal(err)
			}

			err = root.Rename(parent, "original")
			if err != nil {
				t.Fatal(err)
			}

			err = root.Mkdir(parent, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			name = parent + "/second"

			err = extract.entry(native.Header{Name: name, Kind: native.Regular, Mode: 0o600}, bytes.NewReader(nil))
			if err == nil {
				t.Fatal("replaced immediate parent accepted")
			}

			names := []string{name, "original/second"}

			for _, candidate := range names {
				_, err = root.Stat(candidate)
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("file published at %s: %v", candidate, err)
				}
			}
		})
	}
}
