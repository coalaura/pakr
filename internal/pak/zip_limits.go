package pak

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/binary"
	"io"
)

// Count central-directory records before archive/zip allocates its File slice.
// Scan actual records too: ZIP's 16-bit entry count may wrap or be misleading.
func checkZipEntries(source io.ReaderAt, size int64, budget *extractionBudget) error {
	limit := budget.limits.MaxEntries
	if limit == 0 {
		return nil
	}

	buffer := make([]byte, min(size, 65*1024))

	_, err := source.ReadAt(buffer, size-int64(len(buffer)))
	if err != nil {
		return err
	}

	position := len(buffer) - 22

	for position >= 0 {
		if binary.LittleEndian.Uint32(buffer[position:]) == 0x06054b50 && int(binary.LittleEndian.Uint16(buffer[position+20:])) <= len(buffer)-position-22 {
			break
		}

		position--
	}

	if position < 0 {
		return zip.ErrFormat
	}

	end := size - int64(len(buffer)) + int64(position)
	footer := buffer[position:]

	count := uint64(binary.LittleEndian.Uint16(footer[10:]))
	length := uint64(binary.LittleEndian.Uint32(footer[12:]))
	offset := uint64(binary.LittleEndian.Uint32(footer[16:]))

	if count == 0xffff || length == 0xffffffff || offset == 0xffffffff {
		var locator [20]byte

		_, err = source.ReadAt(locator[:], end-20)
		if err != nil {
			return err
		}

		if binary.LittleEndian.Uint32(locator[:]) == 0x07064b50 && binary.LittleEndian.Uint32(locator[4:]) == 0 && binary.LittleEndian.Uint32(locator[16:]) == 1 {
			end = int64(binary.LittleEndian.Uint64(locator[8:]))

			var extended [56]byte

			_, err = source.ReadAt(extended[:], end)
			if err != nil {
				return err
			}

			if binary.LittleEndian.Uint32(extended[:]) != 0x06064b50 {
				return zip.ErrFormat
			}

			count = binary.LittleEndian.Uint64(extended[32:])
			length = binary.LittleEndian.Uint64(extended[40:])
			offset = binary.LittleEndian.Uint64(extended[48:])
		}
	}

	if count > limit {
		return budget.reject("archive entries", limit)
	}

	if end < 0 || length > uint64(end) || offset >= uint64(size) {
		return zip.ErrFormat
	}

	start := end - int64(length)

	// archive/zip may choose either origin for an incorrect base offset. Check
	// both, rather than letting a malformed decoy header bypass the preflight.
	if uint64(start) > offset {
		err = countZipEntries(source, int64(offset), size, budget)
		if err != nil {
			return err
		}
	}

	return countZipEntries(source, start, size, budget)
}

func countZipEntries(source io.ReaderAt, start, size int64, budget *extractionBudget) error {
	reader := bufio.NewReader(io.NewSectionReader(source, start, size-start))

	var (
		header [46]byte
		count  uint64
	)

	for {
		err := context.Cause(budget.ctx)
		if err != nil {
			return err
		}

		_, err = io.ReadFull(reader, header[:])
		if err != nil || binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return nil // archive/zip performs complete structural validation.
		}

		if count == budget.limits.MaxEntries {
			return budget.reject("archive entries", budget.limits.MaxEntries)
		}

		count++

		extra := int64(binary.LittleEndian.Uint16(header[28:])) + int64(binary.LittleEndian.Uint16(header[30:])) + int64(binary.LittleEndian.Uint16(header[32:]))

		_, err = io.CopyN(io.Discard, reader, extra)
		if err != nil {
			return nil
		}
	}
}
