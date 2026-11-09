//go:build integration

package native

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coalaura/pakr/internal/interop"
)

type xzInteropCase struct {
	name    string
	data    []byte
	level   int
	workers int
	extreme bool
	chunk   int
}

func TestXZCLI(t *testing.T) {
	cli := os.Getenv("XZ_CLI")
	if cli == "" {
		cli = "xz"
	}

	mixed := append(xzRandom(256<<10), testData(256<<10)...)

	cases := []xzInteropCase{
		{name: "empty", level: 3, workers: 1, chunk: 1},
		{name: "tiny", data: []byte("x"), level: 3, workers: 1, chunk: 1},
		{name: "text", data: testData(4097), level: 3, workers: 1, chunk: 7},
		{name: "mixed", data: mixed, level: 6, workers: 1, chunk: 317},
		{name: "random", data: xzRandom(256 << 10), level: 9, workers: 1, chunk: 65537},
		{name: "streamed", data: bytes.Repeat(mixed, 12), level: 2, workers: 2, chunk: 65537},
		{name: "incompressible-blocks", data: xzRandom(3<<20 + 1), level: 2, workers: 1, chunk: 65537},
		{name: "level1", data: testData(4097), level: 1, workers: 1, chunk: 317},
		{name: "extreme", data: testData(4097), level: 3, workers: 2, extreme: true, chunk: 317},
		{name: "max", data: testData(4097), level: 9, workers: 1, extreme: true, chunk: 317},
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			encoded, settings := encodeXZ(t, scenario.data, scenario.level, scenario.workers, scenario.extreme, scenario.chunk)
			if settings.Backend == "Fast LZMA2" {
				checkXZFraming(t, encoded, scenario.data, settings.Dictionary)
			}

			produced, verify := interop.Output(t, "stream.xz", encoded)
			if !verify {
				return
			}

			command := exec.CommandContext(t.Context(), cli, "--decompress", "--stdout", "--threads=1", "--memlimit-decompress=128MiB")
			command.Stdin = bytes.NewReader(produced)

			var stderr bytes.Buffer

			command.Stderr = &stderr

			actual, err := command.Output()
			if err != nil || !bytes.Equal(actual, scenario.data) {
				t.Fatalf("official XZ decode: %v; decoded %d bytes, want %d; stderr: %s", err, len(actual), len(scenario.data), stderr.String())
			}

			if scenario.name == "streamed" || scenario.name == "incompressible-blocks" {
				checkXZCLIBlocks(t, cli, produced, scenario.data, settings.Dictionary)
			}

			if scenario.name == "text" {
				corrupt := bytes.Clone(produced)
				corrupt[len(corrupt)-25] ^= 1

				invalid := [][]byte{produced[:len(produced)-1], corrupt}

				for _, stream := range invalid {
					command = exec.CommandContext(t.Context(), cli, "--test", "--threads=1", "--memlimit-decompress=128MiB")
					command.Stdin = bytes.NewReader(stream)

					err = command.Run()
					if err == nil {
						t.Fatal("official XZ accepted damaged stream")
					}
				}
			}
		})
	}

	reference := interop.Reference(t, "liblzma.xz", func() []byte {
		command := exec.CommandContext(t.Context(), cli, "--compress", "--stdout", "--threads=1", "--check=crc64", "-1")
		command.Stdin = bytes.NewReader(mixed)

		var stderr bytes.Buffer

		command.Stderr = &stderr

		encoded, err := command.Output()
		if err != nil {
			t.Fatalf("official XZ encode: %v; stderr: %s", err, stderr.String())
		}

		return encoded
	})

	joined := append(bytes.Clone(reference), reference...)

	actual, err := decodeXZ(joined)
	if err != nil || !bytes.Equal(actual, bytes.Repeat(mixed, 2)) {
		t.Fatalf("existing liblzma concatenated stream decode: %v", err)
	}
}

func checkXZCLIBlocks(t *testing.T, cli string, encoded, data []byte, dictionary int) {
	t.Helper()

	blocks := checkXZFraming(t, encoded, data, dictionary)
	expected := (len(data) + 3*dictionary - 1) / (3 * dictionary)

	if len(blocks) != expected || len(blocks) < 2 {
		t.Fatalf("got %d blocks, want %d independent blocks", len(blocks), expected)
	}

	checkXZCLIListing(t, cli, encoded, 1, len(blocks))
	checkXZCLIContents(t, cli, encoded, data)

	consumed := 0
	offsets := make([]int, 0, len(blocks)*3+2)

	for _, block := range blocks {
		standalone := xzSingleBlock(encoded, block)
		checkXZCLIContents(t, cli, standalone, data[consumed:consumed+block.uncompressed])

		consumed += block.uncompressed
		check := block.offset + (block.unpadded+3)&^3 - 8
		offsets = append(offsets, block.offset+block.headerSize-1, block.offset+block.headerSize, check)
	}

	offsets = append(offsets, len(encoded)-13, len(encoded)-1)

	for _, offset := range offsets {
		corrupt := bytes.Clone(encoded)
		corrupt[offset] ^= 1

		command := exec.CommandContext(t.Context(), cli, "--test", "--threads=2", "--memlimit-decompress=128MiB")
		command.Stdin = bytes.NewReader(corrupt)

		err := command.Run()
		if err == nil {
			t.Fatalf("official XZ accepted corruption at %d", offset)
		}
	}

	for _, block := range blocks {
		command := exec.CommandContext(t.Context(), cli, "--test", "--threads=2", "--memlimit-decompress=128MiB")
		command.Stdin = bytes.NewReader(encoded[:block.offset+(block.unpadded+3)&^3])

		err := command.Run()
		if err == nil {
			t.Fatal("official XZ accepted missing block/Index/footer")
		}
	}

	joined := make([]byte, 0, 2*len(encoded)+4)
	joined = append(joined, encoded...)
	joined = append(joined, 0, 0, 0, 0)
	joined = append(joined, encoded...)

	checkXZCLIListing(t, cli, joined, 2, 2*len(blocks))
	checkXZCLIContents(t, cli, joined, bytes.Repeat(data, 2))
}

func checkXZCLIContents(t *testing.T, cli string, encoded, data []byte) {
	t.Helper()

	command := exec.CommandContext(t.Context(), cli, "--decompress", "--stdout", "--threads=2", "--memlimit-decompress=128MiB", "--memlimit-mt-decompress=128MiB")
	command.Stdin = bytes.NewReader(encoded)

	var stderr bytes.Buffer

	command.Stderr = &stderr

	actual, err := command.Output()
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("official threaded XZ decode: %v; decoded %d bytes, want %d; stderr: %s", err, len(actual), len(data), stderr.String())
	}
}

func checkXZCLIListing(t *testing.T, cli string, encoded []byte, streams, blocks int) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "blocks.xz")

	err := os.WriteFile(path, encoded, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	command := exec.CommandContext(t.Context(), cli, "--robot", "--list", "-vv", path)

	var stderr bytes.Buffer

	command.Stderr = &stderr

	output, err := command.Output()
	if err != nil {
		t.Fatalf("official XZ block listing: %v; stderr: %s", err, stderr.String())
	}

	var (
		listed int
		sized  bool
		file   bool
	)

	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "file":
			file = len(fields) >= 7 && fields[1] == strconv.Itoa(streams) && fields[2] == strconv.Itoa(blocks) && fields[6] == "CRC64"
		case "block":
			listed++
		case "summary":
			sized = len(fields) >= 3 && fields[2] == "yes"
		}
	}

	if !file || !sized || listed != blocks {
		t.Fatalf("official XZ did not confirm CRC64, block counts and both header sizes:\n%s", output)
	}
}
