package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestBSCFormats(t *testing.T) {
	names := []string{"bsc", "tar.bsc", "tbsc"}

	for _, name := range names {
		format, err := ParseFormat(name)
		if err != nil || format.Codec != native.BSC {
			t.Fatalf("parse %s: %+v, %v", name, format, err)
		}

		inferred, err := InferFormat("archive." + name)
		if err != nil || inferred != format {
			t.Fatalf("infer %s: %+v, %v", name, inferred, err)
		}
	}

	format, err := detectHeader([]byte("bsc1"))
	if err != nil || format.Name != "bsc" {
		t.Fatalf("magic: %+v, %v", format, err)
	}
}

func TestBSCStagingAndIntegrity(t *testing.T) {
	input := filepath.Join(t.TempDir(), "payload")
	data := bytes.Repeat([]byte("BSC staging and extraction\n"), 10000)

	err := os.WriteFile(input, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	names := []string{"bsc", "tar.bsc", "tbsc"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			staging := t.TempDir()

			t.Setenv("TMPDIR", staging)
			t.Setenv("TMP", staging)
			t.Setenv("TEMP", staging)

			format, _ := ParseFormat(name)
			options := Options{Format: format, Output: "-", Level: 1, Threads: 4, Memory: 256 << 20}

			var archive bytes.Buffer

			err := Pack(t.Context(), []string{input}, options, &archive)
			if err != nil {
				t.Fatal(err)
			}

			options.Output = t.TempDir()
			filename := "payload"

			if name == "bsc" {
				options.Format = Format{}
				filename = "stdin"
			}

			err = Unpack(t.Context(), "-", options, bytes.NewReader(archive.Bytes()))
			if err != nil {
				t.Fatal(err)
			}

			actual, err := os.ReadFile(filepath.Join(options.Output, filename))
			if err != nil || !bytes.Equal(data, actual) {
				t.Fatalf("stdin round trip: %v", err)
			}

			corrupt := bytes.Clone(archive.Bytes())
			corrupt[len(corrupt)-1] ^= 1

			assertArchiveRejected(t, corrupt, name, "", name == "bsc")
			assertArchiveRejected(t, archive.Bytes()[:archive.Len()-1], name, "", true)

			options.Output = t.TempDir()
			options.Limits = &ExtractionLimits{MaxFileSize: 1}

			err = Unpack(t.Context(), "-", options, bytes.NewReader(archive.Bytes()))
			if err == nil {
				t.Fatal("accepted file beyond extraction limit")
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			options.Output = "-"

			err = Pack(ctx, []string{input}, options, io.Discard)
			if err == nil {
				t.Fatal("cancelled packing succeeded")
			}

			entries, err := os.ReadDir(staging)
			if err != nil || len(entries) != 0 {
				t.Fatalf("staging leaked files: %v, %v", entries, err)
			}
		})
	}
}

func TestBSCTarTraversal(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "traversal-*.bsc")
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	compressor, err := native.NewBSCWriter(t.Context(), file, 1, 1, 64<<20)
	if err != nil {
		t.Fatal(err)
	}

	defer compressor.Close()

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

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	assertArchiveRejected(t, encoded, "tbsc", "", true)
}
