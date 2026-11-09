package pak

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/coalaura/pakr/internal/native"
)

var wimHeaderResources = [...]int{48, 72, 96, 124}

func wimPath(full, name string) (string, error) {
	// wimlib's full_path has exactly one leading separator, including the root.
	full = strings.ReplaceAll(full, "\\", "/")
	if full == "/" && name == "" {
		return ".", nil
	}

	if !strings.HasPrefix(full, "/") || strings.ContainsAny(name, "/\\:\x00") || !utf8.ValidString(full) {
		return "", fmt.Errorf("unsafe WIM path: %q", full)
	}

	full = full[1:]

	for component := range strings.SplitSeq(full, "/") {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, ":\x00<>\"|?*") || strings.TrimRight(component, " .") != component {
			return "", fmt.Errorf("unsafe WIM path: %q", full)
		}

		for _, character := range component {
			if character < 32 {
				return "", fmt.Errorf("unsafe WIM path: %q", full)
			}
		}

		base, _, _ := strings.Cut(strings.ToUpper(component), ".")

		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$"
		devicePrefix := strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")
		device := devicePrefix && (len(base) == 4 && base[3] >= '0' && base[3] <= '9' || strings.ContainsAny(base[3:], "\u00b9\u00b2\u00b3"))

		if reserved || device {
			return "", fmt.Errorf("reserved WIM path: %q", full)
		}
	}

	if path.Base(full) != name {
		return "", fmt.Errorf("inconsistent WIM path: %q", full)
	}

	return cleanName(full)
}

func wimHeader(item native.WIMEntry) (native.Header, error) {
	name, err := wimPath(item.Path, item.Name)
	if err != nil {
		return native.Header{}, err
	}

	header := native.Header{Name: name, Mode: item.Mode & 0o777, Modified: item.Modified}

	// File archives support regular data, directory and relative symlink entries.
	// Hidden, system, temporary and storage hints carry no separate contents.
	// Offline, encrypted, device and unknown attributes still fail closed.
	if item.Attributes & ^uint32(0x1|0x2|0x4|0x10|0x20|0x80|0x100|0x200|0x400|0x800|0x2000) != 0 || item.Size > math.MaxInt64 {
		return header, fmt.Errorf("unsupported WIM attributes or size: %s", name)
	}

	switch {
	case item.Attributes&0x400 != 0:
		if item.Reparse != 0xa000000c || item.Size < 12 || item.Size > 16<<10 {
			return header, fmt.Errorf("unsupported WIM reparse point: %s", name)
		}

		header.Kind = native.Symlink
	case item.Attributes&0x10 != 0:
		if item.Size != 0 {
			return header, fmt.Errorf("WIM directory contains data: %s", name)
		}

		header.Kind = native.Directory
	default:
		header.Kind = native.Regular
		header.Size = int64(item.Size)
	}

	fileType := item.Mode & 0o170000

	validType := fileType == 0 || fileType == 0o100000 && header.Kind == native.Regular || fileType == 0o040000 && header.Kind == native.Directory || fileType == 0o120000 && header.Kind == native.Symlink
	if !validType || name == "." && header.Kind != native.Directory {
		return header, fmt.Errorf("unsupported WIM entry type: %s", name)
	}

	if item.Mode == 0 {
		header.Mode = 0o644
		if header.Kind == native.Directory {
			header.Mode = 0o755
		}

		if item.Attributes&1 != 0 {
			header.Mode &^= 0o222
		}
	}

	return header, nil
}

func validateWIM(source io.ReaderAt, size int64, memory uint64) (uint64, error) {
	var header [208]byte

	_, err := source.ReadAt(header[:], 0)
	if err != nil {
		return 0, err
	}

	if string(header[:8]) != "MSWIM\x00\x00\x00" || binary.LittleEndian.Uint32(header[8:]) != 208 {
		return 0, errors.New("invalid WIM header (pipable WIMs are not supported)")
	}

	version := binary.LittleEndian.Uint32(header[12:])
	flags := binary.LittleEndian.Uint32(header[16:])
	chunk := binary.LittleEndian.Uint32(header[20:])

	if version != 0x10d00 && version != 0xe00 || flags&0x48 != 0 || binary.LittleEndian.Uint16(header[40:]) != 1 || binary.LittleEndian.Uint16(header[42:]) != 1 {
		return 0, errors.New("unsupported WIM version, split archive or incomplete write")
	}

	if chunk > 64<<20 {
		return 0, errors.New("unsupported WIM chunk size")
	}

	reserve := uint64(32<<20) + uint64(chunk)*2

	if version == 0xe00 {
		reserve += 128 << 20
	}

	for _, offset := range wimHeaderResources {
		stored := binary.LittleEndian.Uint64(header[offset:]) & (1<<56 - 1)
		start := binary.LittleEndian.Uint64(header[offset+8:])
		decoded := binary.LittleEndian.Uint64(header[offset+16:])

		if stored > uint64(size) || start > uint64(size)-stored || decoded > memory/16 {
			return 0, errors.New("WIM header resource exceeds file bounds or metadata memory budget")
		}

		if reserve >= memory || decoded > (memory-reserve)/8 {
			return 0, errors.New("WIM header resources exceed metadata memory budget")
		}

		reserve += decoded * 8
	}

	if reserve >= memory {
		return 0, fmt.Errorf("WIM decoding requires more than %d MiB of allocation budget", memory>>20)
	}

	return (memory - reserve) / 16, nil
}

func wimSymlink(data []byte) (string, error) {
	if len(data) < 12 || binary.LittleEndian.Uint32(data[8:]) != 1 {
		return "", errors.New("WIM symlink must have a relative target")
	}

	offset := int(binary.LittleEndian.Uint16(data))
	size := int(binary.LittleEndian.Uint16(data[2:]))
	printOffset := int(binary.LittleEndian.Uint16(data[4:]))
	printSize := int(binary.LittleEndian.Uint16(data[6:]))

	invalidPrint := printOffset%2 != 0 || printSize%2 != 0 || printOffset > len(data)-12 || printSize > len(data)-12-printOffset
	if invalidPrint || offset%2 != 0 || size%2 != 0 || offset > len(data)-12 || size > len(data)-12-offset {
		return "", errors.New("invalid WIM symlink data")
	}

	encoded := data[12+offset : 12+offset+size]
	units := make([]uint16, len(encoded)/2)

	for index := range units {
		units[index] = binary.LittleEndian.Uint16(encoded[index*2:])
	}

	for index := 0; index < len(units); index++ {
		unit := units[index]
		if utf16.IsSurrogate(rune(unit)) {
			if unit > 0xdbff || index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return "", errors.New("invalid UTF-16 WIM symlink target")
			}

			index++
		}
	}

	return strings.ReplaceAll(string(utf16.Decode(units)), "\\", "/"), nil
}
