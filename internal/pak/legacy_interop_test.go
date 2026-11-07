package pak

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type legacyCase struct {
	fixture string
	format  string
	name    string
	payload string
	magic   bool
}

func TestLegacyInterop(t *testing.T) {
	cases := []legacyCase{
		{"cab_1.cab", "cab", "empty", "", true},
		{"cab_2.cab", "cab", "zero", strings.Repeat("\x00", 33000), true},
		{"cab_3.cab", "cab", "zero", strings.Repeat("\x00", 33000), true},
		{"ar.ar", "ar", "hhhhjjjjkkkkllll.o", "987654321", true},
		{"arbsd.ar", "a", "a-long-unix-archive-member.txt", "long ar member\n", true},
		{"package.deb", "deb", "control.tar.gz", "opaque control member\n", true},
		{"lha_header0.lzh", "lha", "file1", strings.Repeat(" ", 26) + "file 1 contents\nhello\nhello\nhello\n", false},
		{"lha_header2.lzh", "lzh", "file1", strings.Repeat(" ", 26) + "file 1 contents\nhello\nhello\nhello\n", false},
		{"lha_lh6.lzh", "lha", "file1", strings.Repeat(" ", 26) + "file 1 contents\nhello\nhello\nhello\n", false},
		{"lha_lh7.lzh", "lha", "file1", strings.Repeat(" ", 26) + "file 1 contents\nhello\nhello\nhello\n", false},
		{"cpio_bin_be.cpio", "cpio", "file1111222233334444", "abcde", true},
		{"cpio_bin_le.cpio", "cpio", "file1111222233334444", "abcde", true},
		{"newc.cpio", "cpio", "dir/file", "legacy archive payload\n", true},
		{"crc.cpio", "cpio", "dir/file", "legacy archive payload\n", true},
		{"odc.cpio", "cpio", "dir/file", "legacy archive payload\n", true},
		{"newc.cpio.gz", "cpio.gz", "dir/file", "legacy archive payload\n", true},
		{"newc.cpio.bz2", "cpio.bz2", "dir/file", "legacy archive payload\n", true},
		{"newc.cpio.xz", "cpio.xz", "dir/file", "legacy archive payload\n", true},
		{"newc.cpio.zst", "cpio.zst", "dir/file", "legacy archive payload\n", true},
		{"cpio_svr4_gzip_rpm.rpm", "rpm", "etc/file1", "hello\n", true},
		{"cpio_svr4_bzip2_rpm.rpm", "rpm", "etc/file3", "hello\n", true},
		{"iso_joliet.iso", "iso", "long-joliet-file-name.textfile", "hello\n", true},
		{"iso_joliet_rockridge.iso", "iso", "long-joliet-file-name.textfile", "hello\n", true},
		{"payload.lzma", "lzma", "stdin", "legacy stream\n", false},
		{"empty.lzma", "lzma", "stdin", "", false},
		{"payload.Z", "Z", "stdin", string(legacyFixture(t, "payload.cpio")), true},
	}

	for _, test := range cases {
		t.Run(test.fixture, func(t *testing.T) {
			data := legacyFixture(t, test.fixture)
			output := unpackFixture(t, data, test.format)

			assertArchiveFile(t, output, test.name, []byte(test.payload))

			if test.magic {
				output = unpackFixture(t, data, "")
				assertArchiveFile(t, output, test.name, []byte(test.payload))
			}

			input := filepath.Join(t.TempDir(), "archive."+test.format)

			err := os.WriteFile(input, data, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			output = t.TempDir()

			err = Unpack(t.Context(), input, Options{Output: output}, nil)
			if err != nil {
				t.Fatal(err)
			}

			name := test.name
			if name == "stdin" {
				name = "archive"
			}

			assertArchiveFile(t, output, name, []byte(test.payload))
		})
	}
}

func TestLegacyFormats(t *testing.T) {
	names := []string{"cab", "iso", "cpio", "cpio.gz", "cpio.bz2", "cpio.xz", "cpio.zst", "ar", "a", "deb", "lha", "lzh", "rpm", "Z", "lzma"}

	for _, name := range names {
		format, err := ParseFormat(name)
		if err != nil {
			t.Fatal(err)
		}

		inferred, err := InferFormat("archive." + name)
		if err != nil || inferred != format {
			t.Fatalf("%s inference: %v", name, err)
		}

		err = Pack(t.Context(), []string{"missing"}, Options{Format: format}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "extraction-only") {
			t.Fatalf("%s creation accepted: %v", name, err)
		}

		err = Pack(t.Context(), []string{"missing"}, Options{Output: filepath.Join(t.TempDir(), "archive."+name)}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "extraction-only") {
			t.Fatalf("%s creation extension accepted: %v", name, err)
		}
	}
}

func TestCompressedCPIODetection(t *testing.T) {
	codecs := []string{"gz", "bz2", "xz", "zst"}

	for _, codec := range codecs {
		data := legacyFixture(t, "newc.cpio."+codec)
		input := filepath.Join(t.TempDir(), "archive."+codec)

		err := os.WriteFile(input, data, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		output := t.TempDir()

		err = Unpack(t.Context(), input, Options{Output: output}, nil)
		if err != nil {
			t.Fatal(err)
		}

		assertArchiveFile(t, output, "dir/file", []byte("legacy archive payload\n"))

		output = unpackFixture(t, data, codec)
		assertArchiveFile(t, output, "stdin", legacyFixture(t, "newc.cpio"))
	}
}

func TestLegacyMetadataAndNames(t *testing.T) {
	output := unpackFixture(t, legacyFixture(t, "iso_joliet_rockridge.iso"), "iso")
	assertArchiveLink(t, output, "symlink", "long-joliet-file-name.textfile")
	assertLegacyHardlink(t, output, "hardlink", "long-joliet-file-name.textfile")

	info, err := os.Stat(filepath.Join(output, "hardlink"))
	if err != nil || info.ModTime().Unix() != 86401 {
		t.Fatalf("ISO timestamp: %v", err)
	}

	output = unpackFixture(t, legacyFixture(t, "lha_header2.lzh"), "lha")
	assertArchiveLink(t, output, "dir2/symlink1", filepath.FromSlash("../file1"))

	output = unpackFixture(t, legacyFixture(t, "cpio_filename_utf8_jp.cpio"), "cpio")
	assertArchiveFile(t, output, "\u6f22\u5b57.txt", []byte("\x8a\xbf\x8e\x9a\x82\xa9\x82\xc8"))
	assertArchiveFile(t, output, "\u8868.txt", []byte("list"))

	data := legacyFixture(t, "lha_filename_utf16.lzh")

	// The first two entries use UTF-16; later entries also require CP932.
	unicodeOnly := append(bytes.Clone(data[:242]), 0)
	output = unpackFixture(t, unicodeOnly, "lha")

	info, err = os.Stat(filepath.Join(output, "\u00dc\u00d6\u00c4\u00fc\u00f6\u00e4", "\u00e4\u00f6\u00fc\u00c4\u00d6\u00dc.txt"))
	if err != nil || info.Size() != 12 {
		t.Fatalf("LHA Unicode filename: %v", err)
	}

	if runtime.GOOS == "windows" {
		output = unpackFixture(t, data, "lha")

		info, err = os.Stat(filepath.Join(output, "\u00dc\u00d6\u00c4\u00fc\u00f6\u00e4", "\u30c6\u30b9\u30c8.txt"))
		if err != nil || info.Size() != 25 {
			t.Fatalf("LHA CP932 filename: %v", err)
		}
	} else {
		// Static Unix builds have no iconv dependency; unsupported codepages fail explicitly.
		assertArchiveRejected(t, data, "lha", "CP932", false)
	}

	output = unpackFixture(t, legacyFixture(t, "package.deb"), "deb")
	assertArchiveFile(t, output, "debian-binary", []byte("2.0\n"))
	assertArchiveFile(t, output, "data.tar.xz", []byte("opaque data member\n"))

	output = unpackFixture(t, legacyFixture(t, "empty-dir.cpio"), "cpio")

	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty CPIO: %v", err)
	}
}

func TestLegacyLimitsAndCancellation(t *testing.T) {
	fixtures := map[string]string{"cab": "cab_2.cab", "iso": "iso_joliet.iso", "cpio": "newc.cpio", "ar": "ar.ar", "lha": "lha_header2.lzh", "rpm": "cpio_svr4_gzip_rpm.rpm", "Z": "payload.Z", "lzma": "payload.lzma"}

	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			data := legacyFixture(t, fixture)

			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			limits := []ExtractionLimits{{MaxOutput: 1}, {MaxFileSize: 1}}

			if format.Container != "raw" {
				limits = append(limits, ExtractionLimits{MaxEntries: 1})
			}

			for _, limit := range limits {
				err = Unpack(t.Context(), "-", Options{Format: format, Output: t.TempDir(), Limits: &limit}, bytes.NewReader(data))
				if err == nil || !strings.Contains(err.Error(), "limit") {
					t.Fatalf("limit not enforced: %v", err)
				}
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err = Unpack(ctx, "-", Options{Format: format, Output: t.TempDir()}, bytes.NewReader(data))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
}

func TestLegacyCorruption(t *testing.T) {
	fixtures := map[string]string{"cab": "cab_3.cab", "iso": "iso_joliet.iso", "cpio": "newc.cpio", "cpio.gz": "newc.cpio.gz", "cpio.bz2": "newc.cpio.bz2", "cpio.xz": "newc.cpio.xz", "cpio.zst": "newc.cpio.zst", "ar": "ar.ar", "lha": "lha_lh7.lzh", "rpm": "cpio_svr4_gzip_rpm.rpm", "lzma": "payload.lzma"}

	for format, fixture := range fixtures {
		t.Run(format, func(t *testing.T) {
			data := legacyFixture(t, fixture)
			assertArchiveRejected(t, data[:len(data)/2], format, "", false)
			assertArchiveRejected(t, []byte("invalid input"), format, "", true)
		})
	}

	assertArchiveRejected(t, legacyFixture(t, "cab_multi.p1.cab"), "cab", "", true)
	assertArchiveRejected(t, []byte("invalid input"), "Z", "", true)
	assertArchiveRejected(t, []byte{0x1f, 0x9d, 0xff}, "Z", "", true)
	assertArchiveRejected(t, append(legacyFixture(t, "payload.lzma"), 1), "lzma", "", true)
	assertArchiveRejected(t, legacyFixture(t, "newc.cpio"), "cpio.gz", "", true)

	data := legacyFixture(t, "crc.cpio")

	position := bytes.Index(data, []byte("legacy archive payload"))
	data[position] ^= 1

	output := assertArchiveRejected(t, data, "cpio", "checksum", false)

	_, err := os.Stat(filepath.Join(output, "dir/file"))
	if !os.IsNotExist(err) {
		t.Fatalf("corrupt partial file retained: %v", err)
	}

	data = legacyFixture(t, "newc.cpio.gz")
	data[len(data)-8] ^= 1
	assertArchiveRejected(t, data, "cpio.gz", "CRC", false)

	data = legacyFixture(t, "cab_1.cab")
	data[42] = 2
	assertArchiveRejected(t, data, "cab", "compression", false)

	data = legacyFixture(t, "cab_2.cab")
	block := binary.LittleEndian.Uint32(data[36:40])
	data[block] ^= 1
	assertArchiveRejected(t, data, "cab", "Checksum", false)

	data = legacyFixture(t, "lha_lh7.lzh")
	// The first compressed file starts at byte 300, after four directory/link headers.
	data[320] ^= 1
	assertArchiveRejected(t, data, "lha", "", false)

	data = legacyFixture(t, "payload.lzma")
	binary.LittleEndian.PutUint32(data[1:5], 0xffffffff)
	assertArchiveRejected(t, data, "lzma", "", true)

	data = legacyFixture(t, "newc.cpio")
	data[len(data)-1] = 1
	assertArchiveRejected(t, data, "cpio", "padding", false)
}

func TestCPIOLinksAndSafety(t *testing.T) {
	var data bytes.Buffer

	appendCPIOTestEntry(&data, "first", "", 0o100644, 2)
	appendCPIOTestEntry(&data, "second", "linked payload", 0o100644, 2)
	appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)

	output := unpackFixture(t, data.Bytes(), "cpio")
	assertArchiveFile(t, output, "first", []byte("linked payload"))
	assertLegacyHardlink(t, output, "first", "second")

	data.Reset()

	appendCPIOTestEntry(&data, "first", "", 0o100644, 2)
	appendCPIOTestEntry(&data, "second", "", 0o100644, 2)
	appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)

	output = unpackFixture(t, data.Bytes(), "cpio")
	assertArchiveFile(t, output, "first", nil)
	assertLegacyHardlink(t, output, "first", "second")

	names := []string{"../escape", "/absolute", "C:/drive"}

	for _, name := range names {
		data.Reset()

		appendCPIOTestEntry(&data, name, "payload", 0o100644, 1)
		appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)
		assertArchiveRejected(t, data.Bytes(), "cpio", "", true)
	}

	data.Reset()

	appendCPIOTestEntry(&data, "link", "../outside", 0o120777, 1)
	appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)
	assertArchiveRejected(t, data.Bytes(), "cpio", "", true)

	data.Reset()

	appendCPIOTestEntry(&data, "device", "", 0o020600, 1)
	appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)
	assertArchiveRejected(t, data.Bytes(), "cpio", "unsupported", true)

	data.Reset()

	appendCPIOTestEntry(&data, "same", "original", 0o100644, 1)
	appendCPIOTestEntry(&data, "same", "replacement", 0o100644, 1)
	appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)

	output = assertArchiveRejected(t, data.Bytes(), "cpio", "", false)
	assertArchiveFile(t, output, "same", []byte("original"))

	data.Reset()

	appendCPIOTestEntry(&data, "TRAILER!!!", "", 0, 1)

	output = unpackFixture(t, data.Bytes(), "cpio")

	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty CPIO archive: %v", err)
	}

	output = unpackFixture(t, []byte("!<arch>\n"), "ar")

	entries, err = os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty AR archive: %v", err)
	}

	output = unpackFixture(t, []byte{0x1f, 0x9d, 0x90}, "Z")
	assertArchiveFile(t, output, "stdin", nil)
}

func legacyFixture(t *testing.T, name string) []byte {
	t.Helper()

	encoded, err := os.ReadFile("testdata/legacy.json")
	if err != nil {
		t.Fatal(err)
	}

	var fixtures map[string]string

	err = json.Unmarshal(encoded, &fixtures)
	if err != nil {
		t.Fatal(err)
	}

	compressed, err := base64.StdEncoding.DecodeString(fixtures[name])
	if err != nil {
		t.Fatal(err)
	}

	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func assertLegacyHardlink(t *testing.T, output, first, second string) {
	t.Helper()

	original, err := os.Stat(filepath.Join(output, first))
	if err != nil {
		t.Fatal(err)
	}

	linked, err := os.Stat(filepath.Join(output, second))
	if err != nil || !os.SameFile(original, linked) {
		t.Fatalf("hardlink not preserved: %v", err)
	}
}

func appendCPIOTestEntry(data *bytes.Buffer, name, payload string, mode, links int) {
	fmt.Fprintf(data, "070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x", 1, mode, 0, 0, links, 86401, len(payload), 0, 0, 0, 0, len(name)+1, 0)

	data.WriteString(name)
	data.WriteByte(0)

	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}

	data.WriteString(payload)

	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}
}
