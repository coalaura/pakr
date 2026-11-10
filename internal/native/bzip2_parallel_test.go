package native

import (
	"bytes"
	"compress/bzip2"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParallelBzip2Streams(t *testing.T) {
	data := testData(4 << 20)
	threadCounts := []int{1, 3}

	for _, threads := range threadCounts {
		t.Run(strconv.Itoa(threads), func(t *testing.T) {
			var encoded bytes.Buffer

			writer, err := NewWriter(&encoded, Bzip2, 3, threads)
			if err != nil {
				t.Fatal(err)
			}

			_, err = writer.Write(data)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			independent, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(encoded.Bytes())))
			if err != nil || !bytes.Equal(independent, data) {
				t.Fatalf("independent decoding: %v", err)
			}

			filename := filepath.Join(t.TempDir(), "input.bz2")
			layouts := []string{"valid", "block-crc", "stream-crc", "truncated"}

			for _, layout := range layouts {
				t.Run(layout, func(t *testing.T) {
					input := bytes.Clone(encoded.Bytes())

					switch layout {
					case "block-crc":
						input[10] ^= 1
					case "stream-crc":
						bit := bzip2FooterBit(t, input) + 48
						input[bit/8] ^= 1 << (7 - bit%8)
					case "truncated":
						input = input[:len(input)-6]
					}

					writeErr := os.WriteFile(filename, input, 0o600)
					if writeErr != nil {
						t.Fatal(writeErr)
					}

					for range 2 {
						reader, readErr := OpenParallel(filename, Bzip2, 3)

						var actual []byte

						if readErr == nil {
							actual, readErr = io.ReadAll(reader)
							reader.Close()
						}

						if layout == "valid" {
							if readErr != nil || !bytes.Equal(actual, data) {
								t.Fatalf("parallel decoding: %v", readErr)
							}
						} else if readErr == nil {
							t.Fatal("corrupt stream accepted")
						}
					}
				})
			}
		})
	}
}

func bzip2FooterBit(t *testing.T, data []byte) int {
	t.Helper()

	var window uint64

	for bit := range len(data) * 8 {
		window = (window<<1 | uint64(data[bit/8]>>(7-bit%8)&1)) & 0xffffffffffff
		if bit >= 47 && window == 0x177245385090 {
			return bit - 47
		}
	}

	t.Fatal("missing bzip2 footer")

	return 0
}
