package pak

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestLzipFormats(t *testing.T) {
	names := []string{"lz", "lzip", "tar.lz", "tlz"}

	for _, name := range names {
		format, err := ParseFormat(name)
		if err != nil || format.Codec != native.Lzip {
			t.Fatalf("parse %s: %+v, %v", name, format, err)
		}

		inferred, err := InferFormat("archive." + name)
		if err != nil || inferred != format {
			t.Fatalf("infer %s: %+v, %v", name, inferred, err)
		}
	}

	format, err := detectHeader([]byte("LZIP\x01\x0c"))
	if err != nil || format.Name != "lz" {
		t.Fatalf("magic: %+v, %v", format, err)
	}
}

func TestLzipExtractionIntegrity(t *testing.T) {
	names := []string{"lz", "tar.lz"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "payload")

			err := os.WriteFile(input, []byte("validated payload"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			format, _ := ParseFormat(name)

			var archive bytes.Buffer

			err = Pack(t.Context(), []string{input}, Options{Format: format, Output: "-", Level: 3, Threads: 1}, &archive)
			if err != nil {
				t.Fatal(err)
			}

			encoded := archive.Bytes()

			corrupt := bytes.Clone(encoded)
			corrupt[len(corrupt)-20] ^= 1

			cases := [][]byte{corrupt, encoded[:len(encoded)-1], append(bytes.Clone(encoded), 'x')}

			for _, data := range cases {
				assertArchiveRejected(t, data, name, "", name == "lz")
			}
		})
	}
}

func TestLzipTarTraversal(t *testing.T) {
	var archive bytes.Buffer

	compressor, err := native.NewLzipWriter(t.Context(), &archive, 3, 64<<20, -1)
	if err != nil {
		t.Fatal(err)
	}

	writer := tar.NewWriter(compressor)

	err = writer.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1})
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.WriteString(writer, "x")
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = compressor.Close()
	if err != nil {
		t.Fatal(err)
	}

	assertArchiveRejected(t, archive.Bytes(), "tlz", "", true)
}
