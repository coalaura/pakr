//go:build integration

package pak

import (
	"archive/tar"
	"bytes"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/interop"
)

func TestReferenceLegacyStreams(t *testing.T) {
	payloads := [][]byte{nil, []byte("x"), legacyFixture(t, "payload.cpio"), make([]byte, 300000)}
	random := rand.New(rand.NewPCG(1, 2))

	for index := range payloads[3] {
		payloads[3][index] = byte(random.Uint32())
	}

	codecs := []referenceCodec{{format: "lzma", tool: "xz"}, {format: "Z", tool: "gzip"}, {format: "Z", tool: "uncompress"}}

	for _, codec := range codecs {
		t.Run(codec.tool, func(t *testing.T) {
			for index, payload := range payloads {
				input := filepath.Join(t.TempDir(), "input")

				err := os.WriteFile(input, payload, 0o640)
				if err != nil {
					t.Fatal(err)
				}

				format, err := ParseFormat(codec.format)
				if err != nil {
					t.Fatal(err)
				}

				var encoded bytes.Buffer

				err = Pack(t.Context(), []string{input}, Options{Format: format, Output: "-", Level: 1, Threads: 1}, &encoded)
				if err != nil {
					t.Fatal(err)
				}

				produced, verify := interop.Output(t, strconv.Itoa(index), encoded.Bytes())
				if !verify {
					continue
				}

				command := exec.CommandContext(t.Context(), codec.tool, "-d", "-c")

				if codec.format == "lzma" {
					command.Args = append(command.Args, "--format=lzma")
				}

				command.Stdin = bytes.NewReader(produced)

				decoded, err := command.Output()
				if err != nil || !bytes.Equal(decoded, payload) {
					t.Fatalf("reference decode (%d bytes): %v", len(payload), err)
				}
			}
		})
	}
}

func TestReferenceLegacyCreation(t *testing.T) {
	formats := []string{"cpio", "cpio.gz", "cpio.bz2", "cpio.xz", "cpio.zst", "ar", "iso"}
	names := []string{"empty", "small", "123456789012345", "1234567890123456", "a name with spaces", "unicode-\u00e9-\u6f22\u5b57", "UPPER", "upper", strings.Repeat("a", 80) + "1.txt", strings.Repeat("a", 80) + "2.txt", strings.Repeat("b", 255)}

	modified := time.Unix(1700000000, 0)
	fileMode := os.FileMode(0o640)
	directoryMode := os.FileMode(0o750)

	if interop.TargetOS(t) == "windows" {
		// Default Windows filesystems cannot represent case-distinct siblings
		// or Unix permission bits. Verify the metadata that Windows supplies.
		names = slices.DeleteFunc(names, func(name string) bool {
			return name == "UPPER"
		})

		fileMode = 0o666
		directoryMode = 0o777
	}

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "tree")

			err := os.MkdirAll(filepath.Join(source, "nested", "empty-dir"), directoryMode)
			if err == nil {
				err = os.Chmod(filepath.Join(source, "nested", "empty-dir"), directoryMode)
			}

			if err != nil {
				t.Fatal(err)
			}

			inputs := []string{source}
			parent := filepath.Join(source, "nested")

			if name == "ar" {
				inputs = make([]string, 0, len(names))
				parent = base
			}

			for _, member := range names {
				filename := filepath.Join(parent, member)
				payload := []byte(member)

				if member == "empty" {
					payload = nil
				}

				err = os.WriteFile(filename, payload, fileMode)
				if err == nil {
					err = os.Chmod(filename, fileMode)
				}

				if err == nil {
					err = os.Chtimes(filename, modified, modified)
				}

				if err != nil {
					t.Fatal(err)
				}

				if name == "ar" {
					inputs = append(inputs, filename)
				}
			}

			if name != "ar" {
				err = os.Symlink("small", filepath.Join(parent, "link"))
				if err != nil {
					t.Fatal(err)
				}
			}

			format, err := ParseFormat(name)
			if err != nil {
				t.Fatal(err)
			}

			archive := filepath.Join(base, "archive."+name)

			err = Pack(t.Context(), inputs, Options{Format: format, Output: archive, Level: 1, Threads: 2}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}

			encoded, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}

			produced, verify := interop.Output(t, "archive", encoded)
			if !verify {
				return
			}

			err = os.WriteFile(archive, produced, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			tools := []string{"bsdtar"}

			if format.Container == "cpio" {
				tools = append(tools, "cpio", "bsdcpio")
			} else if name == "ar" {
				tools = append(tools, "ar", "llvm-ar")
			} else if name == "iso" {
				tools = append(tools, "xorriso")
			}

			for _, tool := range tools {
				t.Run(tool, func(t *testing.T) {
					requireLegacyReferenceTool(t, tool)

					output := t.TempDir()
					command := exec.CommandContext(t.Context(), tool, "-xpf", archive)

					switch tool {
					case "ar", "llvm-ar":
						// ar extraction may apply umask even to stored permission bits.
						command = exec.CommandContext(t.Context(), "sh", "-c", "umask 0; exec \"$@\"", "sh", tool, "xo", archive)
					case "xorriso":
						command.Args = []string{tool, "-osirrox", "on", "-indev", archive, "-extract", "/", output}
					case "cpio", "bsdcpio":
						data, err := os.ReadFile(archive)
						if err != nil {
							t.Fatal(err)
						}

						if format.Codec != 0 {
							decompressors := map[string]string{"cpio.gz": "gzip", "cpio.bz2": "bzip2", "cpio.xz": "xz", "cpio.zst": "zstd"}
							decoder := exec.CommandContext(t.Context(), decompressors[name], "-dc", archive)

							data, err = decoder.Output()
							if err != nil {
								t.Fatal(err)
							}
						}

						command.Args = []string{tool, "-idm"}
						command.Stdin = bytes.NewReader(data)
					}

					command.Dir = output

					message, err := command.CombinedOutput()
					if err != nil {
						t.Fatalf("reference extract: %v: %s", err, message)
					}

					root := output

					if name != "ar" {
						root = filepath.Join(output, "tree", "nested")
						assertArchiveLink(t, root, "link", "small")

						info, err := os.Stat(filepath.Join(root, "empty-dir"))
						if err != nil || !info.IsDir() || info.Mode().Perm() != directoryMode {
							t.Fatalf("empty directory metadata: %v, %v", info, err)
						}
					}

					for _, member := range names {
						payload := []byte(member)

						if member == "empty" {
							payload = nil
						}

						assertArchiveFile(t, root, member, payload)
						assertLegacyReferenceMetadata(t, filepath.Join(parent, member), filepath.Join(root, member))
					}

					entries, err := os.ReadDir(root)
					count := len(names)

					if name != "ar" {
						count += 2
					}

					if err != nil || len(entries) != count {
						t.Fatalf("unexpected extracted entries: %d, %v", len(entries), err)
					}
				})
			}

			if name != "iso" {
				return
			}

			for _, tool := range tools {
				t.Run("joliet-"+tool, func(t *testing.T) {
					requireLegacyReferenceTool(t, tool)

					output := t.TempDir()
					command := exec.CommandContext(t.Context(), tool, "--options=iso9660:!rockridge", "-xf", archive, "-C", output)

					if tool == "xorriso" {
						command.Args = []string{tool, "-osirrox", "on", "-read_fs", "norock", "-indev", archive, "-extract", "/", output}
					}

					message, err := command.CombinedOutput()
					if err != nil {
						t.Fatalf("Joliet extract: %v: %s", err, message)
					}

					root := filepath.Join(output, "tree", "nested")

					entries, err := os.ReadDir(root)
					if err != nil || len(entries) != len(names)+2 {
						t.Fatalf("Joliet entries: %v, %v", entries, err)
					}

					remaining := make(map[string]bool, len(names))

					for _, member := range names {
						remaining[member] = true
					}

					for _, entry := range entries {
						if entry.Name() == "link" {
							assertArchiveFile(t, root, "link", nil)

							continue
						}

						if entry.IsDir() {
							if entry.Name() != "empty-dir" {
								t.Fatalf("unexpected Joliet directory: %s", entry.Name())
							}

							continue
						}

						payload, err := os.ReadFile(filepath.Join(root, entry.Name()))
						if err != nil {
							t.Fatal(err)
						}

						original := string(payload)

						if len(payload) == 0 {
							original = "empty"
						}

						if !remaining[original] || len([]rune(entry.Name())) > 64 {
							t.Fatalf("lost, duplicated or invalid Joliet member: %s (%q)", entry.Name(), payload)
						}

						if len([]rune(original)) <= 64 && entry.Name() != original {
							t.Fatalf("changed representable Joliet name: %s -> %s", original, entry.Name())
						}

						delete(remaining, original)
					}

					if len(remaining) != 0 {
						t.Fatalf("missing Joliet contents: %v", remaining)
					}
				})
			}
		})
	}
}

func requireLegacyReferenceTool(t *testing.T, tool string) {
	t.Helper()

	_, err := exec.LookPath(tool)
	if err == nil {
		return
	}

	optional := tool == "bsdcpio" || tool == "llvm-ar"
	if tool == "bsdtar" || (os.Getenv("CI") == "true" && !optional) {
		t.Fatalf("required reference tool: %v", err)
	}

	t.Skipf("optional reference tool: %v", err)
}

func assertLegacyReferenceMetadata(t *testing.T, source, destination string) {
	t.Helper()

	original, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}

	extracted, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}

	originalStat, err := tar.FileInfoHeader(original, "")
	if err != nil {
		t.Fatal(err)
	}

	extractedStat, err := tar.FileInfoHeader(extracted, "")
	if err != nil {
		t.Fatal(err)
	}

	if original.Mode() != extracted.Mode() || !original.ModTime().Equal(extracted.ModTime()) || originalStat.Uid != extractedStat.Uid || originalStat.Gid != extractedStat.Gid {
		t.Fatalf("metadata changed for %s: mode %v, mtime %v, uid/gid %d/%d", destination, extracted.Mode(), extracted.ModTime(), extractedStat.Uid, extractedStat.Gid)
	}
}
