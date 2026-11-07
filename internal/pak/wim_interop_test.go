//go:build integration

package pak

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/interop"
)

func TestWIMImagexWindowsSecurity(t *testing.T) {
	extensions := []string{"wim", "esd"}

	for _, extension := range extensions {
		t.Run(extension, func(t *testing.T) {
			data := interop.Reference(t, "security."+extension, func() []byte {
				imagex := referenceWIMImagex(t)

				// The pinned upstream fixture has Windows SIDs and a valid empty DACL.
				fixture := filepath.Join(filepath.Dir(imagex), "tests", "wims", "empty_dacl.wim")
				output := filepath.Join(t.TempDir(), "security."+extension)
				arguments := []string{"export", fixture, "1", output, "--compress=LZX", "--check"}

				if extension == "esd" {
					arguments = append(arguments, "--solid", "--solid-compress=LZMS", "--solid-chunk-size=1M")
				}

				runWIMImagex(t, imagex, arguments...)
				runWIMImagex(t, imagex, "verify", output)

				return readWIMFixture(t, output)
			})

			archive := filepath.Join(t.TempDir(), "security."+extension)
			mustWIMFile(t, archive, data)

			output := t.TempDir()

			err := Unpack(t.Context(), archive, Options{Output: output}, nil)
			if err != nil {
				t.Fatal(err)
			}

			filename := filepath.Join(output, "file")
			if string(readWIMFixture(t, filename)) != "1\n" {
				t.Fatal("Windows security fixture content mismatch")
			}

			info, err := os.Stat(filename)
			if err != nil {
				t.Fatal(err)
			}

			expected := time.Date(2013, 9, 16, 20, 50, 8, 0, time.UTC)
			if info.ModTime().Unix() != expected.Unix() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
				t.Fatalf("Windows metadata mapping: mode=%v time=%v", info.Mode(), info.ModTime())
			}
		})
	}
}

func TestWIMImagexRepresentable(t *testing.T) {
	extensions := []string{"wim", "esd"}

	for _, extension := range extensions {
		t.Run(extension, func(t *testing.T) {
			data := interop.Reference(t, "entries."+extension, func() []byte {
				imagex := referenceWIMImagex(t)
				source := t.TempDir()

				filename := filepath.Join(source, "payload")
				mustWIMFile(t, filename, []byte("independent contents\n"))
				mustWIMDirectory(t, filepath.Join(source, "empty"))

				err := os.Chmod(filename, 0o444)
				if err != nil {
					t.Fatal(err)
				}

				stamp := time.Unix(1700000000, 0)

				err = os.Chtimes(filename, stamp, stamp)
				if err != nil {
					t.Fatal(err)
				}

				err = os.Link(filename, filepath.Join(source, "hard"))
				if err != nil {
					t.Fatal(err)
				}

				links := map[string]string{"file-link": "payload", "dir-link": "empty"}

				for name, target := range links {
					err = os.Symlink(target, filepath.Join(source, name))
					if err != nil {
						t.Fatal(err)
					}
				}

				archive := filepath.Join(t.TempDir(), "entries."+extension)
				arguments := []string{"capture", source, archive, "--no-acls", "--unix-data", "--norpfix", "--compress=LZX", "--check"}

				if extension == "esd" {
					arguments = append(arguments, "--solid", "--solid-compress=LZMS", "--solid-chunk-size=1M")
				}

				runWIMImagex(t, imagex, arguments...)
				runWIMImagex(t, imagex, "verify", archive)

				return readWIMFixture(t, archive)
			})

			archive := filepath.Join(t.TempDir(), "entries."+extension)
			mustWIMFile(t, archive, data)

			output := t.TempDir()

			err := Unpack(t.Context(), archive, Options{Output: output}, nil)
			if err != nil {
				t.Fatal(err)
			}

			files := []string{"payload", "hard", "file-link"}

			for _, name := range files {
				if string(readWIMFixture(t, filepath.Join(output, name))) != "independent contents\n" {
					t.Fatalf("content mismatch: %s", name)
				}
			}

			original, err := os.Stat(filepath.Join(output, "payload"))
			if err != nil {
				t.Fatal(err)
			}

			hard, err := os.Stat(filepath.Join(output, "hard"))
			if err != nil || !os.SameFile(original, hard) {
				t.Fatalf("hardlink not preserved: %v", err)
			}

			if original.ModTime().Unix() != 1700000000 || original.Mode().Perm()&0o222 != 0 {
				t.Fatalf("file metadata mismatch: %v, %v", original.Mode(), original.ModTime())
			}

			directories := []string{"empty", "dir-link"}

			for _, name := range directories {
				info, statErr := os.Stat(filepath.Join(output, name))
				if statErr != nil || !info.IsDir() {
					t.Fatalf("directory mismatch: %s, %v", name, statErr)
				}
			}

			links := map[string]string{"file-link": "payload", "dir-link": "empty"}

			for name, expected := range links {
				target, linkErr := os.Readlink(filepath.Join(output, name))
				if linkErr != nil || filepath.ToSlash(target) != expected {
					t.Fatalf("link mismatch: %s -> %s, %v", name, target, linkErr)
				}
			}
		})
	}
}

func TestWIMImagexUnsupportedMetadata(t *testing.T) {
	cases := map[string]string{
		"escape":          "unsafe archive path",
		"absolute":        "relative",
		"xattrs":          "extended attributes",
		"ads":             "named stream",
		"encrypted":       "unsupported",
		"junction":        "reparse point",
		"unknown-reparse": "reparse point",
	}

	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			data := interop.Reference(t, "unsupported.wim", func() []byte {
				imagex := referenceWIMImagex(t)

				if name == "xattrs" {
					fixture := filepath.Join(filepath.Dir(imagex), "tests", "wims", "linux_xattrs_old.wim")
					runWIMImagex(t, imagex, "verify", fixture)

					return readWIMFixture(t, fixture)
				}

				source := t.TempDir()
				mustWIMFile(t, filepath.Join(source, "entry"), []byte("contents must never disappear\n"))

				if name == "escape" || name == "absolute" {
					target := "../../outside"

					if name == "absolute" {
						target = "/outside"
					}

					err := os.Symlink(target, filepath.Join(source, "link"))
					if err != nil {
						t.Fatal(err)
					}
				}

				archive := filepath.Join(t.TempDir(), "unsupported.wim")
				runWIMImagex(t, imagex, "capture", source, archive, "--compress=none", "--no-acls", "--norpfix")
				captured := readWIMFixture(t, archive)

				if name != "escape" && name != "absolute" {
					captured = unsupportedWIMFixture(t, captured, name)
					mustWIMFile(t, archive, captured)
				}

				// Independent parsing and resource verification must accept the fixture.
				runWIMImagex(t, imagex, "verify", archive)
				runWIMImagex(t, imagex, "dir", archive, "--detailed")

				return captured
			})

			archive := filepath.Join(t.TempDir(), "unsupported.wim")
			mustWIMFile(t, archive, data)

			output := t.TempDir()

			err := Unpack(t.Context(), archive, Options{Output: output}, nil)
			if err == nil || !strings.Contains(err.Error(), message) {
				t.Fatalf("expected %q rejection, got %v", message, err)
			}

			assertWIMEmpty(t, output)
		})
	}
}

func referenceWIMImagex(t *testing.T) string {
	t.Helper()

	imagex := os.Getenv("PAKR_WIMLIB_IMAGEX")
	if imagex == "" {
		t.Skip("requires the pristine wimlib-imagex reference build")
	}

	return imagex
}

func readWIMFixture(t *testing.T, filename string) []byte {
	t.Helper()

	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// Modify only metadata in a tiny, uncompressed, single-file imagex capture.
// The disk layouts are wimlib's dentry.c and blob_table.c; append replacement
// metadata/table resources and update their sizes and SHA-1, preserving file data.
func unsupportedWIMFixture(t *testing.T, data []byte, kind string) []byte {
	t.Helper()

	tableOffset := binary.LittleEndian.Uint64(data[56:])
	tableSize := binary.LittleEndian.Uint64(data[48:]) & (1<<56 - 1)
	table := bytes.Clone(data[tableOffset : tableOffset+tableSize])

	for offset := 0; offset < len(table); offset += 50 {
		resource := table[offset : offset+50]
		if resource[7] != 2 {
			continue
		}

		start := binary.LittleEndian.Uint64(resource[8:])
		size := binary.LittleEndian.Uint64(resource[16:])
		metadata := bytes.Clone(data[start : start+size])
		root := (int(binary.LittleEndian.Uint32(metadata)) + 7) &^ 7
		child := int(binary.LittleEndian.Uint64(metadata[root+16:]))

		entry := metadata[child:]
		if binary.LittleEndian.Uint16(entry[96:]) != 0 || binary.LittleEndian.Uint16(entry[100:]) != 10 {
			t.Fatal("unexpected reference fixture layout")
		}

		switch kind {
		case "ads":
			// The named stream shares the nonempty main resource, not an empty placeholder.
			stream := make([]byte, 48)
			binary.LittleEndian.PutUint64(stream, uint64(len(stream)))
			copy(stream[16:36], entry[64:84])
			binary.LittleEndian.PutUint16(stream[36:], 6)
			copy(stream[38:], []byte{'a', 0, 'd', 0, 's', 0})
			binary.LittleEndian.PutUint16(entry[96:], 1)

			end := child + int(binary.LittleEndian.Uint64(entry))
			metadata = append(metadata[:end:end], append(stream, metadata[end:]...)...)
		case "encrypted":
			binary.LittleEndian.PutUint32(entry[8:], 0x4020)
		case "junction", "unknown-reparse":
			binary.LittleEndian.PutUint32(entry[8:], 0x420)
			tag := uint32(0xa0000003)

			if kind == "unknown-reparse" {
				tag = 0x80000042
			}

			binary.LittleEndian.PutUint32(entry[88:], tag)
		}

		binary.LittleEndian.PutUint64(resource, uint64(len(metadata)))
		resource[7] = 2
		binary.LittleEndian.PutUint64(resource[8:], uint64(len(data)))
		binary.LittleEndian.PutUint64(resource[16:], uint64(len(metadata)))
		digest := sha1.Sum(metadata)
		copy(resource[30:], digest[:])

		data = append(data, metadata...)
		binary.LittleEndian.PutUint64(data[56:], uint64(len(data)))

		return append(data, table...)
	}

	t.Fatal("reference fixture lacks an uncompressed metadata resource")

	return nil
}
