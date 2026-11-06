package pak

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestZipAdvancedInterop(t *testing.T) {
	methods := []string{"bzip2", "lzma", "zstd", "ppmd8"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			data := archiveFixture(t, "test_read_format_zip_"+method+".zipx")
			output := unpackFixture(t, data, "zipx")

			actual, err := os.ReadFile(filepath.Join(output, "vimrc"))
			if err != nil || len(actual) != 912 || crc32.ChecksumIEEE(actual) != 0xba8e3baa {
				t.Fatalf("reference payload mismatch: %v", err)
			}
		})
	}
}

func TestZipAdvancedMixed(t *testing.T) {
	data := mixedZipFixture(t)
	variants := map[string][]byte{"zip": data, "zipx": data, "sfx": append([]byte("self-extracting stub"), data...), "zip64": zip64Fixture(data)}

	for name, encoded := range variants {
		t.Run(name, func(t *testing.T) {
			format := "zip"

			if name == "zipx" {
				format = "zipx"
			}

			output := unpackFixture(t, encoded, format)

			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 7 {
				t.Fatalf("missing mixed entries: %v", err)
			}

			for _, entry := range entries {
				actual, readErr := os.ReadFile(filepath.Join(output, entry.Name()))
				if readErr != nil {
					t.Fatal(readErr)
				}

				if strings.HasPrefix(entry.Name(), "vimrc-") {
					if len(actual) != 912 || crc32.ChecksumIEEE(actual) != 0xba8e3baa {
						t.Fatal("mixed reference payload mismatch")
					}
				} else if string(actual) != "mixed payload" {
					t.Fatalf("unexpected payload for %q", entry.Name())
				}
			}
		})
	}
}

func TestExtractionOnlyFormats(t *testing.T) {
	names := []string{"zipx", "rar"}

	for _, name := range names {
		format, err := ParseFormat(name)
		if err != nil {
			t.Fatal(err)
		}

		inferred, err := InferFormat("archive." + name)
		if err != nil || inferred != format {
			t.Fatalf("format inference: %v", err)
		}

		err = Pack(t.Context(), []string{"missing"}, Options{Format: format}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "extraction-only") {
			t.Fatalf("creation accepted: %v", err)
		}

		err = Pack(t.Context(), []string{"missing"}, Options{Format: Format{Name: "zip", Container: "zip"}, Output: filepath.Join(t.TempDir(), "archive."+name)}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "extraction-only") {
			t.Fatalf("creation extension accepted: %v", err)
		}
	}

	var encoded bytes.Buffer

	writer := zip.NewWriter(&encoded)

	entry, err := writer.Create("ordinary")
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.WriteString(entry, "ordinary deflate")
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	unpackFixture(t, encoded.Bytes(), "zipx")

	data := archiveFixture(t, "test_read_format_rar5_stored.rar")

	unpackFixture(t, data, "")
	unpackFixture(t, mixedZipFixture(t), "")

	for _, name := range names {
		archive := filepath.Join(t.TempDir(), "archive."+name)
		payload := data

		if name == "zipx" {
			payload = encoded.Bytes()
		}

		err = os.WriteFile(archive, payload, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		err = Unpack(t.Context(), archive, Options{Output: t.TempDir(), Threads: 4}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRARInterop(t *testing.T) {
	names := []string{"rar", "rar_unicode", "rar5_stored", "rar5_compressed", "rar5_hardlink", "rar5_symlink", "rar5_unicode", "rar5_multiple_files_solid"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			output := unpackFixture(t, archiveFixture(t, "test_read_format_"+name+".rar"), "rar")

			switch name {
			case "rar":
				assertArchiveFile(t, output, "test.txt", []byte("test text document\r\n"))
				assertArchiveFile(t, output, "testdir/test.txt", []byte("test text document\r\n"))
				assertArchiveLink(t, output, "testlink", "test.txt")

				info, err := os.Stat(filepath.Join(output, "testemptydir"))
				if err != nil || !info.IsDir() {
					t.Fatalf("missing directory: %v", err)
				}
			case "rar_unicode":
				actual, err := os.ReadFile(filepath.Join(output, "abcdefghijklmnopqrs\u30c6\u30b9\u30c8.txt"))
				if err != nil || len(actual) != 16 {
					t.Fatalf("compressed Unicode file: %v", err)
				}
			case "rar5_stored":
				assertArchiveFile(t, output, "helloworld.txt", []byte("hello libarchive test suite!\n"))
			case "rar5_compressed":
				assertArchiveFile(t, output, "test.bin", rarTestPayload(1200, 0))
			case "rar5_multiple_files_solid":
				for number := 1; number <= 4; number++ {
					assertArchiveFile(t, output, "test"+strconv.Itoa(number)+".bin", rarTestPayload(4096, number))
				}
			case "rar5_hardlink":
				assertArchiveFile(t, output, "hardlink.txt", []byte("1234\n"))

				original, err := os.Stat(filepath.Join(output, "file.txt"))
				if err != nil {
					t.Fatal(err)
				}

				linked, err := os.Stat(filepath.Join(output, "hardlink.txt"))
				if err != nil || !os.SameFile(original, linked) {
					t.Fatalf("hardlink not preserved: %v", err)
				}
			case "rar5_symlink":
				assertArchiveLink(t, output, "symlink.txt", "file.txt")
				assertArchiveLink(t, output, "dirlink", "dir")
			case "rar5_unicode":
				assertArchiveFile(t, output, "\U0001f44b\U0001f30e.txt", []byte("Hello, world!"))
			}
		})
	}
}

func TestRARRejectedArchives(t *testing.T) {
	names := []string{"rar4_encrypted", "rar4_encrypted_filenames", "rar4_solid_encrypted", "rar_filter_incomplete_block", "rar5_encrypted", "rar5_encrypted_filenames", "rar5_solid_encrypted", "rar5_bad_tables"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			data := archiveFixture(t, "test_read_format_"+name+".rar")

			err := Unpack(t.Context(), "-", Options{Format: Format{Name: "rar", Container: "rar"}, Output: t.TempDir()}, bytes.NewReader(data))
			if err == nil {
				t.Fatal("unsupported or corrupted archive accepted")
			}

			if strings.Contains(name, "encrypted") && !strings.Contains(strings.ToLower(err.Error()), "encrypt") {
				t.Fatalf("missing encryption diagnostic: %v", err)
			}
		})
	}
}

func TestNativeExtractionLimitsAndCancellation(t *testing.T) {
	fixtures := map[string][]byte{"zipx": mixedZipFixture(t), "rar": archiveFixture(t, "test_read_format_rar5_multiple_files_solid.rar")}

	for name, data := range fixtures {
		t.Run(name, func(t *testing.T) {
			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			limits := []ExtractionLimits{{MaxEntries: 1}, {MaxOutput: 1}, {MaxFileSize: 1}}

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
				t.Fatalf("cancellation not propagated: %v", err)
			}

			output := unpackFixture(t, data, name)

			err = Unpack(t.Context(), "-", Options{Format: format, Output: output}, bytes.NewReader(data))
			if err == nil {
				t.Fatal("existing files overwritten")
			}
		})
	}
}

func archiveFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile("testdata/archives.json")
	if err != nil {
		t.Fatal(err)
	}

	var fixtures map[string]string

	err = json.Unmarshal(data, &fixtures)
	if err != nil {
		t.Fatal(err)
	}

	encoded, exists := fixtures[name]
	if !exists {
		t.Fatalf("missing fixture %s", name)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}

	return decoded
}

func unpackFixture(t *testing.T, data []byte, name string) string {
	t.Helper()

	var (
		format Format
		err    error
	)

	if name != "" {
		format, err = ParseFormat(name)
		if err != nil {
			t.Fatal(err)
		}
	}

	output := t.TempDir()

	err = Unpack(t.Context(), "-", Options{Format: format, Output: output, Threads: 4}, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	return output
}

func mixedZipFixture(t *testing.T) []byte {
	t.Helper()

	var encoded bytes.Buffer

	writer := zip.NewWriter(&encoded)
	methods := []string{"bzip2", "lzma", "zstd", "ppmd8"}

	for _, method := range methods {
		data := archiveFixture(t, "test_read_format_zip_"+method+".zipx")

		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}

		file := archive.File[0]
		file.Name = "vimrc-" + method

		err = writer.Copy(file)
		if err != nil {
			t.Fatal(err)
		}
	}

	var compressed bytes.Buffer

	compressor, err := native.NewWriter(&compressed, native.XZ, 3, 1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.WriteString(compressor, "mixed payload")
	if err != nil {
		t.Fatal(err)
	}

	err = compressor.Close()
	if err != nil {
		t.Fatal(err)
	}

	header := zip.FileHeader{Name: "\u65e5\u672c\u8a9e.txt", Method: 95, Flags: 0x800, CRC32: crc32.ChecksumIEEE([]byte("mixed payload")), CompressedSize64: uint64(compressed.Len()), UncompressedSize64: 13}

	entry, err := writer.CreateRaw(&header)
	if err != nil {
		t.Fatal(err)
	}

	_, err = entry.Write(compressed.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	ordinary := []uint16{zip.Store, zip.Deflate}

	for _, method := range ordinary {
		entry, err = writer.CreateHeader(&zip.FileHeader{Name: "ordinary-" + strconv.Itoa(int(method)), Method: method})
		if err != nil {
			t.Fatal(err)
		}

		_, err = io.WriteString(entry, "mixed payload")
		if err != nil {
			t.Fatal(err)
		}
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}

func zip64Fixture(data []byte) []byte {
	end := len(data) - 22

	record := make([]byte, 76)
	binary.LittleEndian.PutUint32(record, 0x06064b50)
	binary.LittleEndian.PutUint64(record[4:], 44)

	count := uint64(binary.LittleEndian.Uint16(data[end+10:]))

	binary.LittleEndian.PutUint64(record[24:], count)
	binary.LittleEndian.PutUint64(record[32:], count)
	binary.LittleEndian.PutUint64(record[40:], uint64(binary.LittleEndian.Uint32(data[end+12:])))
	binary.LittleEndian.PutUint64(record[48:], uint64(binary.LittleEndian.Uint32(data[end+16:])))
	binary.LittleEndian.PutUint32(record[56:], 0x07064b50)
	binary.LittleEndian.PutUint64(record[64:], uint64(end))
	binary.LittleEndian.PutUint32(record[72:], 1)

	result := make([]byte, 0, len(data)+len(record))
	result = append(result, data[:end]...)
	result = append(result, record...)
	result = append(result, data[end:]...)
	binary.LittleEndian.PutUint16(result[len(result)-12:], 0xffff)

	return result
}

func rarTestPayload(size, number int) []byte {
	data := make([]byte, size)

	for index := 0; index < size/4; index++ {
		value := index + 1
		binary.LittleEndian.PutUint32(data[index*4:], uint32(max(0, value*value-3*value+1+number)))
	}

	return data
}

func assertArchiveFile(t *testing.T, output, name string, expected []byte) {
	t.Helper()

	actual, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(name)))
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("%s payload mismatch: %v", name, err)
	}
}

func assertArchiveLink(t *testing.T, output, name, expected string) {
	t.Helper()

	actual, err := os.Readlink(filepath.Join(output, name))
	if err != nil || actual != expected {
		t.Fatalf("%s link mismatch: %q, %v", name, actual, err)
	}
}
