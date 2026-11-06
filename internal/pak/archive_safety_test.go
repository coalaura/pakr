package pak

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestZipAdvancedPreflight(t *testing.T) {
	names := [][]string{{"same", "same"}, {"parent", "parent/child"}, {"../escape"}, {"/absolute"}, {"C:/drive"}}

	for _, paths := range names {
		t.Run(paths[0], func(t *testing.T) {
			var encoded bytes.Buffer

			writer := zip.NewWriter(&encoded)

			for _, name := range paths {
				_, err := writer.CreateRaw(&zip.FileHeader{Name: name, Method: 12})
				if err != nil {
					t.Fatal(err)
				}
			}

			err := writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			assertArchiveRejected(t, encoded.Bytes(), "zipx", "", true)
		})
	}

	methods := []uint16{9, 94, 96, 97, 65535}

	for _, method := range methods {
		t.Run(strconv.Itoa(int(method)), func(t *testing.T) {
			data := archiveFixture(t, "test_read_format_zip_bzip2.zipx")

			central := bytes.Index(data, []byte("PK\x01\x02"))

			binary.LittleEndian.PutUint16(data[8:], method)
			binary.LittleEndian.PutUint16(data[central+10:], method)

			assertArchiveRejected(t, data, "zipx", "unsupported ZIP compression method", true)
		})
	}
}

func TestZipAdvancedCorruption(t *testing.T) {
	mutations := []string{"crc", "payload", "header", "truncated", "encrypted", "size-overflow"}

	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			data := archiveFixture(t, "test_read_format_zip_bzip2.zipx")

			central := bytes.Index(data, []byte("PK\x01\x02"))
			empty := false

			switch mutation {
			case "crc":
				data[14] ^= 1
				data[central+16] ^= 1
			case "payload":
				archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}

				offset, err := archive.File[0].DataOffset()
				if err != nil {
					t.Fatal(err)
				}

				data[offset+5] ^= 0xff
			case "header":
				data[0] = 0
			case "truncated":
				data = data[:len(data)-30]
			case "encrypted":
				data[central+8] |= 1
				empty = true
			case "size-overflow":
				// ZIP64 values are tested separately by the common size preflight;
				// this inconsistent central directory must also fail closed.
				binary.LittleEndian.PutUint32(data[central+24:], 0xffffffff)
				empty = true
			}

			output := assertArchiveRejected(t, data, "zipx", "", empty)

			_, err := os.Lstat(filepath.Join(output, "vimrc"))
			if !os.IsNotExist(err) {
				t.Fatalf("partial file retained: %v", err)
			}
		})
	}
}

func TestRARFramingAndCorruption(t *testing.T) {
	names := []string{"rar", "rar5_stored"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			original := archiveFixture(t, "test_read_format_"+name+".rar")

			for length := range len(original) {
				_, err := rarFormat(t.Context(), bytes.NewReader(original[:length]), int64(length))
				if err == nil {
					t.Fatalf("truncation at %d accepted", length)
				}
			}

			assertArchiveRejected(t, original[:len(original)-1], "rar", "", true)

			data := bytes.Clone(original)
			data[10] ^= 1

			assertArchiveRejected(t, data, "rar", "", true)

			data = bytes.Clone(original)
			payload := []byte("test text document")
			filename := "test.txt"

			if name == "rar5_stored" {
				payload = []byte("hello libarchive")
				filename = "helloworld.txt"
			}

			position := bytes.Index(data, payload)
			if position < 0 {
				t.Fatal("missing reference payload")
			}

			data[position] ^= 1
			output := assertArchiveRejected(t, data, "rar", "", false)

			_, err := os.Stat(filepath.Join(output, filename))
			if !os.IsNotExist(err) {
				t.Fatalf("corrupt partial file retained: %v", err)
			}

			data = bytes.Clone(original)

			if name == "rar" {
				data[10] |= 1
			} else {
				data[16] |= 1
			}

			repairRARHeaders(t, data)
			assertArchiveRejected(t, data, "rar", "multipart", true)
		})
	}
}

func TestRARChecksumAndSolidRejection(t *testing.T) {
	data := archiveFixture(t, "test_read_format_rar5_stored.rar")

	var checksum [4]byte

	binary.LittleEndian.PutUint32(checksum[:], crc32.ChecksumIEEE([]byte("hello libarchive test suite!\n")))

	position := bytes.Index(data, checksum[:])
	if position < 0 {
		t.Fatal("missing reference CRC")
	}

	clear(data[position : position+4])
	repairRARHeaders(t, data)
	assertArchiveRejected(t, data, "rar", "CRC", false)

	data = archiveFixture(t, "test_read_format_rar.rar")
	position = 7 + int(binary.LittleEndian.Uint16(data[12:]))
	data[position+3] |= 0x10

	repairRARHeaders(t, data)
	assertArchiveRejected(t, data, "rar", "solid", true)
}

func TestRARUnsupportedAndMalformedHeaders(t *testing.T) {
	data := archiveFixture(t, "test_read_format_rar5_multiarchive_solid.part04.rar")
	assertArchiveRejected(t, data, "rar", "multipart", true)

	data = archiveFixture(t, "test_read_format_rar5_symlink.rar")

	// Change the first link's redirection type to an unsupported junction.
	data[102] = 3

	repairRARHeaders(t, data)
	assertArchiveRejected(t, data, "rar", "unsupported RAR5 link", true)

	data = archiveFixture(t, "test_read_format_rar5_stored.rar")

	// Keep valid framing and checksums, but declare a name beyond the header.
	data[46] = 0x7f

	repairRARHeaders(t, data)
	assertArchiveRejected(t, data, "rar", "", true)
}

func TestRARPathSafety(t *testing.T) {
	names := []string{"rar", "rar5_stored", "rar5_symlink", "rar5_hardlink"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			original := archiveFixture(t, "test_read_format_"+name+".rar")
			from := "test.txt"
			targets := []string{"../x.txt", "/bad.txt"}

			if name == "rar5_stored" {
				from = "helloworld.txt"
				targets = []string{"../outside.txt", "/absolute.txt/"}
			} else if name != "rar" {
				from = "file.txt"
			}

			for _, target := range targets {
				data := bytes.Clone(original)
				position := bytes.Index(data, []byte(from))

				if name == "rar5_symlink" || name == "rar5_hardlink" {
					position = bytes.LastIndex(data, []byte(from))
				}

				if position < 0 || len(from) != len(target) {
					t.Fatal("invalid fixture mutation")
				}

				copy(data[position:], target)
				repairRARHeaders(t, data)
				assertArchiveRejected(t, data, "rar", "", false)
			}
		})
	}
}

func TestNativeExtractionSymlinkTraversal(t *testing.T) {
	fixtures := map[string][]byte{"rar": archiveFixture(t, "test_read_format_rar5_stored.rar"), "zipx": archiveFixture(t, "test_read_format_zip_bzip2.zipx")}

	for format, data := range fixtures {
		t.Run(format, func(t *testing.T) {
			output := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside")

			name := "vimrc"

			if format == "rar" {
				name = "helloworld.txt"
			}

			err := os.Symlink(outside, filepath.Join(output, name))
			if err != nil {
				t.Fatal(err)
			}

			parsed, err := ParseFormat(format)
			if err != nil {
				t.Fatal(err)
			}

			err = Unpack(t.Context(), "-", Options{Format: parsed, Output: output}, bytes.NewReader(data))
			if err == nil {
				t.Fatal("symlink followed")
			}

			_, err = os.Stat(outside)
			if !os.IsNotExist(err) {
				t.Fatalf("wrote outside extraction root: %v", err)
			}
		})
	}
}

func assertArchiveRejected(t *testing.T, data []byte, name, message string, empty bool) string {
	t.Helper()

	format, err := ParseFormat(name)
	if err != nil {
		t.Fatal(err)
	}

	output := t.TempDir()

	err = Unpack(t.Context(), "-", Options{Format: format, Output: output}, bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), message) {
		t.Fatalf("expected rejection containing %q, got %v", message, err)
	}

	if empty {
		entries, readErr := os.ReadDir(output)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("preflight wrote entries: %v", readErr)
		}
	}

	return output
}

func repairRARHeaders(t *testing.T, data []byte) {
	t.Helper()

	if data[6] == 0 {
		for offset := 7; offset < len(data); {
			length := int(binary.LittleEndian.Uint16(data[offset+5:]))

			header := data[offset : offset+length]
			binary.LittleEndian.PutUint16(header, uint16(crc32.ChecksumIEEE(header[2:])))

			offset += length

			if binary.LittleEndian.Uint16(header[3:])&0x8000 != 0 {
				offset += int(binary.LittleEndian.Uint32(header[7:]))
			}
		}

		return
	}

	for offset := 8; offset < len(data); {
		length, count := binary.Uvarint(data[offset+4:])
		total := 4 + count + int(length)
		header := data[offset : offset+total]
		binary.LittleEndian.PutUint32(header, crc32.ChecksumIEEE(header[4:]))
		fields := header[4+count:]

		_, err := rarInteger(&fields)
		if err != nil {
			t.Fatal(err)
		}

		flags, err := rarInteger(&fields)
		if err != nil {
			t.Fatal(err)
		}

		if flags&1 != 0 {
			_, err = rarInteger(&fields)
			if err != nil {
				t.Fatal(err)
			}
		}

		offset += total

		if flags&2 != 0 {
			size, readErr := rarInteger(&fields)
			if readErr != nil || size > uint64(len(data)-offset) {
				t.Fatalf("invalid fixture data: %v", readErr)
			}

			offset += int(size)
		}
	}
}
