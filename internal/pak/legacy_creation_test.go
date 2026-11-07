package pak

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLegacyCreation(t *testing.T) {
	formats := []string{"cpio", "cpio.gz", "cpio.bz2", "cpio.xz", "cpio.zst", "ar", "a", "iso", "lzma", "Z"}
	payloads := [][]byte{nil, []byte("x"), bytes.Repeat([]byte("archive payload\n"), 100000), make([]byte, 300000)}

	random := rand.New(rand.NewPCG(1, 2))

	for index := range payloads[3] {
		payloads[3][index] = byte(random.Uint32())
	}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			for _, payload := range payloads {
				input := filepath.Join(t.TempDir(), "long-unicode-\u00e9-\u6f22\u5b57-file.txt")

				err = os.WriteFile(input, payload, 0o640)
				if err != nil {
					t.Fatal(err)
				}

				var output bytes.Buffer

				err = Pack(t.Context(), []string{input}, Options{Format: format, Output: "-", Level: 1, Threads: 2}, &output)
				if err != nil {
					t.Fatal(err)
				}

				directory := unpackFixture(t, output.Bytes(), name)
				member := filepath.Base(input)

				if format.Container == "raw" {
					member = "stdin"
				}

				assertArchiveFile(t, directory, member, payload)
			}
		})
	}
}

func TestLegacyCreationPublication(t *testing.T) {
	formats := []string{"cpio", "cpio.gz", "ar", "iso", "lzma", "Z"}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			input := filepath.Join(t.TempDir(), "file")

			err = os.WriteFile(input, []byte("contents"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			directory := t.TempDir()
			output := filepath.Join(directory, "archive."+name)

			options := Options{Format: format, Output: output, Level: 1, Threads: 1}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err = Pack(ctx, []string{input}, options, io.Discard)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}

			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("cancelled output leaked: %v, %v", entries, err)
			}

			err = Pack(t.Context(), []string{input}, options, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			original, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}

			err = Pack(t.Context(), []string{input}, options, io.Discard)
			if err == nil {
				t.Fatal("existing output overwritten")
			}

			current, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(original, current) {
				t.Fatalf("existing output changed: %v", err)
			}

			entries, err = os.ReadDir(directory)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary output leaked: %v, %v", entries, err)
			}
		})
	}
}

func TestLegacyCreationTree(t *testing.T) {
	formats := []string{"cpio", "cpio.gz", "cpio.bz2", "cpio.xz", "cpio.zst", "iso"}

	source := filepath.Join(t.TempDir(), "tree")
	directory := filepath.Join(source, "nested", "empty")

	err := os.MkdirAll(directory, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	names := []string{"UPPER", "long-unicode-\u00e9-\u6f22\u5b57-file.txt", strings.Repeat("a", 80) + "1.txt", strings.Repeat("a", 80) + "2.txt"}
	modified := time.Unix(1700000000, 0)

	if runtime.GOOS != "windows" {
		names = append(names, "upper")
	}

	for _, name := range names {
		filename := filepath.Join(source, "nested", name)

		err = os.WriteFile(filename, []byte(name), 0o640)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Chtimes(filename, modified, modified)
		if err != nil {
			t.Fatal(err)
		}
	}

	links := runtime.GOOS != "windows"
	if links {
		err = os.Symlink("UPPER", filepath.Join(source, "nested", "link"))
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer

			err = Pack(t.Context(), []string{source}, Options{Format: format, Output: "-", Level: 1, Threads: 2}, &output)
			if err != nil {
				t.Fatal(err)
			}

			destination := unpackFixture(t, output.Bytes(), name)

			for _, member := range names {
				filename := filepath.Join("tree", "nested", member)
				assertArchiveFile(t, destination, filename, []byte(member))

				info, err := os.Stat(filepath.Join(destination, filename))
				if err != nil {
					t.Fatal(err)
				}

				if !info.ModTime().Equal(modified) || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
					t.Fatalf("metadata: %s %v %v", member, info.Mode(), info.ModTime())
				}
			}

			info, err := os.Stat(filepath.Join(destination, "tree", "nested", "empty"))
			if err != nil || !info.IsDir() {
				t.Fatalf("empty directory: %v", err)
			}

			if links {
				assertArchiveLink(t, destination, "tree/nested/link", "UPPER")
			}
		})
	}
}

func TestLegacyCreationRejectedInput(t *testing.T) {
	formats := []string{"ar", "a", "lzma", "Z"}
	source := t.TempDir()

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			destination := t.TempDir()
			output := filepath.Join(destination, "archive."+name)

			err = Pack(t.Context(), []string{source}, Options{Format: format, Output: output, Level: 1, Threads: 1}, io.Discard)
			if err == nil {
				t.Fatal("unsupported directory accepted")
			}

			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected output leaked: %v, %v", entries, err)
			}
		})
	}
}
