//go:build integration

package pak

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
	"github.com/coalaura/pakr/internal/native"
)

func TestReferenceZipStreaming(t *testing.T) {
	data := archiveStreamTestData(9 << 20)
	base := t.TempDir()
	inputs := make([]string, 2)

	for index := range inputs {
		inputs[index] = filepath.Join(base, "large-\u00e9-"+strconv.Itoa(index))

		err := os.WriteFile(inputs[index], data, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	archive := filepath.Join(base, "archive.zip")
	options := Options{Format: Format{Name: "zip", Container: "zip"}, Output: archive, Threads: 2, Level: 3}

	err := Pack(t.Context(), inputs, options, io.Discard)
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

	for _, input := range inputs {
		decoder := exec.CommandContext(t.Context(), "bsdtar", "-xOf", "-", filepath.Base(input))
		decoder.Stdin = bytes.NewReader(produced)

		actual, decodeErr := decoder.Output()
		if decodeErr != nil || !bytes.Equal(actual, data) {
			t.Fatalf("independent large ZIP decode: %v", decodeErr)
		}
	}
}

func TestReferenceBzip2Layouts(t *testing.T) {
	data := archiveStreamTestData(4 << 20)
	layouts := []int{1, 2}

	for _, streams := range layouts {
		t.Run(strconv.Itoa(streams), func(t *testing.T) {
			encoded := interop.Reference(t, "archive", func() []byte {
				var output bytes.Buffer

				for index := range streams {
					encoder := exec.CommandContext(t.Context(), "bzip2", "-3c")
					encoder.Stdin = bytes.NewReader(data[index*len(data)/streams : (index+1)*len(data)/streams])
					encoder.Stdout = &output

					err := encoder.Run()
					if err != nil {
						t.Fatal(err)
					}
				}

				return output.Bytes()
			})

			base := t.TempDir()
			archive := filepath.Join(base, "reference.bz2")

			err := os.WriteFile(archive, encoded, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			options := Options{Format: Format{Name: "bz2", Codec: native.Bzip2}, Output: filepath.Join(base, "output"), Threads: 3}

			err = Unpack(t.Context(), archive, options, nil)
			if err != nil {
				t.Fatal(err)
			}

			actual, err := os.ReadFile(filepath.Join(options.Output, "reference"))
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("independent bzip2 stream layout: %v", err)
			}
		})
	}
}
