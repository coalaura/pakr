package native

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestSevenZip(t *testing.T) {
	names := []string{"file.txt", "données.txt"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "archive.7z")

			writer, err := OpenArchive(filename, true, 3, 2)
			if err != nil {
				t.Fatal(err)
			}

			data := []byte("hello from pakr")
			header := Header{Name: name, Size: int64(len(data)), Mode: 0o644, Modified: time.Now()}

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

			if decoded.Name != name || decoded.Size != int64(len(data)) {
				t.Fatalf("header mismatch: %+v", decoded)
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
