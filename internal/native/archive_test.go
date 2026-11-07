package native

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSevenZip(t *testing.T) {
	names := []string{"file.txt", "données.txt"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			source := t.TempDir()
			data := []byte("hello from pakr")

			err := os.WriteFile(filepath.Join(source, name), data, 0o644)
			if err != nil {
				t.Fatal(err)
			}

			entries, err := os.ReadDir(source)
			if err != nil {
				t.Fatal(err)
			}

			observed := entries[0].Name()
			filename := filepath.Join(t.TempDir(), "archive.7z")

			writer, err := OpenArchive(filename, true, 3, 2)
			if err != nil {
				t.Fatal(err)
			}

			header := Header{Name: observed, Size: int64(len(data)), Mode: 0o644, Modified: time.Now()}

			err = writer.WriteHeader(header)
			if err != nil {
				t.Fatal(err)
			}

			_, err = writer.Write(data)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			reader, err := OpenArchive(filename, false, 3, 2)
			if err != nil {
				t.Fatal(err)
			}

			defer reader.Close()

			decoded, err := reader.Next()
			if err != nil {
				t.Fatalf("header: %v", err)
			}

			if decoded.Size != int64(len(data)) {
				t.Fatalf("header mismatch: %+v", decoded)
			}

			if decoded.Name != observed {
				// libarchive decomposes Unicode on macOS, including on spelling-preserving APFS.
				if runtime.GOOS != "darwin" || name == "file.txt" || filepath.Base(decoded.Name) != decoded.Name {
					t.Fatalf("filename = %q, want filesystem name %q", decoded.Name, observed)
				}

				expected, err := entries[0].Info()
				if err != nil {
					t.Fatal(err)
				}

				actual, err := os.Lstat(filepath.Join(source, decoded.Name))
				if err != nil {
					t.Fatalf("archive name %q does not resolve to filesystem name %q: %v", decoded.Name, observed, err)
				}

				if !os.SameFile(expected, actual) {
					t.Fatalf("archive name %q resolves to a different file than %q", decoded.Name, observed)
				}
			}

			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("data: %v", err)
			}

			if !bytes.Equal(output, data) {
				t.Fatal("7z content mismatch")
			}
		})
	}
}
