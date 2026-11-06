package pak

import (
	"archive/zip"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"github.com/coalaura/pakr/internal/native"
)

type zipChecksumReader struct {
	source   io.Reader
	crc      uint32
	expected uint32
}

func (reader *zipChecksumReader) Read(data []byte) (int, error) {
	count, err := reader.source.Read(data)
	reader.crc = crc32.Update(reader.crc, crc32.IEEETable, data[:count])

	if errors.Is(err, io.EOF) && reader.crc != reader.expected {
		err = zip.ErrChecksum
	}

	return count, err
}

func unpackZipAdvanced(input string, files []*zip.File, extract *extractor) (result error) {
	archive, err := native.OpenArchiveReader(input, "zip")
	if err != nil {
		return err
	}

	defer func() {
		result = errors.Join(result, archive.Close())
	}()

	// libarchive visits local-header order, which need not be central-directory
	// order. Require an exact, one-to-one match with the preflighted entries.
	remaining := make(map[string]*zip.File, len(files))

	for _, file := range files {
		remaining[file.Name] = file
	}

	for {
		err = extract.copying.ctx.Err()
		if err != nil {
			return err
		}

		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if len(remaining) != 0 {
				return errors.New("ZIP decoder omitted central-directory entries")
			}

			return nil
		}

		if err != nil {
			return fmt.Errorf("read ZIP header: %w", err)
		}

		file, exists := remaining[header.Name]
		if !exists {
			return fmt.Errorf("ZIP header disagrees with central directory: %q", header.Name)
		}

		delete(remaining, header.Name)

		kind := native.Regular

		if file.FileInfo().IsDir() {
			kind = native.Directory
		} else if file.Mode()&os.ModeSymlink != 0 {
			kind = native.Symlink
		}

		if header.Kind != kind || kind != native.Symlink && header.Size != int64(file.UncompressedSize64) {
			return fmt.Errorf("ZIP entry metadata disagrees with central directory: %q", file.Name)
		}

		if file.Method == zip.Store || file.Method == zip.Deflate {
			continue
		}

		item := zipHeader(file)
		item.Kind = kind
		item.Link = header.Link

		if kind == native.Symlink {
			if uint64(len(header.Link)) != file.UncompressedSize64 || crc32.ChecksumIEEE([]byte(header.Link)) != file.CRC32 {
				return fmt.Errorf("ZIP symbolic link %q: %w", file.Name, zip.ErrChecksum)
			}
		}

		reader := zipChecksumReader{source: archive, expected: file.CRC32}

		err = extract.entry(item, &reader)
		if err != nil {
			return err
		}
	}
}
