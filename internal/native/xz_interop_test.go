//go:build integration

package native

import (
	"bytes"
	"os"
	"os/exec"
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

			actual, err := command.Output()
			if err != nil || !bytes.Equal(actual, scenario.data) {
				t.Fatalf("official XZ decode: %v", err)
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

		encoded, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}

		return encoded
	})

	joined := append(bytes.Clone(reference), reference...)

	actual, err := decodeXZ(joined)
	if err != nil || !bytes.Equal(actual, bytes.Repeat(mixed, 2)) {
		t.Fatalf("existing liblzma concatenated stream decode: %v", err)
	}
}
