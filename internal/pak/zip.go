package pak

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/coalaura/pakr/internal/native"
)

const zipBufferLimit = 8 << 20

type zipResult struct {
	header     *zip.FileHeader
	compressed []byte
	err        error
}

func packZip(target io.Writer, entries []entry, options Options, copying *copier) error {
	archive := zip.NewWriter(target)

	archive.RegisterCompressor(zip.Deflate, func(target io.Writer) (io.WriteCloser, error) {
		return native.NewWriter(target, native.Deflate, options.Level, 1)
	})

	// A bounded batch limits live libdeflate input/output buffers to roughly 256 MiB.
	workers := min(options.Threads, 16)
	results := make([]zipResult, workers)

	for offset := 0; offset < len(entries); offset += workers {
		batch := entries[offset:min(offset+workers, len(entries))]

		var pending sync.WaitGroup

		for index := range batch {
			pending.Go(func() {
				results[index] = prepareZip(&batch[index], options.Level, copying)
			})
		}

		pending.Wait()

		for index := range batch {
			result := &results[index]
			if result.err != nil {
				return result.err
			}

			err := copying.ctx.Err()
			if err != nil {
				return err
			}

			if result.compressed != nil {
				var writer io.Writer

				writer, err = archive.CreateRaw(result.header)
				if err == nil {
					_, err = writer.Write(result.compressed)
				}
			} else {
				var writer io.Writer

				writer, err = archive.CreateHeader(result.header)
				if err == nil && batch[index].header.Kind == native.Regular {
					err = copying.file(writer, &batch[index])
				}
			}

			result.compressed = nil

			if err != nil {
				return err
			}

			copying.progress.File()
		}
	}

	return archive.Close()
}

func prepareZip(item *entry, level int, copying *copier) zipResult {
	// zip.Writer retains header pointers until Close; batch slots may be reused before then.
	header := &zip.FileHeader{Name: item.header.Name, Method: zip.Deflate, Modified: item.header.Modified}
	header.SetMode(os.FileMode(item.header.Mode))

	if item.header.Kind == native.Directory {
		header.Name += "/"
		header.SetMode(os.ModeDir | os.FileMode(item.header.Mode))

		return zipResult{header: header}
	}

	if item.header.Size > zipBufferLimit {
		return zipResult{header: header}
	}

	err := copying.ctx.Err()
	if err != nil {
		return zipResult{err: err}
	}

	var data []byte

	if item.header.Kind == native.Symlink {
		header.SetMode(os.ModeSymlink | os.FileMode(item.header.Mode))

		data = []byte(item.header.Link)
	} else {
		file, err := os.Open(item.path)
		if err != nil {
			return zipResult{err: err}
		}

		// One spare byte detects growth without ReadAll's repeated buffer expansion.
		data = make([]byte, int(item.header.Size)+1)

		count, readErr := io.ReadFull(file, data)

		data = data[:count]

		err = readErr
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			err = nil
		}

		err = closeWithError(file, err)
		if err != nil {
			return zipResult{err: err}
		}

		if int64(len(data)) != item.header.Size {
			return zipResult{err: fmt.Errorf("input changed while packing: %s", item.path)}
		}
	}

	compressed, err := native.CompressDeflate(data, level)
	if err != nil {
		return zipResult{err: err}
	}

	header.UncompressedSize64 = uint64(len(data))
	header.CompressedSize64 = uint64(len(compressed))
	header.CRC32 = crc32.ChecksumIEEE(data)

	copying.progress.Add(len(data))

	return zipResult{header: header, compressed: compressed}
}

func unpackZip(source io.ReaderAt, size int64, extract *extractor) error {
	archive, err := zip.NewReader(source, size)
	if err != nil {
		return err
	}

	archive.RegisterDecompressor(zip.Deflate, func(source io.Reader) io.ReadCloser {
		reader, err := native.NewReader(source, native.Deflate, 1)
		if err != nil {
			return &errorReader{err: err}
		}

		return reader
	})

	var (
		compressed []byte
		data       []byte
	)

	for _, file := range archive.File {
		if file.Flags&1 != 0 {
			return fmt.Errorf("encrypted zip entries are not supported: %s", file.Name)
		}

		header := native.Header{Name: file.Name, Size: int64(file.UncompressedSize64), Mode: uint32(file.Mode().Perm()), Modified: file.Modified}

		if file.UncompressedSize64 > uint64(^uint64(0)>>1) {
			return fmt.Errorf("zip entry too large: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			header.Kind = native.Directory

			err = extract.entry(header, nil)
			if err != nil {
				return err
			}

			continue
		}

		var reader io.ReadCloser

		if file.Method == zip.Deflate && file.CompressedSize64 <= zipBufferLimit && file.UncompressedSize64 <= zipBufferLimit {
			var raw io.Reader

			raw, err = file.OpenRaw()
			if err != nil {
				return err
			}

			compressed = resize(compressed, int(file.CompressedSize64))
			data = resize(data, int(file.UncompressedSize64))

			_, err = io.ReadFull(raw, compressed)
			if err == nil {
				err = native.DecompressDeflate(compressed, data)
			}

			if err != nil {
				return fmt.Errorf("decode zip entry %q: %w", file.Name, err)
			}

			if crc32.ChecksumIEEE(data) != file.CRC32 {
				return zip.ErrChecksum
			}

			reader = io.NopCloser(bytes.NewReader(data))
		} else {
			reader, err = file.Open()
			if err != nil {
				return err
			}
		}

		if file.Mode()&os.ModeSymlink != 0 {
			var target strings.Builder

			_, err = io.Copy(&target, io.LimitReader(reader, 4097))
			if err == nil && target.Len() > 4096 {
				err = errors.New("symbolic link target exceeds 4096 bytes")
			}

			header.Kind = native.Symlink
			header.Link = target.String()
		}

		if err == nil {
			err = extract.entry(header, reader)
		}

		err = closeWithError(reader, err)
		if err != nil {
			return err
		}
	}

	return nil
}

func resize(buffer []byte, size int) []byte {
	if cap(buffer) < size {
		return make([]byte, size)
	}

	return buffer[:size]
}
