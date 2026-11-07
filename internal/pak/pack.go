package pak

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/coalaura/pakr/internal/native"
	"github.com/coalaura/plain/minimal"
)

type Options struct {
	Format   Format
	Output   string
	Threads  int
	Memory   uint64
	Level    int
	Image    int
	Extreme  bool
	Seekable bool
	Limits   *ExtractionLimits
	Log      *minimal.Minimal
	Progress io.Writer
	Stdin    io.Reader
	Stdout   io.Writer
}

func Pack(ctx context.Context, inputs []string, options Options, stdout io.Writer) error {
	if options.Format.extractionOnly() {
		return fmt.Errorf("%s is extraction-only; archive creation is not supported", options.Format.Name)
	}

	outputFormat, _ := InferFormat(options.Output)
	if outputFormat.extractionOnly() {
		return fmt.Errorf("%s is extraction-only; archive creation is not supported", outputFormat.Name)
	}

	if options.Seekable && options.Format.Codec != native.Zstd {
		return errors.New("--seekable requires zst or tar.zst")
	}

	if len(inputs) == 0 {
		return errors.New("provide at least one file or directory to pack")
	}

	output := options.Output
	if output == "" {
		output = "archive." + options.Format.Name
	}

	packPreview(options.Log, inputs, output)

	progress := NewProgress(options.Log, options.Progress, "scan")
	defer progress.Close()

	var (
		absolute string
		err      error
	)

	if output != "-" {
		absolute, err = filepath.Abs(output)
		if err != nil {
			return err
		}

		_, err = os.Lstat(absolute)
		if err == nil {
			return fmt.Errorf("output already exists: %s", output)
		}

		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	var entries []entry

	total := int64(-1)

	stdin := options.Format.Name == "knz" && len(inputs) == 1 && inputs[0] == "-"
	if stdin {
		if options.Stdin == nil {
			return errors.New("kanzi stdin input is unavailable")
		}
	} else {
		options.Stdin = nil

		entries, total, err = collect(ctx, inputs, absolute)
		if err != nil {
			return err
		}
	}

	if !stdin && (options.Format.Container == "" || options.Format.Container == "raw") && (len(entries) != 1 || entries[0].header.Kind != native.Regular) {
		return fmt.Errorf("%s compresses one regular file; use an archive format for multiple files or directories", options.Format.Name)
	}

	progress.Phase("pack", total)

	copying := &copier{ctx: ctx, progress: progress, buffer: make([]byte, native.BufferSize)}

	var (
		temporary *os.File
		target    = stdout
	)

	if output != "-" || options.Format.Container == "7z" || options.Format.Container == "wim" || options.Format.Codec == native.BSC {
		directory := ""

		if output != "-" {
			directory = filepath.Dir(absolute)
		}

		temporary, err = os.CreateTemp(directory, ".pakr-*")
		if err != nil {
			return err
		}

		defer os.Remove(temporary.Name())
		defer temporary.Close()

		target = temporary
	}

	written := countingWriter{output: target}

	if options.Log != nil && options.Format.Codec != native.BSC {
		target = &written
	}

	switch options.Format.Container {
	case "wim":
		err = temporary.Close()
		if err == nil {
			err = packWIM(temporary.Name(), inputs, entries, options, copying)
		}
	case "7z":
		err = temporary.Close()
		if err == nil {
			err = pack7z(temporary.Name(), entries, options, copying)
		}
	case "zip":
		err = packZip(target, entries, options, copying)
	case "ar", "iso":
		err = packNative(target, entries, options.Format.Container, copying)
	default:
		err = packStream(target, entries, options, copying)
	}

	if err != nil {
		return err
	}

	progress.Phase("finish", -1)

	err = ctx.Err()
	if err != nil {
		return err
	}

	size := written.bytes

	if (options.Format.Container == "7z" || options.Format.Container == "wim" || options.Format.Codec == native.BSC) && options.Log != nil {
		info, err := os.Stat(temporary.Name())
		if err != nil {
			return err
		}

		size = info.Size()
	}

	if temporary != nil {
		if options.Format.Container != "7z" && options.Format.Container != "wim" {
			err = temporary.Close()
			if err != nil {
				return err
			}
		}

		if output == "-" {
			file, err := os.Open(temporary.Name())
			if err != nil {
				return err
			}

			defer file.Close()

			// Publishing a seekable archive must not count the input data twice.
			publishing := *copying
			publishing.progress = nil

			_, err = publishing.copy(stdout, file)
			if err != nil {
				return err
			}
		} else {
			err = publish(temporary.Name(), absolute)
			if err != nil {
				return err
			}
		}
	}

	if output == "-" {
		output = "stdout"
	}

	progress.Complete("packed", output, size)

	return nil
}

func packStream(target io.Writer, entries []entry, options Options, copying *copier) error {
	var (
		codec io.WriteCloser
		err   error
	)

	if options.Format.Codec != 0 || options.Format.Name == "lzma" {
		if options.Format.Name == "lzma" {
			codec, err = native.NewLZMAWriter(copying.ctx, target, options.Level, options.Memory)
		} else if options.Seekable {
			codec, err = native.NewSeekableWriter(target, options.Level, options.Threads)
		} else if options.Format.Codec == native.Gzip {
			codec, err = native.NewGzipWriter(target, options.Level, options.Threads)
		} else if options.Format.Codec == native.Bzip2 && options.Threads > 1 {
			codec, err = native.NewBzip2Writer(target, options.Level, options.Threads)
		} else if options.Format.Codec == native.Bzip3 {
			codec, err = native.NewBzip3Writer(copying.ctx, target, options.Level, options.Threads, options.Memory)
		} else if options.Format.Codec == native.Kanzi {
			size := int64(-1)

			if options.Format.Container == "" && options.Stdin == nil {
				size = entries[0].header.Size
			}

			var stream *native.KanziStream

			stream, err = native.NewKanziWriter(copying.ctx, target, options.Level, options.Threads, options.Memory, size)
			if err == nil {
				codec = stream

				logKanzi(options, stream.Settings)
			}
		} else if options.Format.Codec == native.BSC {
			seekable, ok := target.(io.WriteSeeker)
			if !ok {
				return errors.New("BSC output requires seekable staging")
			}

			codec, err = native.NewBSCWriter(copying.ctx, seekable, options.Level, options.Threads, options.Memory)
		} else if options.Format.Codec == native.Lzip {
			size := int64(-1)

			if options.Format.Container == "" {
				size = entries[0].header.Size
			}

			codec, err = native.NewLzipWriter(copying.ctx, target, options.Level, options.Memory, size)
		} else {
			codec, err = native.NewWriterExtreme(target, options.Format.Codec, options.Level, options.Threads, options.Extreme)
		}

		if err != nil {
			return err
		}

		defer codec.Close()

		target = codec
	}

	if options.Format.Container == "tar" {
		err = packTar(target, entries, copying)
	} else if options.Format.Container == "cpio" {
		err = packNative(target, entries, "cpio", copying)
	} else if options.Format.Name == "Z" {
		err = packNative(target, entries, "Z", copying)
	} else if options.Stdin != nil {
		_, err = copying.copy(target, options.Stdin)
		copying.progress.File()
	} else {
		err = copying.file(target, &entries[0])
		copying.progress.File()
	}

	copying.progress.Phase("finish", -1)

	if codec != nil {
		return closeWithError(codec, err)
	}

	return err
}

func packTar(target io.Writer, entries []entry, copying *copier) error {
	archive := tar.NewWriter(target)

	for index := range entries {
		item := &entries[index]

		header := tar.Header{
			Name:     item.header.Name,
			Size:     item.header.Size,
			Mode:     int64(item.header.Mode),
			ModTime:  item.header.Modified,
			Linkname: item.header.Link,
			Typeflag: tar.TypeReg,
		}

		switch item.header.Kind {
		case native.Directory:
			header.Typeflag = tar.TypeDir
		case native.Symlink:
			header.Typeflag = tar.TypeSymlink
		}

		err := copying.ctx.Err()
		if err == nil {
			err = archive.WriteHeader(&header)
		}

		if err == nil && item.header.Kind == native.Regular {
			err = copying.file(archive, item)
		}

		if err != nil {
			return closeWithError(archive, err)
		}

		copying.progress.File()
	}

	copying.progress.Phase("finish", -1)

	return archive.Close()
}

func pack7z(filename string, entries []entry, options Options, copying *copier) error {
	archive, err := native.OpenArchive(filename, true, options.Level, options.Threads)
	if err != nil {
		return err
	}

	defer archive.Close()

	for index := range entries {
		item := &entries[index]

		err = copying.ctx.Err()
		if err == nil {
			err = archive.WriteHeader(item.header)
		}

		if err == nil && item.header.Kind == native.Regular {
			err = copying.file(archive, item)
		}

		if err != nil {
			return err
		}

		copying.progress.File()
	}

	copying.progress.Phase("finish", -1)

	return archive.Close()
}

func packNative(target io.Writer, entries []entry, format string, copying *copier) error {
	if format == "iso" {
		// Libarchive uses signed 32-bit block locations. Reserve a conservative
		// metadata bound per entry, including both directory trees and path tables.
		blocks := int64(1024)
		directories := 1

		for index := range entries {
			header := &entries[index].header
			blocks += header.Size/2048 + 65

			if header.Kind == native.Directory {
				directories++
			}

			if blocks > 0x7fffffff || directories > 65535 {
				return errors.New("ISO9660 image exceeds block-address or directory-count limits")
			}
		}
	}

	archive, err := native.NewArchiveWriter(copying.ctx, target, format)
	if err != nil {
		return err
	}

	defer archive.Close()

	for index := range entries {
		item := &entries[index]

		err = copying.ctx.Err()
		if err == nil {
			header := item.header

			if item.mode&os.ModeSetuid != 0 {
				header.Mode |= 0o4000
			}

			if item.mode&os.ModeSetgid != 0 {
				header.Mode |= 0o2000
			}

			if item.mode&os.ModeSticky != 0 {
				header.Mode |= 0o1000
			}

			err = archive.WriteHeader(header)
		}

		if err == nil && item.header.Kind == native.Regular {
			err = copying.file(archive, item)
		}

		if err != nil {
			return archive.Abort(err)
		}

		copying.progress.File()
	}

	copying.progress.Phase("finish", -1)

	return archive.Close()
}
