package native

/*
#include <archive.h>
#include <stdint.h>
#include <stdlib.h>
int pakr_archive_open_writer(struct archive *archive, uintptr_t handle);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime/cgo"
	"strings"
	"unicode/utf8"
	"unsafe"
)

var errArchiveClosed = errors.New("archive writer is closed")

// Abort releases staging files without generating the remainder of a failed image.
func (archive *Archive) Abort(err error) error {
	archive.err = errors.Join(archive.err, err)

	return archive.Close()
}

// NewArchiveWriter streams output through the existing pack destination. ISO9660
// stages file contents internally; the Go callback also checks cancellation during close.
func NewArchiveWriter(ctx context.Context, target io.Writer, format string) (*Archive, error) {
	err := context.Cause(ctx)
	if err != nil {
		return nil, err
	}

	if !archiveLocaleAvailable {
		return nil, errors.New("a UTF-8 C locale is required for libarchive archives")
	}

	archive := &Archive{archive: C.archive_write_new(), writing: true, format: format, ctx: ctx, target: target}
	if archive.archive == nil {
		return nil, errors.New("allocate libarchive writer")
	}

	var status C.int

	switch format {
	case "cpio":
		status = C.archive_write_set_format_cpio_newc(archive.archive)
		if status == C.ARCHIVE_OK {
			options := C.CString("cpio:hdrcharset=UTF-8")
			status = C.archive_write_set_options(archive.archive, options)

			C.free(unsafe.Pointer(options))
		}
	case "ar":
		status = C.archive_write_set_format_ar_bsd(archive.archive)
	case "iso":
		status = C.archive_write_set_format_iso9660(archive.archive)
		if status == C.ARCHIVE_OK {
			options := C.CString("iso9660:iso-level=2,iso9660:rockridge=strict,iso9660:joliet")
			status = C.archive_write_set_options(archive.archive, options)

			C.free(unsafe.Pointer(options))
		}
	case "Z":
		status = C.archive_write_set_format_raw(archive.archive)
		if status == C.ARCHIVE_OK {
			status = C.archive_write_add_filter_compress(archive.archive)
		}
	default:
		return nil, archive.Abort(fmt.Errorf("unsupported archive writer: %s", format))
	}

	err = archive.check(status)
	if err == nil {
		// Batch native callbacks and file writes without padding the final block.
		err = archive.check(C.archive_write_set_bytes_per_block(archive.archive, BufferSize))
	}

	if err == nil {
		// Do not pad raw streams or AR archives to libarchive's default tape block.
		err = archive.check(C.archive_write_set_bytes_in_last_block(archive.archive, 1))
	}

	if err == nil {
		archive.handle = cgo.NewHandle(archive)
		err = archive.check(C.pakr_archive_open_writer(archive.archive, C.uintptr_t(archive.handle)))
	}

	if err != nil {
		return nil, archive.Abort(err)
	}

	return archive, nil
}

//export pakrArchiveWrite
func pakrArchiveWrite(handle C.uintptr_t, data unsafe.Pointer, size C.size_t) C.la_ssize_t {
	archive := cgo.Handle(handle).Value().(*Archive)
	if archive.err == nil {
		archive.err = context.Cause(archive.ctx)
	}

	if archive.err != nil {
		return -1
	}

	// The callback is synchronous; the writer must not retain libarchive's buffer.
	buffer := unsafe.Slice((*byte)(data), int(size))
	count, err := archive.target.Write(buffer)

	if err == nil && count != len(buffer) {
		err = io.ErrShortWrite
	}

	if err == nil {
		err = context.Cause(archive.ctx)
	}

	archive.err = err

	if err != nil {
		return -1
	}

	return C.la_ssize_t(count)
}

func validateArchiveHeader(header Header, format string) error {
	if header.Name == "" || strings.ContainsRune(header.Name, 0) || !utf8.ValidString(header.Name) || strings.ContainsRune(header.Link, 0) || !utf8.ValidString(header.Link) {
		return errors.New("archive names and link targets must be nonempty UTF-8 names without NUL bytes")
	}

	if header.Size < 0 || header.Mode > 0o7777 {
		return errors.New("unrepresentable archive size or mode")
	}

	if header.Kind != Regular && header.Kind != Directory && header.Kind != Symlink {
		return errors.New("unsupported archive entry type")
	}

	seconds := header.Modified.Unix()

	switch format {
	case "cpio":
		if header.Name == "TRAILER!!!" || uint64(len(header.Name)) >= math.MaxUint32 || uint64(len(header.Link)) > math.MaxUint32 || header.Size > math.MaxUint32 || seconds < 0 || seconds > math.MaxUint32 {
			return fmt.Errorf("unrepresentable newc name, size or timestamp: %s", header.Name)
		}
	case "ar":
		if header.Kind != Regular || strings.Contains(header.Name, "/") {
			return errors.New("AR requires flat regular files; directories and links are unsupported")
		}

		if strings.HasPrefix(header.Name, "__.SYMDEF") || strings.HasPrefix(header.Name, "#1/") || strings.ContainsAny(header.Name, "\r\n") || strings.HasSuffix(header.Name, " ") {
			return fmt.Errorf("reserved or unrepresentable AR member name: %s", header.Name)
		}

		extended := int64(0)

		if len(header.Name) >= 16 || strings.Contains(header.Name, " ") {
			extended = int64(len(header.Name))
		}

		if header.Size > 9999999999-extended || seconds < 0 || seconds > 999999999999 || header.UID > 999999 || header.GID > 999999 {
			return fmt.Errorf("unrepresentable AR size, timestamp or ownership: %s", header.Name)
		}
	case "iso":
		if header.Size >= 0xfffff800 || header.Modified.UTC().Year() < 1900 || header.Modified.UTC().Year() > 2155 {
			return fmt.Errorf("unrepresentable ISO9660 level 2 size or timestamp: %s", header.Name)
		}

		depth := strings.Count(header.Name, "/")

		if header.Kind == Directory {
			depth++
		}

		if depth > 7 || len(header.Name) > 4095 || len(header.Link) > 4095 {
			return fmt.Errorf("ISO9660 supports at most seven directory levels and 4095-byte Rock Ridge paths: %s", header.Name)
		}

		for component := range strings.SplitSeq(header.Name, "/") {
			if len(component) > 255 {
				return fmt.Errorf("ISO9660 Rock Ridge name exceeds 255 bytes: %s", header.Name)
			}
		}

		for _, character := range header.Name {
			if character > 0xffff {
				return fmt.Errorf("ISO9660 Joliet names require Unicode BMP characters: %s", header.Name)
			}
		}
	case "Z":
		if header.Kind != Regular {
			return errors.New(".Z compresses one regular file")
		}
	}

	return nil
}
