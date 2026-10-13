package pak

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRoundTrips(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "données")

	err := os.MkdirAll(filepath.Join(source, "empty"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	data := bytes.Repeat([]byte("pakr archive round trip\n"), 50000)

	err = os.WriteFile(filepath.Join(source, "hello.txt"), data, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(source, "zero"), nil, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	names := []string{"tar", "tar.gz", "tar.zst", "tar.bz2", "tar.xz", "tar.lz4", "tar.br", "zip", "7z", "gz", "zst", "bz2", "xz", "lz4", "br"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			input := source

			if format.Container == "" {
				input = filepath.Join(source, "hello.txt")
			}

			archive := filepath.Join(base, "hello.txt."+name)
			options := Options{Format: format, Output: archive, Threads: 2, Level: 3}

			err = Pack(context.Background(), []string{input}, options, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			err = Pack(context.Background(), []string{input}, options, io.Discard)
			if err == nil {
				t.Fatal("existing archive overwritten")
			}

			output := filepath.Join(base, "unpack-"+name)
			options.Output = output

			err = Unpack(context.Background(), archive, options, nil)
			if err != nil {
				t.Fatal(err)
			}

			file := filepath.Join(output, "hello.txt")

			if format.Container != "" {
				file = filepath.Join(output, "données", "hello.txt")

				info, statErr := os.Stat(filepath.Join(output, "données", "empty"))
				if statErr != nil || !info.IsDir() {
					t.Fatalf("empty directory missing: %v", statErr)
				}
			}

			actual, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(actual, data) {
				t.Fatal("extracted content mismatch")
			}

			err = Unpack(context.Background(), archive, options, nil)
			if err == nil {
				t.Fatal("existing extracted file overwritten")
			}
		})
	}
}

func TestRejectEscapingPaths(t *testing.T) {
	names := []string{"../outside", "/outside", "C:/outside", `..\outside`}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var encoded bytes.Buffer

			archive := tar.NewWriter(&encoded)

			err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: 1})
			if err != nil {
				t.Fatal(err)
			}

			_, err = archive.Write([]byte("x"))
			if err != nil {
				t.Fatal(err)
			}

			err = archive.Close()
			if err != nil {
				t.Fatal(err)
			}

			base := t.TempDir()
			input := filepath.Join(base, "evil.tar")

			err = os.WriteFile(input, encoded.Bytes(), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			options := Options{Output: filepath.Join(base, "out"), Threads: 1, Level: 3}

			err = Unpack(context.Background(), input, options, nil)
			if err == nil {
				t.Fatal("escaping archive path accepted")
			}
		})
	}
}
