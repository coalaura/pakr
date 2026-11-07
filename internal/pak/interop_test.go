//go:build integration

package pak

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
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
	lzip := os.Getenv("LZIP_CLI")
	if lzip == "" {
		lzip = "lzip"
	}

	codecs := []referenceCodec{{format: "gz", tool: "gzip"}, {format: "zst", tool: "zstd"}, {format: "bz2", tool: "bzip2"}, {format: "xz", tool: "xz"}, {format: "lz", tool: lzip}, {format: "lz4", tool: "lz4"}, {format: "br", tool: "brotli"}}

	data := bytes.Repeat([]byte("pakr native interoperability\x00\xff\xfe\n"), 100000)

	for _, codec := range codecs {
		t.Run(codec.format, func(t *testing.T) {
			base := t.TempDir()
			input := filepath.Join(base, "input")

			err := os.WriteFile(input, data, 0o600)
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

			encoded, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}

			produced, verify := interop.Output(t, "archive", encoded)
			if verify {
				decoder := exec.CommandContext(t.Context(), codec.tool, "-d", "-c")
				decoder.Stdin = bytes.NewReader(produced)

				decoded, err := decoder.Output()
				if err != nil {
					t.Fatalf("reference decode: %v", err)
				}

				if !bytes.Equal(decoded, data) {
					t.Fatal("reference decoded different content")
				}
			}

			encoded = interop.Reference(t, "archive", func() []byte {
				encoder := exec.CommandContext(t.Context(), codec.tool, "-c", input)

				result, err := encoder.Output()
				if err != nil {
					t.Fatalf("reference encode: %v", err)
				}

				return result
			})

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

			decoded, err := os.ReadFile(filepath.Join(options.Output, "reference"))
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
	formats := []referenceArchive{{format: "tar", native: "pax"}, {format: "tar.lz", native: "pax"}, {format: "zip", native: "zip"}, {format: "7z", native: "7zip"}}

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

			encoded, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}

			produced, verify := interop.Output(t, "archive", encoded)
			if verify {
				err = os.WriteFile(archive, produced, 0o600)
				if err != nil {
					t.Fatal(err)
				}

				decoder := exec.CommandContext(t.Context(), "bsdtar", "-xf", archive, "-C", referenceOutput)

				output, err := decoder.CombinedOutput()
				if err != nil {
					t.Fatalf("reference decode: %v: %s", err, output)
				}

				verifyArchiveContents(t, referenceOutput, data)
			}

			archive = filepath.Join(base, "reference."+item.format)

			encoded = interop.Reference(t, "archive", func() []byte {
				encoder := exec.CommandContext(t.Context(), "bsdtar", "--format", item.native, "-cf", archive, "-C", base)

				if item.format == "tar.lz" {
					encoder.Args = append(encoder.Args, "--lzip")
				}

				encoder.Args = append(encoder.Args, "données")

				output, err := encoder.CombinedOutput()
				if err != nil {
					t.Fatalf("reference encode: %v: %s", err, output)
				}

				result, err := os.ReadFile(archive)
				if err != nil {
					t.Fatal(err)
				}

				return result
			})

			err = os.WriteFile(archive, encoded, 0o600)
			if err != nil {
				t.Fatal(err)
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

func TestReferenceLegacyArchives(t *testing.T) {
	cases := []legacyCase{
		{fixture: "cab_1.cab", format: "cab", name: "dir1/file1"},
		{fixture: "cab_2.cab", format: "cab", name: "zero"},
		{fixture: "cab_3.cab", format: "cab", name: "zero"},
		{fixture: "iso_joliet.iso", format: "iso", name: "hardlink"},
		{fixture: "iso_joliet_rockridge.iso", format: "iso", name: "hardlink"},
		{fixture: "newc.cpio.gz", format: "cpio.gz", name: "dir/file"},
		{fixture: "newc.cpio.bz2", format: "cpio.bz2", name: "dir/file"},
		{fixture: "newc.cpio.xz", format: "cpio.xz", name: "dir/file"},
		{fixture: "newc.cpio.zst", format: "cpio.zst", name: "dir/file"},
		{fixture: "crc.cpio", format: "cpio", name: "dir/file"},
		{fixture: "odc.cpio", format: "cpio", name: "dir/file"},
		{fixture: "cpio_bin_le.cpio", format: "cpio", name: "file1111222233334444"},
		{fixture: "cpio_bin_be.cpio", format: "cpio", name: "file1111222233334444"},
		{fixture: "ar.ar", format: "ar", name: "hhhhjjjjkkkkllll.o"},
		{fixture: "arbsd.ar", format: "ar", name: "a-long-unix-archive-member.txt"},
		{fixture: "package.deb", format: "deb", name: "data.tar.xz"},
		{fixture: "lha_lh6.lzh", format: "lha", name: "file1"},
		{fixture: "lha_lh7.lzh", format: "lha", name: "file1"},
		{fixture: "cpio_svr4_gzip_rpm.rpm", format: "rpm", name: "./etc/file1"},
		{fixture: "cpio_svr4_bzip2_rpm.rpm", format: "rpm", name: "./etc/file1"},
	}

	for _, item := range cases {
		t.Run(item.fixture, func(t *testing.T) {
			data := legacyFixture(t, item.fixture)

			expected := interop.Reference(t, "contents", func() []byte {
				decoder := exec.CommandContext(t.Context(), "bsdtar", "-xOf", "-", item.name)
				decoder.Stdin = bytes.NewReader(data)

				result, err := decoder.Output()
				if err != nil {
					t.Fatalf("reference decode: %v", err)
				}

				return result
			})

			output := unpackFixture(t, data, item.format)
			assertArchiveFile(t, output, item.name, expected)
		})
	}
}

func TestReferenceSeekable(t *testing.T) {
	base := t.TempDir()
	input := filepath.Join(base, "input")

	data := bytes.Repeat([]byte("seekable zstd interoperability\n"), 600000)

	err := os.WriteFile(input, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	format, err := ParseFormat("zst")
	if err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(base, "archive.zst")

	options := Options{Format: format, Output: archive, Threads: 4, Level: 3, Seekable: true}

	err = Pack(t.Context(), []string{input}, options, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}

	produced, verify := interop.Output(t, "archive", encoded)
	if verify {
		decoder := exec.CommandContext(t.Context(), "zstd", "-d", "-c")
		decoder.Stdin = bytes.NewReader(produced)

		decoded, err := decoder.Output()
		if err != nil || !bytes.Equal(decoded, data) {
			t.Fatalf("reference seekable decode: %v", err)
		}
	}

	options.Output = filepath.Join(base, "output")

	err = Unpack(t.Context(), archive, options, nil)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := os.ReadFile(filepath.Join(options.Output, "archive"))
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatalf("parallel seekable extraction: %v", err)
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
