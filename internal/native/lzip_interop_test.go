//go:build integration

package native

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
)

func TestLzipCLI(t *testing.T) {
	cli := os.Getenv("LZIP_CLI")
	if cli == "" {
		cli = "lzip"
	}

	levels := []int{1, 3, 9}
	sizes := []int{0, 1, 3200, 4<<20 + 37}

	for _, level := range levels {
		for _, size := range sizes {
			data := testData(size)
			compressed := encodeLzip(t, data, level)
			name := strconv.Itoa(level) + "-" + strconv.Itoa(size)

			produced, verify := interop.Output(t, name, compressed)
			if verify {
				command := exec.CommandContext(t.Context(), cli, "-d", "-c")
				command.Stdin = bytes.NewReader(produced)

				actual, err := command.Output()
				if err != nil {
					t.Fatal(err)
				}

				if !bytes.Equal(actual, data) {
					t.Fatalf("official decoder mismatch: level %d size %d", level, size)
				}
			}

			compressed = interop.Reference(t, name, func() []byte {
				command := exec.CommandContext(t.Context(), cli, "-c", "-"+strconv.Itoa(level))
				command.Stdin = bytes.NewReader(data)

				encoded, err := command.Output()
				if err != nil {
					t.Fatal(err)
				}

				return encoded
			})

			// Independently produced members must also concatenate without padding.
			joined := append(bytes.Clone(compressed), compressed...)

			reader, err := NewLzipReader(t.Context(), bytes.NewReader(joined), 1<<30)
			if err != nil {
				t.Fatal(err)
			}

			actual, err := io.ReadAll(reader)

			reader.Close()

			if err != nil || !bytes.Equal(actual, bytes.Repeat(data, 2)) {
				t.Fatalf("official encoder mismatch: level %d size %d: %v", level, size, err)
			}
		}
	}
}
