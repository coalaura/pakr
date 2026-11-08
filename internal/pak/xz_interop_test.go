//go:build integration

package pak

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
)

func TestXZCLIContainers(t *testing.T) {
	cli := os.Getenv("XZ_CLI")
	if cli == "" {
		cli = "xz"
	}

	data := bytes.Repeat([]byte("XZ container payload\x00\xff\n"), 1000)
	input := filepath.Join(t.TempDir(), "input")

	err := os.WriteFile(input, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	formats := []string{"xz", "lzma2", "tar.xz", "txz", "cpio.xz"}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer

			options := Options{Format: format, Output: "-", Level: 3, Threads: 2, Memory: 128 << 20}

			err = Pack(t.Context(), []string{input}, options, &output)
			if err != nil {
				t.Fatal(err)
			}

			produced, verify := interop.Output(t, "archive.xz", output.Bytes())
			if !verify {
				return
			}

			command := exec.CommandContext(t.Context(), cli, "--decompress", "--stdout", "--threads=1", "--memlimit-decompress=64MiB")
			command.Stdin = bytes.NewReader(produced)

			decoded, err := command.Output()
			if err != nil {
				t.Fatalf("official XZ decode: %v", err)
			}

			if name == "tar.xz" || name == "txz" {
				reader := tar.NewReader(bytes.NewReader(decoded))

				entry, err := reader.Next()
				if err != nil || entry.Name != "input" {
					t.Fatalf("tar entry: %v, %v", entry, err)
				}

				decoded, err = io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}

				_, err = reader.Next()
				if !errors.Is(err, io.EOF) {
					t.Fatalf("tar termination: %v", err)
				}
			}

			if name == "cpio.xz" {
				if !bytes.HasPrefix(decoded, []byte("070701")) || !bytes.Contains(decoded, data) || !bytes.Contains(decoded, []byte("TRAILER!!!")) {
					t.Fatal("invalid decoded newc container")
				}
			} else if !bytes.Equal(decoded, data) {
				t.Fatal("decoded payload mismatch")
			}
		})
	}
}
