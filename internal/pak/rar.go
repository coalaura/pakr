package pak

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

type rarInfo struct {
	format  string
	entries int64
}

func (info *rarInfo) checkRAR4(ctx context.Context, source io.ReaderAt, size int64) error {
	var prefix [7]byte

	buffer := make([]byte, 0, 256)
	main := false

	for offset := int64(7); offset < size; {
		err := ctx.Err()
		if err != nil {
			return err
		}

		_, err = source.ReadAt(prefix[:], offset)
		if err != nil {
			return fmt.Errorf("truncated RAR4 header: %w", err)
		}

		length := int(binary.LittleEndian.Uint16(prefix[5:]))
		if length < len(prefix) || int64(length) > size-offset {
			return errors.New("invalid RAR4 header size")
		}

		buffer = resize(buffer, length)

		_, err = source.ReadAt(buffer, offset)
		if err != nil {
			return err
		}

		if uint16(crc32.ChecksumIEEE(buffer[2:])) != binary.LittleEndian.Uint16(buffer) {
			return errors.New("RAR4 header checksum mismatch")
		}

		kind := buffer[2]
		flags := binary.LittleEndian.Uint16(buffer[3:])
		dataSize := uint64(0)

		if flags&0x8000 != 0 {
			if length < 11 {
				return errors.New("invalid RAR4 data header")
			}

			dataSize = uint64(binary.LittleEndian.Uint32(buffer[7:]))
		}

		switch kind {
		case 0x73:
			if main || length < 13 {
				return errors.New("invalid RAR4 main header")
			}

			main = true

			if flags&1 != 0 {
				return errors.New("multipart RAR4 archives are not supported")
			}

			if flags&0x80 != 0 {
				return errors.New("encrypted RAR4 archives are not supported")
			}
		case 0x74:
			if !main || length < 32 || flags&0x8000 == 0 {
				return errors.New("invalid RAR4 file header")
			}

			if flags&3 != 0 {
				return errors.New("split RAR4 entries are not supported")
			}

			if flags&4 != 0 {
				return errors.New("encrypted RAR4 entries are not supported")
			}

			info.entries++

			if flags&0x100 != 0 {
				if length < 40 {
					return errors.New("invalid RAR4 large file header")
				}

				dataSize |= uint64(binary.LittleEndian.Uint32(buffer[32:])) << 32
			}
		case 0x7b:
			if flags&1 != 0 {
				return errors.New("multipart RAR4 archives are not supported")
			}

			if !main {
				return errors.New("missing RAR4 main header")
			}

			if dataSize != 0 {
				return errors.New("invalid RAR4 end header")
			}

			return nil
		case 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a:
			// Comments, recovery records and other non-file metadata.
		default:
			return fmt.Errorf("unsupported RAR4 header type: %#x", kind)
		}

		offset += int64(length)
		if dataSize > uint64(size-offset) {
			return errors.New("truncated RAR4 data")
		}

		offset += int64(dataSize)
	}

	return errors.New("truncated RAR4 archive: missing end header")
}

func (info *rarInfo) checkRAR5(ctx context.Context, source io.ReaderAt, size int64) error {
	var prefix [7]byte

	buffer := make([]byte, 0, 256)
	main := false

	for offset := int64(8); offset < size; {
		err := ctx.Err()
		if err != nil {
			return err
		}

		_, err = source.ReadAt(prefix[:], offset)
		if err != nil {
			return fmt.Errorf("truncated RAR5 header: %w", err)
		}

		length, count := binary.Uvarint(prefix[4:])
		if count <= 0 || length > 2<<20 || length < 2 {
			return errors.New("invalid RAR5 header size")
		}

		total := 4 + count + int(length)
		if int64(total) > size-offset {
			return errors.New("truncated RAR5 header")
		}

		buffer = resize(buffer, total)

		_, err = source.ReadAt(buffer, offset)
		if err != nil {
			return err
		}

		if crc32.ChecksumIEEE(buffer[4:]) != binary.LittleEndian.Uint32(buffer) {
			return errors.New("RAR5 header checksum mismatch")
		}

		header := buffer[4+count:]

		kind, err := rarInteger(&header)
		if err != nil {
			return err
		}

		flags, err := rarInteger(&header)
		if err != nil {
			return err
		}

		if flags&0x18 != 0 {
			return errors.New("split RAR5 entries are not supported")
		}

		extraSize := uint64(0)

		if flags&1 != 0 {
			extraSize, err = rarInteger(&header)
			if err != nil {
				return err
			}
		}

		dataSize := uint64(0)

		if flags&2 != 0 {
			dataSize, err = rarInteger(&header)
			if err != nil {
				return err
			}
		}

		if extraSize > uint64(len(header)) || dataSize > uint64(size-offset-int64(total)) {
			return errors.New("invalid RAR5 extra or data size")
		}

		extra := header[len(header)-int(extraSize):]
		header = header[:len(header)-int(extraSize)]

		switch kind {
		case 1, 5:
			archiveFlags, err := rarInteger(&header)
			if err != nil {
				return err
			}

			if archiveFlags&1 != 0 {
				return errors.New("multipart RAR5 archives are not supported")
			}

			if kind == 5 {
				if !main || dataSize != 0 {
					return errors.New("invalid RAR5 end header or missing main header")
				}

				return nil
			}

			if main {
				return errors.New("duplicate RAR5 main header")
			}

			main = true
		case 2, 3:
			if !main {
				return errors.New("missing RAR5 main header")
			}

			err = checkRAR5Extras(extra)
			if err != nil {
				return err
			}

			if kind == 2 {
				info.entries++
			}
		case 4:
			return errors.New("encrypted RAR5 archives are not supported")
		default:
			return fmt.Errorf("unsupported RAR5 header type: %d", kind)
		}

		offset += int64(total)
		if dataSize > uint64(size-offset) {
			return errors.New("truncated RAR5 data")
		}

		offset += int64(dataSize)
	}

	return errors.New("truncated RAR5 archive: missing end header")
}

// Check framing independently of the decoder: libarchive can report EOF at a
// header boundary without noticing a missing end marker or a subsequent volume.
// Payloads are skipped, never buffered, and header storage is bounded by the spec.
func rarFormat(ctx context.Context, source io.ReaderAt, size int64) (rarInfo, error) {
	var (
		signature [8]byte
		info      rarInfo
	)

	_, err := source.ReadAt(signature[:], 0)
	if err != nil {
		return info, err
	}

	switch {
	case bytes.Equal(signature[:7], []byte("Rar!\x1a\x07\x00")):
		info.format = "rar4"
		err = info.checkRAR4(ctx, source, size)
	case bytes.Equal(signature[:], []byte("Rar!\x1a\x07\x01\x00")):
		info.format = "rar5"
		err = info.checkRAR5(ctx, source, size)
	default:
		err = errors.New("invalid RAR4/RAR5 signature")
	}

	return info, err
}

func checkRAR5Extras(extra []byte) error {
	for len(extra) != 0 {
		length, err := rarInteger(&extra)
		if err != nil || length == 0 || length > uint64(len(extra)) {
			return errors.New("invalid RAR5 extra record size")
		}

		record := extra[:int(length)]
		extra = extra[int(length):]

		kind, err := rarInteger(&record)
		if err != nil {
			return err
		}

		switch kind {
		case 1:
			return errors.New("encrypted RAR5 entries are not supported")
		case 5:
			redirect, err := rarInteger(&record)
			if err != nil {
				return err
			}

			if redirect != 1 && redirect != 2 && redirect != 4 {
				return fmt.Errorf("unsupported RAR5 link type: %d", redirect)
			}
		}
	}

	return nil
}

func rarInteger(data *[]byte) (uint64, error) {
	value, count := binary.Uvarint(*data)
	if count <= 0 {
		return 0, errors.New("invalid RAR5 header integer")
	}

	*data = (*data)[count:]

	return value, nil
}
