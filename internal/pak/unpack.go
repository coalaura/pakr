package pak

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/coalaura/pakr/internal/native"
)

func Unpack(ctx context.Context, input string, options Options, stdin io.Reader) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	budget := newExtractionBudget(ctx, options.Limits, cancel)
	inputName := input

	var (
		source io.Reader
		file   *os.File
		size   int64
		err    error
	)

	output := options.Output
	if output == "" {
		output = "."
	}

	unpackPreview(options.Log, input, output)

	progress := NewProgress(options.Log, options.Progress, "read")
	defer progress.Close()

	if input == "-" {
		buffered := bufio.NewReaderSize(stdin, 512)

		if options.Format.Name == "" {
			header, readErr := buffered.Peek(512)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
			}

			options.Format, err = detectHeader(header)
			if err == nil {
				source, options.Format, err = probeCPIOStream(buffered, options.Format)
				if err != nil {
					return err
				}
			}
		}

		if options.Format.Name == "" || options.Format.nativeExtraction() || options.Format.Container == "zip" || options.Format.Container == "7z" || options.Format.Container == "rar" || options.Format.Codec == native.BSC {
			temporary, err := os.CreateTemp("", "pakr-input-*")
			if err != nil {
				return err
			}

			defer os.Remove(temporary.Name())

			copying := copier{ctx: ctx, buffer: make([]byte, native.BufferSize)}

			inputSource := source
			if inputSource == nil {
				inputSource = buffered
			}

			_, err = copying.copy(temporary, inputSource)

			err = closeWithError(temporary, err)
			if err != nil {
				return err
			}

			input = temporary.Name()
			source = nil
		} else if source == nil {
			source = buffered
		}
	}

	if source == nil {
		file, err = os.Open(input)
		if err != nil {
			return err
		}

		defer file.Close()

		info, err := file.Stat()
		if err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return errors.New("input must be a regular archive file or stdin (-)")
		}

		size = info.Size()
		source = file

		if options.Format.Name == "" {
			options.Format, err = InferFormat(input)
			probeCPIO := options.Format.Container == "" && (options.Format.Codec == native.Gzip || options.Format.Codec == native.Bzip2 || options.Format.Codec == native.XZ || options.Format.Codec == native.Zstd)

			if err != nil || probeCPIO {
				options.Format, err = detectFormat(file)
				if err != nil {
					return err
				}
			}
		}
	}

	if output == "-" && options.Format.Name == "knz" {
		return unpackKanziStdout(source, options, budget, progress)
	}

	err = os.MkdirAll(output, 0o755)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}

	defer root.Close()

	progress.Phase("unpack", 0)

	extract := extractor{root: root, budget: budget, copying: &copier{ctx: ctx, progress: progress, buffer: make([]byte, native.BufferSize)}}

	switch options.Format.Container {
	case "zip":
		err = unpackZip(file, size, options.Threads, &extract)
	case "7z":
		err = unpackArchive(input, "7z", -1, &extract)
	case "rar":
		var info rarInfo

		info, err = rarFormat(ctx, file, size)
		if err == nil {
			err = unpackArchive(input, info.format, info.entries, &extract)
		}
	case "iso":
		err = validateISO(file, size)
		if err == nil {
			err = unpackArchive(input, options.Format.Name, -1, &extract)
		}
	case "cab", "cpio", "ar", "lha", "rpm":
		err = unpackArchive(input, options.Format.Name, -1, &extract)
	case "raw":
		err = unpackLegacyStream(input, inputName, options.Format.Name, &extract)
	default:
		err = unpackStream(source, file, size, inputName, options, &extract)
	}

	if err != nil {
		return errors.Join(err, context.Cause(ctx))
	}

	progress.Phase("finish", -1)

	err = ctx.Err()
	if err == nil {
		err = extract.finish()
	}

	if err != nil {
		return err
	}

	progress.Complete("unpacked", output, -1)

	return nil
}

func unpackStream(source io.Reader, file *os.File, size int64, input string, options Options, extract *extractor) error {
	var (
		codec io.ReadCloser
		err   error
	)

	switch options.Format.Codec {
	case native.Kanzi:
		var stream *native.KanziStream

		stream, err = native.NewKanziReader(extract.copying.ctx, source, options.Threads, options.Memory)
		if err == nil {
			codec = stream

			logKanzi(options, stream.Settings)
		}
	case native.BSC:
		codec, err = native.NewBSCReader(extract.copying.ctx, file, size, options.Threads, options.Memory)
	case native.Lzip:
		codec, err = native.NewLzipReader(extract.copying.ctx, source, options.Memory)
	case native.Bzip3:
		codec, err = native.NewBzip3Reader(extract.copying.ctx, source, options.Threads, options.Memory)
	case native.Gzip, native.Bzip2:
		if file != nil && size >= 1<<20 && options.Threads > 1 {
			codec, err = native.OpenParallel(file.Name(), options.Format.Codec, min(options.Threads, 16))
		} else {
			codec, err = native.NewReader(source, options.Format.Codec, 1)
		}
	case 0:
	case native.Zstd:
		if file != nil {
			var seekable *native.SeekableReader

			seekable, err = native.OpenSeekable(file, size, options.Threads)
			if seekable != nil {
				codec = seekable
			}
		}

		if codec == nil && err == nil {
			codec, err = native.NewReader(source, native.Zstd, options.Threads)
		}
	default:
		codec, err = native.NewReader(source, options.Format.Codec, options.Threads)
	}

	if err != nil {
		return err
	}

	if codec != nil {
		defer codec.Close()
		source = codec
	}

	if options.Format.Container == "tar" {
		err = unpackTar(source, extract)
		if err == nil {
			// Tar's end marker precedes the codec footer; drain to validate its checksum.
			extract.copying.progress.Phase("finish", -1)

			draining := *extract.copying
			draining.progress = nil

			_, err = draining.copy(io.Discard, source)
		}

		return err
	}

	header := native.Header{Name: rawOutputName(input), Size: -1, Mode: 0o644}

	if decoded, ok := codec.(*native.BSCReader); ok {
		header.Size = decoded.Size()
	}

	return extract.entry(header, source)
}

func unpackTar(source io.Reader, extract *extractor) error {
	archive := tar.NewReader(source)

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		item := native.Header{Name: header.Name, Size: header.Size, Mode: uint32(header.Mode), Modified: header.ModTime, Link: header.Linkname}

		switch header.Typeflag {
		case tar.TypeReg:
			item.Kind = native.Regular
		case tar.TypeDir:
			item.Kind = native.Directory
		case tar.TypeSymlink:
			item.Kind = native.Symlink
		case tar.TypeLink:
			item.Kind = native.Hardlink
		default:
			return fmt.Errorf("unsupported tar entry type: %s", header.Name)
		}

		err = extract.entry(item, archive)
		if err != nil {
			return err
		}
	}
}

func unpackArchive(input, format string, entries int64, extract *extractor) (result error) {
	archive, err := native.OpenArchiveReader(input, format)
	if err != nil {
		return err
	}

	defer func() {
		result = errors.Join(result, archive.Close())
	}()

	cpio := format == "rpm" || strings.HasPrefix(format, "cpio")

	var links cpioLinks

	if cpio {
		// Count entries on arrival, including deferred payload-last hardlinks.
		extract.prechecked = true

		defer func() {
			extract.prechecked = false
		}()
	}

	for {
		err = extract.copying.ctx.Err()
		if err != nil {
			return err
		}

		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if entries > 0 {
				return errors.New("native decoder omitted RAR entries")
			}

			if cpio {
				err = finishCPIO(archive, &links, extract)
				if err != nil {
					return err
				}
			}

			return archive.Close()
		}

		if err != nil {
			return err
		}

		if entries == 0 {
			return errors.New("native decoder returned unexpected RAR entries")
		}

		if entries > 0 {
			entries--
		}

		if cpio {
			err = extract.budget.entry(header.Size)
			if err == nil {
				err = links.entry(header, archive, extract)
			}
		} else {
			err = extract.entry(header, archive)
		}

		if err != nil {
			return err
		}
	}
}

func probeCPIOStream(input *bufio.Reader, format Format) (io.Reader, Format, error) {
	switch format.Codec {
	case native.Gzip, native.Bzip2, native.XZ, native.Zstd:
	default:
		return input, format, nil
	}

	var recorded bytes.Buffer

	// Limit before recording: decoder read-ahead must also fit in the replay buffer.
	limited := io.LimitReader(input, cpioProbeInputLimit)
	format, err := probeCPIO(io.TeeReader(limited, &recorded), format)
	source := io.MultiReader(bytes.NewReader(recorded.Bytes()), input)

	return source, format, err
}

func detectFormat(file *os.File) (Format, error) {
	var header [512]byte

	count, err := file.ReadAt(header[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return Format{}, err
	}

	format, err := detectHeader(header[:count])
	if err != nil {
		count, readErr := file.ReadAt(header[:7], 16*2048)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Format{}, readErr
		}

		if count == 7 && string(header[1:6]) == "CD001" && header[6] == 1 {
			return ParseFormat("iso")
		}

		return Format{}, err
	}

	switch format.Codec {
	case native.Gzip, native.Bzip2, native.XZ, native.Zstd:
		return probeCPIO(io.NewSectionReader(file, 0, cpioProbeInputLimit), format)
	}

	return format, nil
}

func detectHeader(data []byte) (Format, error) {
	name := ""

	switch {
	case bytes.HasPrefix(data, []byte("MSCF")):
		name = "cab"
	case bytes.HasPrefix(data, []byte("!<arch>\n")):
		name = "ar"
	case bytes.HasPrefix(data, []byte{0xed, 0xab, 0xee, 0xdb}):
		name = "rpm"
	case validCPIOHeader(data):
		name = "cpio"
	case bytes.HasPrefix(data, []byte{0x1f, 0x9d}):
		name = "Z"
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		name = "gz"
	case bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		name = "zst"
	case bytes.HasPrefix(data, []byte("BZh")):
		name = "bz2"
	case bytes.HasPrefix(data, []byte("BZ3v1")):
		name = "bz3"
	case bytes.HasPrefix(data, []byte("bsc1")):
		name = "bsc"
	case bytes.HasPrefix(data, []byte("KANZ")):
		name = "knz"
	case bytes.HasPrefix(data, []byte("LZIP")):
		name = "lz"
	case bytes.HasPrefix(data, []byte{0xfd, '7', 'z', 'X', 'Z', 0}):
		name = "xz"
	case bytes.HasPrefix(data, []byte{0x04, 0x22, 0x4d, 0x18}):
		name = "lz4"
	case bytes.HasPrefix(data, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		name = "7z"
	case bytes.HasPrefix(data, []byte{'P', 'K', 3, 4}), bytes.HasPrefix(data, []byte{'P', 'K', 5, 6}):
		name = "zip"
	case bytes.HasPrefix(data, []byte("Rar!\x1a\x07\x00")), bytes.HasPrefix(data, []byte("Rar!\x1a\x07\x01\x00")):
		name = "rar"
	case len(data) >= 262 && string(data[257:262]) == "ustar":
		name = "tar"
	default:
		return Format{}, errors.New("unrecognized archive; specify --format (required for brotli, legacy lzma, LHA and extensionless compressed tar)")
	}

	return ParseFormat(name)
}

func rawOutputName(input string) string {
	if input == "-" {
		return "stdin"
	}

	name := filepath.Base(input)

	extension := filepath.Ext(name)
	if extension == "" {
		return name + ".out"
	}

	return strings.TrimSuffix(name, extension)
}
