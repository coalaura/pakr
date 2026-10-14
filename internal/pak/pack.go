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
	Level    int
	Log      *minimal.Minimal
	Progress io.Writer
}

func Pack(ctx context.Context, inputs []string, options Options, stdout io.Writer) error {
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

	entries, total, err := collect(ctx, inputs, absolute)
	if err != nil {
		return err
	}

	if options.Format.Container == "" && (len(entries) != 1 || entries[0].header.Kind != native.Regular) {
		return fmt.Errorf("%s compresses one regular file; use tar.%s for files or directories", options.Format.Name, options.Format.Name)
	}

	progress.Phase("pack", total)

	copying := &copier{ctx: ctx, progress: progress, buffer: make([]byte, native.BufferSize)}

	var (
		temporary *os.File
		target    = stdout
	)

	if output != "-" || options.Format.Container == "7z" {
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

	if options.Log != nil {
		target = &written
	}

	switch options.Format.Container {
	case "7z":
		err = temporary.Close()
		if err == nil {
			err = pack7z(temporary.Name(), entries, options, copying)
		}
	case "zip":
		err = packZip(target, entries, options, copying)
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

	if options.Format.Container == "7z" && options.Log != nil {
		info, err := os.Stat(temporary.Name())
		if err != nil {
			return err
		}

		size = info.Size()
	}

	if temporary != nil {
		if options.Format.Container != "7z" {
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

	if options.Format.Codec != 0 {
		if options.Format.Codec == native.Gzip {
			codec, err = native.NewGzipWriter(target, options.Level, options.Threads)
		} else {
			codec, err = native.NewWriter(target, options.Format.Codec, options.Level, options.Threads)
		}

		if err != nil {
			return err
		}

		defer codec.Close()

		target = codec
	}

	if options.Format.Container == "tar" {
		err = packTar(target, entries, copying)
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
