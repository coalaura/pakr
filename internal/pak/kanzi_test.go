package pak

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestKanziFormats(t *testing.T) {
	names := []string{"knz", "tar.knz", "tknz"}

	for _, name := range names {
		format, err := ParseFormat(name)
		if err != nil || format.Codec != native.Kanzi {
			t.Fatalf("parse %s: %+v, %v", name, format, err)
		}

		inferred, err := InferFormat("archive." + name)
		if err != nil || inferred != format {
			t.Fatalf("infer %s: %+v, %v", name, inferred, err)
		}
	}

	format, err := detectHeader([]byte("KANZ"))
	if err != nil || format.Name != "knz" {
		t.Fatalf("magic: %+v, %v", format, err)
	}
}

func TestKanziPackingStdin(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "missing-temp-directory")

	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)

	input := []byte("native streaming without staging\n")
	format, _ := ParseFormat("knz")

	options := Options{Format: format, Output: "-", Level: 3, Threads: 4, Memory: 512 << 20, Stdin: bytes.NewReader(input)}

	var archive bytes.Buffer

	err := Pack(t.Context(), []string{"-"}, options, &archive)
	if err != nil {
		t.Fatal(err)
	}

	encoded := bytes.Clone(archive.Bytes())

	var decoded bytes.Buffer

	options.Stdout = &decoded

	err = Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
	if err != nil || !bytes.Equal(decoded.Bytes(), input) {
		t.Fatalf("stdout round trip: %v", err)
	}

	options.Limits = &ExtractionLimits{MaxOutput: 1}

	err = Unpack(t.Context(), "-", options, bytes.NewReader(encoded))
	if !errors.Is(err, ErrExtractionLimit) {
		t.Fatalf("stdout output limit: %v", err)
	}

	options.Limits = nil
	options.Output = filepath.Join(base, "out")

	err = Unpack(t.Context(), "-", options, &archive)
	if err != nil {
		t.Fatal(err)
	}

	actual, err := os.ReadFile(filepath.Join(options.Output, "stdin"))
	if err != nil || !bytes.Equal(actual, input) {
		t.Fatalf("stdin round trip: %v", err)
	}
}

func TestKanziExtractionIntegrity(t *testing.T) {
	names := []string{"knz", "tar.knz"}

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
			corrupt[len(corrupt)-10] ^= 1

			cases := [][]byte{corrupt, encoded[:len(encoded)-1], append(bytes.Clone(encoded), 'x')}

			for _, data := range cases {
				assertArchiveRejected(t, data, name, "", name == "knz")
			}
		})
	}
}

func TestKanziTarTraversal(t *testing.T) {
	var archive bytes.Buffer

	compressor, err := native.NewKanziWriter(t.Context(), &archive, 3, 1, 512<<20, -1)
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

	assertArchiveRejected(t, archive.Bytes(), "tknz", "", true)
}
