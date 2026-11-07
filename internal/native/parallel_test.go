package native

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParallelGzipRepeated(t *testing.T) {
	for worker := range 3 {
		t.Run(strconv.Itoa(worker), func(t *testing.T) {
			t.Parallel()

			// Span several rapidgzip chunks and give concurrent readers distinct windows.
			data := testData(16 << 20)

			for index := 0; index < len(data)/2; index++ {
				data[index] = byte(worker + index%251)
			}

			var encoded bytes.Buffer

			writer := gzip.NewWriter(&encoded)

			_, err := writer.Write(data)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			expected := sha256.Sum256(data)
			filename := filepath.Join(t.TempDir(), "single.gz")

			err = os.WriteFile(filename, encoded.Bytes(), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			buffer := make([]byte, BufferSize)

			for range 3 {
				reader, openErr := OpenParallel(filename, Gzip, 3)
				if openErr != nil {
					t.Fatal(openErr)
				}

				digest := sha256.New()
				count, readErr := io.CopyBuffer(digest, reader, buffer)

				reader.Close()

				if readErr != nil || count != int64(len(data)) || !bytes.Equal(digest.Sum(nil), expected[:]) {
					t.Fatalf("repeated parallel decode: bytes=%d, error=%v", count, readErr)
				}
			}
		})
	}
}
