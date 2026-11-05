package native

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkCodecs(b *testing.B) {
	formats := []int{Gzip, Zstd, Bzip2, XZ, LZ4, Brotli}
	levels := []int{1, 3, 9}
	workers := []int{1, 4, 16}
	corpora := []string{"text", "mixed", "random", "compressed"}

	for _, corpus := range corpora {
		data := benchmarkData(corpus, 8<<20)

		for _, format := range formats {
			for _, level := range levels {
				for _, threads := range workers {
					name := fmt.Sprintf("%s/codec%d/level%d/threads%d", corpus, format, level, threads)

					b.Run(name, func(b *testing.B) {
						benchmarkCodec(b, data, format, level, threads)
					})
				}
			}
		}
	}
}

func BenchmarkGzipChunks(b *testing.B) {
	data := benchmarkData("mixed", 32<<20)
	chunks := []int{256 << 10, 1 << 20, 4 << 20}
	workers := []int{1, 4, 16}

	for _, chunk := range chunks {
		for _, threads := range workers {
			b.Run(fmt.Sprintf("chunk%d/threads%d", chunk, threads), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()

				for b.Loop() {
					writer, err := NewGzipWriter(io.Discard, 3, threads)
					if err != nil {
						b.Fatal(err)
					}

					writer.chunk = chunk

					_, err = writer.Write(data)
					if err != nil {
						b.Fatal(err)
					}

					err = writer.Close()
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkDeflateBackends(b *testing.B) {
	sizes := []int{1 << 20, 8 << 20, 32 << 20}

	for _, size := range sizes {
		data := benchmarkData("mixed", size)

		b.Run(fmt.Sprintf("libdeflate/%d", size), func(b *testing.B) {
			codec, err := NewDeflater(3)
			if err != nil {
				b.Fatal(err)
			}

			defer codec.Close()

			var output []byte

			b.SetBytes(int64(size))
			b.ReportAllocs()

			for b.Loop() {
				output, err = codec.Compress(output, data)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(len(output))/float64(size), "output/input")
		})

		b.Run(fmt.Sprintf("streaming/%d", size), func(b *testing.B) {
			benchmarkCodec(b, data, Deflate, 3, 1)
		})
	}
}

func BenchmarkParallelReaders(b *testing.B) {
	sizes := []int{64 << 10, 1 << 20, 32 << 20}
	formats := []int{Gzip, Bzip2}

	for _, size := range sizes {
		data := benchmarkData("mixed", size)

		for _, format := range formats {
			var compressed bytes.Buffer

			writer, err := benchmarkWriter(&compressed, format, 3, 4)
			if err != nil {
				b.Fatal(err)
			}

			_, err = writer.Write(data)
			if err != nil {
				b.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				b.Fatal(err)
			}

			filename := filepath.Join(b.TempDir(), "stream")

			err = os.WriteFile(filename, compressed.Bytes(), 0o600)
			if err != nil {
				b.Fatal(err)
			}

			modes := []bool{false, true}

			for _, parallel := range modes {
				b.Run(fmt.Sprintf("codec%d/size%d/parallel%t", format, size, parallel), func(b *testing.B) {
					b.SetBytes(int64(size))
					b.ReportAllocs()

					for b.Loop() {
						var (
							reader io.ReadCloser
							file   *os.File
						)

						if parallel {
							reader, err = OpenParallel(filename, format, 4)
						} else {
							file, err = os.Open(filename)
							if err == nil {
								reader, err = NewReader(file, format, 1)
							}
						}

						if err != nil {
							b.Fatal(err)
						}

						_, err = io.Copy(io.Discard, reader)
						reader.Close()

						if file != nil {
							file.Close()
						}

						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func BenchmarkLargeCodecs(b *testing.B) {
	data := benchmarkData("mixed", 128<<20)
	formats := []int{Gzip, Zstd, Bzip2, XZ, LZ4, Brotli}
	workers := []int{1, 4, 16}

	for _, format := range formats {
		for _, threads := range workers {
			b.Run(fmt.Sprintf("codec%d/threads%d", format, threads), func(b *testing.B) {
				benchmarkCodec(b, data, format, 3, threads)
			})
		}
	}
}

func BenchmarkSeekable(b *testing.B) {
	data := benchmarkData("mixed", 128<<20)
	workers := []int{1, 4, 8}
	modes := []bool{false, true}

	for _, seekable := range modes {
		var (
			compressed bytes.Buffer
			writer     io.WriteCloser
			err        error
		)

		if seekable {
			writer, err = NewSeekableWriter(&compressed, 3, 4)
		} else {
			writer, err = NewWriter(&compressed, Zstd, 3, 4)
		}

		if err != nil {
			b.Fatal(err)
		}

		_, err = writer.Write(data)
		if err != nil {
			b.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("compress/seekable%t", seekable), func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()

			for b.Loop() {
				compressed.Reset()

				if seekable {
					writer, err = NewSeekableWriter(&compressed, 3, 4)
				} else {
					writer, err = NewWriter(&compressed, Zstd, 3, 4)
				}

				if err != nil {
					b.Fatal(err)
				}

				_, err = writer.Write(data)
				if err != nil {
					b.Fatal(err)
				}

				err = writer.Close()
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(compressed.Len())/float64(len(data)), "output/input")
		})

		for _, threads := range workers {
			b.Run(fmt.Sprintf("decompress/seekable%t/threads%d", seekable, threads), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()

				for b.Loop() {
					var reader io.ReadCloser

					if seekable {
						reader, err = OpenSeekable(bytes.NewReader(compressed.Bytes()), int64(compressed.Len()), threads)
					} else {
						reader, err = NewReader(bytes.NewReader(compressed.Bytes()), Zstd, threads)
					}

					if err != nil {
						b.Fatal(err)
					}

					_, err = io.Copy(io.Discard, reader)
					reader.Close()

					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func benchmarkCodec(b *testing.B, data []byte, format, level, threads int) {
	var compressed bytes.Buffer

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()

	for b.Loop() {
		compressed.Reset()

		writer, err := benchmarkWriter(&compressed, format, level, threads)
		if err != nil {
			b.Fatal(err)
		}

		_, err = writer.Write(data)
		if err != nil {
			writer.Close()
			b.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportMetric(float64(compressed.Len())/float64(len(data)), "output/input")
}

func benchmarkWriter(target io.Writer, format, level, threads int) (io.WriteCloser, error) {
	if format == Gzip {
		return NewGzipWriter(target, level, threads)
	}

	if format == Bzip2 && threads > 1 {
		return NewBzip2Writer(target, level, threads)
	}

	return NewWriter(target, format, level, threads)
}

func benchmarkData(corpus string, size int) []byte {
	data := testData(size)

	switch corpus {
	case "compressed":
		var compressed bytes.Buffer

		writer := gzip.NewWriter(&compressed)
		writer.Write(data)
		writer.Close()
		data = compressed.Bytes()
	case "text":
		line := []byte("package sample\nfunc value() string { return `some text and JSON: {\"id\":1234,\"active\":true}` }\n")

		for offset := 0; offset < size; offset += len(line) {
			copy(data[offset:], line)
		}
	case "random":
		random := rand.NewChaCha8([32]byte{19})

		for index := range data {
			data[index] = byte(random.Uint64())
		}
	}

	return data
}
