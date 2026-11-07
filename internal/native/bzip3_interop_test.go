//go:build integration

package native

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
)

// Integration-only: the largest native state requires multiple GiB of RAM.
func TestBzip3BlockLevels(t *testing.T) {
	data := testData(bzip3Minimum + 1)

	for level := 1; level <= 9; level++ {
		compressed := encodeBzip3(t, data, level, 1, len(data))

		declared := binary.LittleEndian.Uint32(compressed[5:])
		if int(declared) != bzip3Sizes[level-1] {
			t.Fatalf("level %d declared %d", level, declared)
		}

		actual := decodeBzip3(t, compressed, 1)
		if !bytes.Equal(actual, data) {
			t.Fatalf("level %d round trip mismatch", level)
		}

		runtime.GC()
	}
}

func TestBzip3CLI(t *testing.T) {
	cli := os.Getenv("BZIP3_CLI")
	if cli == "" {
		cli = "bzip3"
	}

	sizes := []int{0, 23, 3200, 3<<20 + 37, 17<<20 + 37}

	for _, size := range sizes {
		data := testData(size)
		compressed := encodeBzip3(t, data, 1, 4, 317)
		name := strconv.Itoa(size)

		produced, verify := interop.Output(t, name, compressed)
		if verify {
			command := exec.CommandContext(t.Context(), cli, "-d", "-c", "-j", "4")
			command.Stdin = bytes.NewReader(produced)

			actual, err := command.Output()
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(actual, data) {
				t.Fatal("official decoder mismatch")
			}
		}

		compressed = interop.Reference(t, name, func() []byte {
			command := exec.CommandContext(t.Context(), cli, "-c", "-b", "1", "-j", "4")
			command.Stdin = bytes.NewReader(data)

			encoded, err := command.Output()
			if err != nil {
				t.Fatal(err)
			}

			return encoded
		})

		actual := decodeBzip3(t, compressed, 4)
		if !bytes.Equal(actual, data) {
			t.Fatal("official encoder mismatch")
		}
	}
}
