package native

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type parallelIntegrityCase struct {
	name  string
	input []byte
	want  []byte
	valid bool
}

func TestParallelIntegrity(t *testing.T) {
	formats := []int{Gzip, Bzip2}
	threadCounts := []int{1, 3}
	data := testData(4 << 20)

	for _, format := range formats {
		t.Run(strconv.Itoa(format), func(t *testing.T) {
			encoded := encodeParallelIntegrity(t, format, data)
			empty := encodeParallelIntegrity(t, format, nil)
			
			corrupt := bytes.Clone(encoded)
			headerSize := 10

			if format == Gzip {
				corrupt[len(corrupt)-8] ^= 1
			} else {
				headerSize = 4
				bit := bzip2FooterBit(t, corrupt) + 48
				corrupt[bit/8] ^= 1 << (7 - bit%8)
			}

			malformed := bytes.Clone(encoded)
			malformed[0] ^= 0xff

			doubled := bytes.Repeat(data, 2)
			cases := []parallelIntegrityCase{
				{name: "valid", input: encoded, want: data, valid: true},
				{name: "empty", input: empty, valid: true},
				{name: "concatenated", input: bytes.Repeat(encoded, 2), want: doubled, valid: true},
				{name: "empty-members", input: bytes.Join([][]byte{empty, encoded, empty, encoded, empty}, nil), want: doubled, valid: true},
				{name: "stream-crc", input: corrupt},
				{name: "first-stream-crc", input: bytes.Join([][]byte{corrupt, encoded}, nil)},
				{name: "last-stream-crc", input: bytes.Join([][]byte{encoded, corrupt}, nil)},
				{name: "header-only", input: encoded[:headerSize]},
				{name: "short-header", input: encoded[:headerSize-1]},
				{name: "short-body", input: encoded[:len(encoded)/2]},
				{name: "short-footer", input: encoded[:len(encoded)-1]},
				{name: "short-last-member", input: bytes.Join([][]byte{encoded, encoded[:len(encoded)-1]}, nil)},
				{name: "malformed-header", input: malformed},
			}

			for _, threads := range threadCounts {
				for _, test := range cases {
					t.Run(strconv.Itoa(threads)+"/"+test.name, func(t *testing.T) {
						filename := filepath.Join(t.TempDir(), "input")

						err := os.WriteFile(filename, test.input, 0o600)
						if err != nil {
							t.Fatal(err)
						}

						reader, err := OpenParallel(filename, format, threads)

						var actual []byte

						if err == nil {
							actual, err = io.ReadAll(reader)
							reader.Close()
						}

						if test.valid {
							if err != nil || !bytes.Equal(actual, test.want) {
								t.Fatalf("decode: %v, bytes=%d, want=%d", err, len(actual), len(test.want))
							}
						} else if err == nil {
							t.Fatal("corrupt stream accepted")
						}
					})
				}
			}
		})
	}
}

func encodeParallelIntegrity(t *testing.T, format int, data []byte) []byte {
	t.Helper()

	var encoded bytes.Buffer

	writer, err := NewWriter(&encoded, format, 3, 1)
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

	return encoded.Bytes()
}
