package pak

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"

	"github.com/coalaura/pakr/internal/native"
)

const zipBufferLimit = 8 << 20

type zipResult struct {
	header     *zip.FileHeader
	compressed []byte
	stream     *zipStream
	err        error
}

type zipWorker struct {
	codec  *native.Deflater
	input  []byte
	output []byte
	jobs   chan *entry
	done   chan zipResult
	chunks chan zipChunk
}

type zipDecoder struct {
	codec      *native.Inflater
	compressed []byte
	data       []byte
}

func (worker *zipWorker) run(copying *copier, level int) {
	defer worker.codec.Close()

	for item := range worker.jobs {
		result := prepareZip(item, worker, copying)
		if result.err != nil || item.header.Size <= zipBufferLimit {
			select {
			case worker.done <- result:
			case <-copying.ctx.Done():
				return
			}

			continue
		}

		result.stream = newZipStream(copying.ctx, worker.chunks)
		worker.chunks = result.stream.free

		select {
		case worker.done <- result:
		case <-copying.ctx.Done():
			return
		}

		worker.stream(result.stream, item, copying, level)
	}
}

func (worker *zipWorker) stream(stream *zipStream, item *entry, copying *copier, level int) {
	defer close(stream.ready)

	encoder := zipStreamEncoder{stream: stream}
	codec, err := native.NewWriter(&encoder.output, native.Deflate, level, 1)
	encoder.codec = codec

	worker.input = resize(worker.input, native.BufferSize)
	streaming := *copying
	streaming.buffer = worker.input

	if err == nil {
		err = streaming.file(&encoder, item)
		err = encoder.finish(err)
	}

	if err != nil {
		stream.send(zipChunk{err: err})
	}
}

func (decoder *zipDecoder) extract(file *zip.File, extract *extractor) error {
	header := zipHeader(file)

	var (
		reader io.ReadCloser
		err    error
	)

	if file.Mode()&os.ModeSymlink != 0 && header.Size > 4096 {
		return errors.New("symbolic link target exceeds 4096 bytes")
	}

	if file.Method == zip.Deflate && file.CompressedSize64 <= zipBufferLimit && file.UncompressedSize64 <= zipBufferLimit {
		var raw io.Reader

		raw, err = file.OpenRaw()
		if err != nil {
			return err
		}

		decoder.compressed = resize(decoder.compressed, int(file.CompressedSize64))
		decoder.data = resize(decoder.data, int(file.UncompressedSize64))

		_, err = io.ReadFull(raw, decoder.compressed)
		if err == nil {
			err = decoder.codec.Decompress(decoder.compressed, decoder.data)
		}

		if err != nil {
			return fmt.Errorf("decode zip entry %q: %w", file.Name, err)
		}

		if crc32.ChecksumIEEE(decoder.data) != file.CRC32 {
			return zip.ErrChecksum
		}

		reader = io.NopCloser(bytes.NewReader(decoder.data))
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

	return closeWithError(reader, err)
}

func packZip(target io.Writer, entries []entry, options Options, copying *copier) error {
	ctx, cancel := context.WithCancel(copying.ctx)

	working := *copying
	working.ctx = ctx
	copying = &working

	archive := zip.NewWriter(target)

	var compressed zipStreamWriter

	archive.RegisterCompressor(zip.Deflate, func(target io.Writer) (io.WriteCloser, error) {
		compressed.target = target

		return &compressed, nil
	})

	// Slots bound the reorder window; streaming entries also have bounded chunk queues.
	workers := make([]zipWorker, min(options.Threads, 16, len(entries)))

	var pending sync.WaitGroup

	defer func() {
		cancel()

		for index := range workers {
			if workers[index].jobs != nil {
				close(workers[index].jobs)
			}
		}

		pending.Wait()
	}()

	for index := range workers {
		worker := &workers[index]

		codec, err := native.NewDeflater(options.Level)
		if err != nil {
			return err
		}

		worker.codec = codec
		worker.jobs = make(chan *entry, 1)
		worker.done = make(chan zipResult, 1)
		worker.jobs <- &entries[index]

		pending.Go(func() {
			worker.run(copying, options.Level)
		})
	}

	for index := range entries {
		worker := &workers[index%len(workers)]

		var result zipResult

		select {
		case result = <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}

		if result.err != nil {
			return result.err
		}

		err := copying.ctx.Err()
		if err != nil {
			return err
		}

		if result.stream != nil {
			var writer io.Writer

			writer, err = archive.CreateHeader(result.header)
			if err == nil {
				err = result.stream.write(writer, &compressed)
			}
		} else if result.compressed != nil {
			var writer io.Writer

			writer, err = archive.CreateRaw(result.header)
			if err == nil {
				var count int

				count, err = writer.Write(result.compressed)
				if err == nil && count != len(result.compressed) {
					err = io.ErrShortWrite
				}
			}
		} else {
			var writer io.Writer

			writer, err = archive.CreateHeader(result.header)
			if err == nil && entries[index].header.Kind == native.Regular {
				err = copying.file(writer, &entries[index])
			}
		}

		if err != nil {
			return err
		}

		next := index + len(workers)
		if next < len(entries) {
			worker.jobs <- &entries[next]
		}

		copying.progress.File()
	}

	copying.progress.Phase("finish", -1)

	return archive.Close()
}

func prepareZip(item *entry, worker *zipWorker, copying *copier) zipResult {
	// zip.Writer retains header pointers until Close; worker slots may be reused before then.
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
		worker.input = resize(worker.input, int(item.header.Size)+1)
		data = worker.input

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

	compressed, err := worker.codec.Compress(worker.output, data)
	if err != nil {
		return zipResult{err: err}
	}

	worker.output = compressed

	header.UncompressedSize64 = uint64(len(data))
	header.CompressedSize64 = uint64(len(compressed))
	header.CRC32 = crc32.ChecksumIEEE(data)

	if item.header.Kind == native.Regular {
		copying.progress.Add(len(data))
	}

	return zipResult{header: header, compressed: compressed}
}

func unpackZip(source io.ReaderAt, size int64, threads int, extract *extractor) error {
	err := checkZipEntries(source, size, extract.budget)
	if err != nil {
		return err
	}

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

	err = validateZipPaths(archive.File)
	if err != nil {
		return err
	}

	advanced := false

	for _, file := range archive.File {
		switch file.Method {
		case zip.Store, zip.Deflate:
		case 12, 14, 93, 95, 98:
			advanced = true
		default:
			return fmt.Errorf("unsupported ZIP compression method %d for %q", file.Method, file.Name)
		}

		err = extract.budget.entry(int64(file.UncompressedSize64))
		if err != nil {
			return err
		}
	}

	extract.prechecked = true

	if advanced {
		input, ok := source.(*os.File)
		if !ok {
			return errors.New("advanced ZIP methods require a seekable archive file")
		}

		err = unpackZipAdvanced(input.Name(), archive.File, extract)
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(extract.copying.ctx)
	defer cancel()

	jobs := make(chan *zip.File)
	directories := extract.directoryState()

	var (
		pending sync.WaitGroup
		first   sync.Once
		failure error
	)

	for range min(threads, 16, max(1, len(archive.File))) {
		pending.Go(func() {
			codec, workerErr := native.NewInflater()
			if workerErr != nil {
				first.Do(func() {
					failure = workerErr
					cancel()
				})

				return
			}

			defer codec.Close()

			decoder := zipDecoder{codec: codec}
			copying := copier{ctx: ctx, progress: extract.copying.progress, buffer: make([]byte, native.BufferSize)}

			worker := extractor{root: extract.root, copying: &copying, budget: extract.budget, directories: directories, prechecked: true}
			defer worker.close()

			for file := range jobs {
				workerErr = decoder.extract(file, &worker)
				if workerErr != nil {
					first.Do(func() {
						failure = workerErr
						cancel()
					})

					return
				}
			}
		})
	}

	// Directory metadata is collected by the caller; links are deferred until workers exit.
	for _, file := range archive.File {
		if file.Method != zip.Store && file.Method != zip.Deflate {
			continue
		}

		if file.FileInfo().IsDir() {
			header := zipHeader(file)
			header.Kind = native.Directory

			err = extract.entry(header, nil)
			if err != nil {
				cancel()

				break
			}
		} else if file.Mode()&os.ModeSymlink == 0 {
			select {
			case jobs <- file:
			case <-ctx.Done():
			}
		}

		if ctx.Err() != nil {
			break
		}
	}

	close(jobs)
	pending.Wait()

	if err != nil || failure != nil {
		return errors.Join(err, failure)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	codec, err := native.NewInflater()
	if err != nil {
		return err
	}

	defer codec.Close()

	decoder := zipDecoder{codec: codec}

	for _, file := range archive.File {
		if file.Mode()&os.ModeSymlink != 0 && (file.Method == zip.Store || file.Method == zip.Deflate) {
			err = decoder.extract(file, extract)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func zipHeader(file *zip.File) native.Header {
	return native.Header{Name: file.Name, Size: int64(file.UncompressedSize64), Mode: uint32(file.Mode().Perm()), Modified: file.Modified}
}

func validateZipPaths(files []*zip.File) error {
	paths := make(map[string]bool, len(files))

	for _, file := range files {
		if file.Flags&0x41 != 0 || file.UncompressedSize64 > uint64(^uint64(0)>>1) {
			return fmt.Errorf("encrypted or oversized zip entry: %s", file.Name)
		}

		if file.FileInfo().IsDir() && (file.UncompressedSize64 != 0 || file.CRC32 != 0) {
			return fmt.Errorf("ZIP directory has payload or invalid checksum: %q", file.Name)
		}

		if file.Mode()&os.ModeSymlink != 0 && file.UncompressedSize64 > 4096 {
			return fmt.Errorf("symbolic link target exceeds 4096 bytes: %q", file.Name)
		}

		name, err := cleanName(file.Name)
		if err != nil {
			return err
		}

		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			name = strings.ToLower(name)
		}

		if _, exists := paths[name]; exists {
			return fmt.Errorf("duplicate zip path: %q", file.Name)
		}

		paths[name] = file.FileInfo().IsDir()
	}

	for name := range paths {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if directory, exists := paths[parent]; exists && !directory {
				return fmt.Errorf("conflicting zip path: %q", name)
			}
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
