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
)

type Options struct {
	Format   Format
	Output   string
	Threads  int
	Level    int
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

	var progress *Progress

	if options.Progress != nil {
		progress = NewProgress(options.Progress, total)
		defer progress.Close()
	}

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

	if temporary == nil {
		return nil
	}

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

		_, err = io.CopyBuffer(stdout, file, copying.buffer)
		return err
	}

	return publish(temporary.Name(), absolute)
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

	return archive.Close()
}
