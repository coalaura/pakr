package pak

import (
	"encoding/binary"
	"io"

	"github.com/coalaura/pakr/internal/native"
)

const (
	// A BZIP2 block can require up to 900 KiB of input before producing output.
	cpioProbeInputLimit  = 1 << 20
	cpioProbeHeaderLimit = 110
)

func probeCPIO(input io.Reader, format Format) (Format, error) {
	decoder, err := native.NewReader(input, format.Codec, 1)
	if err != nil {
		return Format{}, err
	}

	defer decoder.Close()

	var header [cpioProbeHeaderLimit]byte

	count, _ := io.ReadFull(decoder, header[:])
	if validCPIOHeader(header[:count]) {
		return ParseFormat("cpio." + format.Name)
	}

	// An exhausted probe or invalid candidate is inconclusive. Actual extraction
	// restarts from the original bytes and validates the complete codec stream.
	return format, nil
}

func validCPIOHeader(data []byte) bool {
	if len(data) < 6 {
		return false
	}

	var (
		mode     uint64
		links    uint64
		nameSize uint64
		size     uint64
	)

	switch string(data[:6]) {
	case "070701", "070702":
		if len(data) < 110 {
			return false
		}

		var fields [13]uint64

		for index := range fields {
			value, valid := cpioNumber(data[6+index*8:14+index*8], 16)
			if !valid {
				return false
			}

			fields[index] = value
		}

		if string(data[:6]) == "070701" && fields[12] != 0 {
			return false
		}

		mode = fields[1]
		links = fields[4]
		size = fields[6]
		nameSize = fields[11]
	case "070707":
		if len(data) < 76 {
			return false
		}

		var fields [10]uint64

		offset := 6

		for index := range fields {
			width := 6

			if index == 7 || index == 9 {
				width = 11
			}

			value, valid := cpioNumber(data[offset:offset+width], 8)
			if !valid {
				return false
			}

			fields[index] = value
			offset += width
		}

		mode = fields[2]
		links = fields[5]
		nameSize = fields[8]
		size = fields[9]
	default:
		if len(data) < 26 {
			return false
		}

		var order binary.ByteOrder

		switch string(data[:2]) {
		case "\xc7\x71":
			order = binary.LittleEndian
		case "\x71\xc7":
			order = binary.BigEndian
		default:
			return false
		}

		mode = uint64(order.Uint16(data[6:8]))
		links = uint64(order.Uint16(data[12:14]))
		nameSize = uint64(order.Uint16(data[20:22]))
		size = uint64(order.Uint16(data[22:24]))<<16 | uint64(order.Uint16(data[24:26]))
	}

	// Match libarchive's pathname bound without reading or allocating the name.
	if nameSize < 2 || nameSize > 1<<20 || mode > 0xffff {
		return false
	}

	if mode == 0 {
		return nameSize == 11 && size == 0 // TRAILER!!!
	}

	if links == 0 {
		return false
	}

	kind := mode & 0o170000

	switch kind {
	case 0o010000, 0o020000, 0o040000, 0o060000, 0o100000, 0o120000, 0o140000:
		return true
	default:
		return false
	}
}

func cpioNumber(data []byte, base byte) (uint64, bool) {
	var value uint64

	for _, digit := range data {
		switch {
		case digit >= '0' && digit <= '9':
			digit -= '0'
		case digit >= 'a' && digit <= 'f':
			digit -= 'a' - 10
		case digit >= 'A' && digit <= 'F':
			digit -= 'A' - 10
		default:
			return 0, false
		}

		if digit >= base {
			return 0, false
		}

		value = value*uint64(base) + uint64(digit)
	}

	return value, true
}
