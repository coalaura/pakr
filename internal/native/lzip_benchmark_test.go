package native

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
)

func BenchmarkLzip(b *testing.B) {
	sizes := []int{4096, 8 << 20}
	levels := []int{1, 3, 9}
	corpora := []string{"text", "source", "mixed", "random"}

	for _, corpus := range corpora {
		for _, size := range sizes {
			data := benchmarkData(corpus, size)

			if corpus == "source" {
				source, err := os.ReadFile("codec.go")
				if err != nil {
					b.Fatal(err)
				}

				data = bytes.Repeat(source, size/len(source)+1)[:size]
			}

			for _, level := range levels {
				compressed := encodeLzip(b, data, level)
				name := fmt.Sprintf("%s/size%d/level%d", corpus, size, level)

				b.Run(name+"/encode", func(b *testing.B) {
					b.SetBytes(int64(size))
					b.ReportAllocs()

					for b.Loop() {
						writer, err := NewLzipWriter(b.Context(), io.Discard, level, 1<<30, int64(size))
						if err != nil {
							b.Fatal(err)
						}

						_, err = writer.Write(data)

						closeErr := writer.Close()

						if err != nil || closeErr != nil {
							b.Fatalf("encode: %v, close: %v", err, closeErr)
						}
					}

					b.ReportMetric(float64(len(compressed))/float64(size), "output/input")
				})

				b.Run(name+"/decode", func(b *testing.B) {
					buffer := make([]byte, BufferSize)

					b.SetBytes(int64(size))
					b.ReportAllocs()

					for b.Loop() {
						reader, err := NewLzipReader(b.Context(), bytes.NewReader(compressed), 1<<30)
						if err != nil {
							b.Fatal(err)
						}

						_, err = io.CopyBuffer(lzipTestWriter{io.Discard}, reader, buffer)

						reader.Close()

						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
