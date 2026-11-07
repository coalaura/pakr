//go:build integration

package native

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
)

func TestBSCCLI(t *testing.T) {
	cli := os.Getenv("BSC_CLI")
	if cli == "" {
		cli = "bsc"
	}

	directory := t.TempDir()
	input := filepath.Join(directory, "input")
	packed := filepath.Join(directory, "packed.bsc")
	output := filepath.Join(directory, "output")

	sizes := []int{0, 1, 4096, 3<<20 + 37}

	for _, size := range sizes {
		data := testData(size)

		for level := 1; level <= 9; level++ {
			encoded := encodeBSC(t, data, level, 4, 317)
			name := strconv.Itoa(level) + "-" + strconv.Itoa(size)

			produced, verify := interop.Output(t, name, encoded)
			if !verify {
				continue
			}

			err := os.WriteFile(packed, produced, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			command := exec.CommandContext(t.Context(), cli, "d", packed, output)

			message, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("official decode level %d size %d: %v: %s", level, size, err, message)
			}

			actual, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("official decode mismatch: %v", err)
			}
		}
	}

	data := testData(3<<20 + 37)

	err := os.WriteFile(input, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	options := [][]string{
		{"-b1", "-e0", "-cf"},
		{"-b1", "-e1", "-cp", "-r"},
		{"-b1", "-e2", "-ca", "-r", "-s", "-H18", "-M32"},
		{"-b1", "-p"},
	}

	for sorter := 3; sorter <= 6; sorter++ {
		options = append(options, []string{"-b1", "-m" + strconv.Itoa(sorter), "-e2", "-cp", "-r"})
	}

	for index, flags := range options {
		encoded := interop.Reference(t, strconv.Itoa(index), func() []byte {
			arguments := append([]string{"e", input, packed}, flags...)
			command := exec.CommandContext(t.Context(), cli, arguments...)

			message, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("official encode %v: %v: %s", flags, err, message)
			}

			data, err := os.ReadFile(packed)
			if err != nil {
				t.Fatal(err)
			}

			return data
		})

		blocks := splitBSC(t, encoded)
		reordered := bytes.Clone(encoded[:8])

		for _, block := range slices.Backward(blocks) {
			reordered = append(reordered, block...)
		}

		if !bytes.Equal(data, decodeBSC(t, reordered, 4)) {
			t.Fatalf("official encode mismatch: %v", flags)
		}
	}
}
