//go:build integration && linux

package pak

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type referenceCodec struct {
	format string
	tool   string
}

type referenceArchive struct {
	format string
	native string
}

func TestReferenceInteroperability(t *testing.T) {
	codecs := []referenceCodec{{format: "gz", tool: "gzip"}, {format: "zst", tool: "zstd"}, {format: "bz2", tool: "bzip2"}, {format: "xz", tool: "xz"}, {format: "lz4", tool: "lz4"}, {format: "br", tool: "brotli"}}

	data := bytes.Repeat([]byte("pakr native interoperability\x00\xff\xfe\n"), 100000)

	for _, codec := range codecs {
		t.Run(codec.format, func(t *testing.T) {
			tool, err := exec.LookPath(codec.tool)
			if err != nil {
				t.Fatal(err)
			}

			base := t.TempDir()
			input := filepath.Join(base, "input")

			err = os.WriteFile(input, data, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			format, err := ParseFormat(codec.format)
			if err != nil {
				t.Fatal(err)
			}

			archive := filepath.Join(base, "archive."+codec.format)
			options := Options{Format: format, Output: archive, Threads: 2, Level: 3}

			err = Pack(context.Background(), []string{input}, options, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			decoder := exec.CommandContext(t.Context(), tool, "-d", "-c", archive)

			decoded, err := decoder.Output()
			if err != nil {
				t.Fatalf("reference decode: %v", err)
			}

			if !bytes.Equal(decoded, data) {
				t.Fatal("reference decoded different content")
			}

			encoder := exec.CommandContext(t.Context(), tool, "-c", input)

			encoded, err := encoder.Output()
			if err != nil {
				t.Fatalf("reference encode: %v", err)
			}

			archive = filepath.Join(base, "reference."+codec.format)

			err = os.WriteFile(archive, encoded, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			options.Output = filepath.Join(base, "output")

			err = Unpack(context.Background(), archive, options, nil)
			if err != nil {
				t.Fatalf("reference archive decode: %v", err)
			}

			decoded, err = os.ReadFile(filepath.Join(options.Output, "reference"))
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(decoded, data) {
				t.Fatal("pakr decoded different content")
			}
		})
	}
}

func TestReferenceArchives(t *testing.T) {
	tool, err := exec.LookPath("bsdtar")
	if err != nil {
		t.Fatal(err)
	}

	formats := []referenceArchive{{format: "tar", native: "pax"}, {format: "zip", native: "zip"}, {format: "7z", native: "7zip"}}

	data := []byte("pakr archive interoperability\n")

	for _, item := range formats {
		t.Run(item.format, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "données")

			err := os.MkdirAll(filepath.Join(source, "empty"), 0o755)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(filepath.Join(source, "file"), data, 0o640)
			if err != nil {
				t.Fatal(err)
			}

			err = os.Symlink("file", filepath.Join(source, "link"))
			if err != nil {
				t.Fatal(err)
			}

			format, err := ParseFormat(item.format)
			if err != nil {
				t.Fatal(err)
			}

			archive := filepath.Join(base, "archive."+item.format)
			options := Options{Format: format, Output: archive, Threads: 2, Level: 3}

			err = Pack(t.Context(), []string{source}, options, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			referenceOutput := filepath.Join(base, "reference-out")

			err = os.Mkdir(referenceOutput, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			decoder := exec.CommandContext(t.Context(), tool, "-xf", archive, "-C", referenceOutput)

			output, err := decoder.CombinedOutput()
			if err != nil {
				t.Fatalf("reference decode: %v: %s", err, output)
			}

			verifyArchiveContents(t, referenceOutput, data)

			archive = filepath.Join(base, "reference."+item.format)
			encoder := exec.CommandContext(t.Context(), tool, "--format", item.native, "-cf", archive, "-C", base, "données")

			output, err = encoder.CombinedOutput()
			if err != nil {
				t.Fatalf("reference encode: %v: %s", err, output)
			}

			options.Output = filepath.Join(base, "pakr-out")

			err = Unpack(t.Context(), archive, options, nil)
			if err != nil {
				t.Fatalf("reference archive decode: %v", err)
			}

			verifyArchiveContents(t, options.Output, data)
		})
	}
}

func verifyArchiveContents(t *testing.T, directory string, expected []byte) {
	t.Helper()

	source := filepath.Join(directory, "données")

	data, err := os.ReadFile(filepath.Join(source, "file"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(data, expected) {
		t.Fatal("archive content mismatch")
	}

	link, err := os.Readlink(filepath.Join(source, "link"))
	if err != nil || link != "file" {
		t.Fatalf("symbolic link mismatch: %q: %v", link, err)
	}

	info, err := os.Stat(filepath.Join(source, "empty"))
	if err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: %v", err)
	}
}
