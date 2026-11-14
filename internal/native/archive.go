package native

/*
#include <archive.h>
#include <archive_entry.h>
#include <locale.h>
#include <stdlib.h>
#include "sevenzip.h"
#include "sevenzip_decode.h"
int pakr_archive_read_tail(struct archive *archive);
typedef __LA_MODE_T pakr_mode_t;
#ifdef _WIN32
#include <windows.h>
#endif

static int pakr_archive_locale(void) {
#ifdef _WIN32
	return setlocale(LC_CTYPE, ".UTF-8") != NULL;
#elif defined(__APPLE__)
	return setlocale(LC_CTYPE, "en_US.UTF-8") != NULL;
#else
	return setlocale(LC_CTYPE, "C.UTF-8") != NULL;
#endif
}

static int pakr_archive_open(struct archive *archive, const char *path, int writing) {
#ifdef _WIN32
	int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
	if (length == 0) return ARCHIVE_FATAL;
	wchar_t *wide = malloc(length * sizeof(wchar_t));
	if (!wide) return ARCHIVE_FATAL;
	MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide, length);
	int result = writing ? archive_write_open_filename_w(archive, wide) : archive_read_open_filename_w(archive, wide, 1048576);
	free(wide);
	return result;
#else
	return writing ? archive_write_open_filename(archive, path) : archive_read_open_filename(archive, path, 1048576);
#endif
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/cgo"
	"strconv"
	"time"
	"unsafe"
)

const (
	Regular = iota
	Directory
	Symlink
	Hardlink
)

type Header struct {
	Name     string
	Link     string
	Size     int64
	Mode     uint32
	Modified time.Time
	Kind     int
	Links    uint32
	UID      uint32
	GID      uint32
}

type Archive struct {
	archive  *C.struct_archive
	writing  bool
	filter   int
	format   string
	ctx      context.Context
	target   io.Writer
	handle   cgo.Handle
	err      error
	encoder  *C.pakr_7z_encoder
	decoder  *C.pakr_7z_decoder
	sevenzip *sevenzipReader
}

// libarchive converts entry names through the C locale. Initialize it before
// any compression workers start; never change it while the process is running.
var archiveLocaleAvailable = C.pakr_archive_locale() != 0

func (archive *Archive) WriteHeader(header Header) error {
	if archive.archive == nil {
		return errArchiveClosed
	}

	if archive.err != nil {
		return archive.err
	}

	if archive.format != "" {
		err := validateArchiveHeader(header, archive.format)
		if err != nil {
			archive.err = err

			return err
		}
	}

	entry := C.archive_entry_new()
	if entry == nil {
		return errors.New("allocate archive entry")
	}

	defer C.archive_entry_free(entry)

	name := C.CString(header.Name)
	defer C.free(unsafe.Pointer(name))

	// The C locale is UTF-8. Supplying its representation directly avoids
	// libarchive normalizing UTF-8 names while converting them to that locale.
	C.archive_entry_set_pathname(entry, name)
	C.archive_entry_set_size(entry, C.la_int64_t(header.Size))
	C.archive_entry_set_perm(entry, C.pakr_mode_t(header.Mode&0o777))
	C.archive_entry_set_mtime(entry, C.time_t(header.Modified.Unix()), C.long(header.Modified.Nanosecond()))
	C.archive_entry_set_nlink(entry, 1)

	if archive.format != "" {
		C.archive_entry_set_perm(entry, C.pakr_mode_t(header.Mode))
		C.archive_entry_set_uid(entry, C.la_int64_t(header.UID))
		C.archive_entry_set_gid(entry, C.la_int64_t(header.GID))
	}

	switch header.Kind {
	case Regular:
		C.archive_entry_set_filetype(entry, C.AE_IFREG)
	case Directory:
		C.archive_entry_set_filetype(entry, C.AE_IFDIR)
		C.archive_entry_set_size(entry, 0)
	case Symlink:
		link := C.CString(header.Link)
		defer C.free(unsafe.Pointer(link))

		C.archive_entry_set_filetype(entry, C.AE_IFLNK)
		C.archive_entry_set_symlink(entry, link)
		C.archive_entry_set_size(entry, 0)
	default:
		return errors.New("unsupported archive entry type")
	}

	return archive.check(C.archive_write_header(archive.archive, entry))
}

func (archive *Archive) Write(data []byte) (int, error) {
	if archive.archive == nil {
		return 0, errArchiveClosed
	}

	if archive.err != nil {
		return 0, archive.err
	}

	if len(data) == 0 {
		return 0, nil
	}

	count := C.archive_write_data(archive.archive, unsafe.Pointer(&data[0]), C.size_t(len(data)))
	if count < 0 {
		return 0, archive.failure()
	}

	if int(count) != len(data) {
		archive.err = io.ErrShortWrite

		return int(count), io.ErrShortWrite
	}

	return int(count), nil
}

func (archive *Archive) Next() (Header, error) {
	var entry *C.struct_archive_entry

	status := C.archive_read_next_header(archive.archive, &entry)
	if status == C.ARCHIVE_EOF {
		if C.archive_read_has_encrypted_entries(archive.archive) > 0 {
			return Header{}, errors.New("encrypted archive entries are not supported")
		}

		return Header{}, io.EOF
	}

	err := archive.check(status)
	if err != nil {
		return Header{}, err
	}

	if C.archive_entry_is_encrypted(entry) != 0 {
		return Header{}, errors.New("encrypted archive entries are not supported")
	}

	name := C.archive_entry_pathname_utf8(entry)
	if name == nil {
		return Header{}, errors.New("archive entry name is not valid UTF-8")
	}

	header := Header{
		Name:     C.GoString(name),
		Size:     int64(C.archive_entry_size(entry)),
		Mode:     uint32(C.archive_entry_perm(entry)),
		Modified: time.Unix(int64(C.archive_entry_mtime(entry)), int64(C.archive_entry_mtime_nsec(entry))),
		Links:    uint32(C.archive_entry_nlink(entry)),
	}

	if C.archive_entry_size_is_set(entry) == 0 {
		header.Size = -1
	} else if header.Size < 0 {
		return Header{}, errors.New("invalid negative archive entry size")
	}

	if C.archive_entry_mtime_is_set(entry) == 0 {
		header.Modified = time.Time{}
	}

	link := C.archive_entry_hardlink_utf8(entry)
	if link != nil {
		header.Kind = Hardlink
		header.Link = C.GoString(link)

		return header, nil
	}

	if C.archive_entry_hardlink(entry) != nil {
		return Header{}, errors.New("archive hardlink target is not valid UTF-8")
	}

	kind := C.archive_entry_filetype(entry)

	switch kind {
	case C.AE_IFREG:
		header.Kind = Regular
	case C.AE_IFDIR:
		header.Kind = Directory
	case C.AE_IFLNK:
		link = C.archive_entry_symlink_utf8(entry)
		if link == nil {
			return Header{}, errors.New("archive link target is not valid UTF-8")
		}

		header.Kind = Symlink
		header.Link = C.GoString(link)
	default:
		return Header{}, fmt.Errorf("unsupported archive entry type: %s", header.Name)
	}

	return header, nil
}

func (archive *Archive) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	count := C.archive_read_data(archive.archive, unsafe.Pointer(&data[0]), C.size_t(len(data)))
	if count < 0 {
		return 0, archive.failure()
	}

	if count == 0 {
		return 0, io.EOF
	}

	return int(count), nil
}

// ReadTail validates CPIO padding and forces outer filters through their trailers.
// Each call is bounded so the caller can check cancellation between blocks.
func (archive *Archive) ReadTail() error {
	status := C.pakr_archive_read_tail(archive.archive)
	if status == C.ARCHIVE_EOF {
		return io.EOF
	}

	return archive.check(status)
}

func (archive *Archive) Close() error {
	if archive.archive == nil {
		return nil
	}

	var (
		err    error
		status C.int
	)

	if archive.writing {
		if archive.ctx != nil {
			archive.err = errors.Join(archive.err, context.Cause(archive.ctx))
		}

		if archive.err != nil {
			C.archive_write_fail(archive.archive)

			err = archive.err
		} else {
			err = archive.check(C.archive_write_close(archive.archive))
		}

		status = C.archive_write_free(archive.archive)
	} else {
		err = archive.check(C.archive_read_close(archive.archive))
		status = C.archive_read_free(archive.archive)
	}

	archive.archive = nil

	if archive.encoder != nil {
		C.pakr_7z_encoder_free(archive.encoder)
		archive.encoder = nil
	}

	if archive.decoder != nil {
		C.pakr_7z_decoder_free(archive.decoder)
		archive.decoder = nil
	}

	if archive.sevenzip != nil {
		err = errors.Join(err, archive.sevenzip.file.Close())
		archive.sevenzip = nil
	}

	if archive.handle != 0 {
		archive.handle.Delete()
		archive.handle = 0
	}

	if status != C.ARCHIVE_OK {
		err = errors.Join(err, fmt.Errorf("libarchive finalization failed: status %d", status))
	}

	return err
}

func (archive *Archive) check(status C.int) error {
	if status != C.ARCHIVE_OK {
		return archive.failure()
	}

	return nil
}

func (archive *Archive) failure() error {
	message := C.archive_error_string(archive.archive)
	err := errors.New("libarchive operation failed")

	if message == nil {
		err = errors.Join(archive.err, err)
	} else {
		err = errors.Join(archive.err, errors.New(C.GoString(message)))
	}

	if archive.writing {
		archive.err = err
	}

	return err
}

func (archive *Archive) cpioFilters(format string) C.int {
	filters := []int{int(C.ARCHIVE_FILTER_GZIP), int(C.ARCHIVE_FILTER_BZIP2), int(C.ARCHIVE_FILTER_XZ), int(C.ARCHIVE_FILTER_ZSTD)}

	if format == "rpm" {
		filters = append(filters, int(C.ARCHIVE_FILTER_RPM), int(C.ARCHIVE_FILTER_LZMA))
	}

	for _, filter := range filters {
		status := C.archive_read_support_filter_by_code(archive.archive, C.int(filter))
		if status != C.ARCHIVE_OK {
			return status
		}
	}

	return C.ARCHIVE_OK
}

func OpenArchive(path string, writing bool, level, threads int) (*Archive, error) {
	return openArchive(context.Background(), path, "7z", writing, level, threads, 1<<30)
}

// OpenArchiveReader enables only the selected container and its applicable filters.
func OpenArchiveReader(path, format string) (*Archive, error) {
	return openArchive(context.Background(), path, format, false, 3, 1, 1<<30)
}

func openArchive(ctx context.Context, path, format string, writing bool, level, threads int, memory uint64) (*Archive, error) {
	if level < 1 || level > 9 || threads < 1 {
		return nil, errors.New("invalid archive level or thread count")
	}

	if !archiveLocaleAvailable {
		return nil, errors.New("a UTF-8 C locale is required for libarchive archives")
	}

	archive := &Archive{writing: writing, ctx: ctx}

	if writing {
		archive.archive = C.archive_write_new()
	} else {
		archive.archive = C.archive_read_new()
	}

	if archive.archive == nil {
		return nil, errors.New("allocate libarchive instance")
	}

	var status C.int

	if writing {
		status = C.archive_write_set_format_7zip(archive.archive)
		if status == C.ARCHIVE_OK {
			options := C.CString("7zip:compression=lzma2,7zip:compression-level=" + strconv.Itoa(NativeLevel(SevenZip, level)) + ",7zip:threads=" + strconv.Itoa(threads))

			status = C.archive_write_set_options(archive.archive, options)

			C.free(unsafe.Pointer(options))
		}
	} else {
		switch format {
		case "7z":
			status = archive.enable7zipDecoder(path, threads, memory)
		case "zip":
			status = C.archive_read_support_format_zip_seekable(archive.archive)
		case "rar4":
			status = C.archive_read_support_format_rar(archive.archive)
			if status == C.ARCHIVE_OK {
				// RAR4 names without the Unicode flag otherwise use the Windows
				// OEM code page, even with a UTF-8 locale.
				options := C.CString("rar:hdrcharset=UTF-8")
				status = C.archive_read_set_options(archive.archive, options)

				C.free(unsafe.Pointer(options))
			}
		case "rar5":
			status = C.archive_read_support_format_rar5(archive.archive)
		case "cab":
			status = C.archive_read_support_format_cab(archive.archive)
		case "iso":
			status = C.archive_read_support_format_iso9660(archive.archive)
		case "ar", "deb":
			status = C.archive_read_support_format_ar(archive.archive)
		case "lha":
			status = C.archive_read_support_format_lha(archive.archive)
		case "cpio", "rpm", "cpio.gz", "cpio.bz2", "cpio.xz", "cpio.zst":
			status = C.archive_read_support_format_cpio(archive.archive)
			if status == C.ARCHIVE_OK {
				// CPIO has no charset marker; use UTF-8 rather than the Windows OEM default.
				options := C.CString("cpio:hdrcharset=UTF-8")
				status = C.archive_read_set_options(archive.archive, options)

				C.free(unsafe.Pointer(options))
			}

			if status == C.ARCHIVE_OK {
				status = archive.cpioFilters(format)
			}

			switch format {
			case "rpm":
				archive.filter = int(C.ARCHIVE_FILTER_RPM)
			case "cpio.gz":
				archive.filter = int(C.ARCHIVE_FILTER_GZIP)
			case "cpio.bz2":
				archive.filter = int(C.ARCHIVE_FILTER_BZIP2)
			case "cpio.xz":
				archive.filter = int(C.ARCHIVE_FILTER_XZ)
			case "cpio.zst":
				archive.filter = int(C.ARCHIVE_FILTER_ZSTD)
			}
		case "Z", "lzma":
			status = C.archive_read_support_format_raw(archive.archive)
			if status == C.ARCHIVE_OK {
				// The empty reader accepts correctly decoded empty streams.
				status = C.archive_read_support_format_empty(archive.archive)
			}

			archive.filter = int(C.ARCHIVE_FILTER_COMPRESS)

			if format == "lzma" {
				archive.filter = int(C.ARCHIVE_FILTER_LZMA)
			}

			if status == C.ARCHIVE_OK {
				// Force the selected decoder: raw bidding must never return encoded bytes.
				status = C.archive_read_append_filter(archive.archive, C.int(archive.filter))
			}
		default:
			archive.Close()

			return nil, fmt.Errorf("unsupported native archive format: %q", format)
		}
	}

	err := archive.check(status)
	if err == nil && writing {
		archive.handle = cgo.NewHandle(archive)
		archive.encoder = C.pakr_7z_encoder_new(C.int(level), C.int(threads), C.uint64_t(memory), C.uintptr_t(archive.handle))

		if archive.encoder == nil {
			err = errors.New("allocate 7z encoder")
		} else {
			message := C.GoString(C.pakr_7z_encoder_error(unsafe.Pointer(archive.encoder)))
			if message != "" {
				err = errors.New(message)
			} else {
				C.pakr_archive_write_7zip_encoder(archive.archive, unsafe.Pointer(archive.encoder), (*[0]byte)(C.pakr_7z_encoder_start), (*[0]byte)(C.pakr_7z_encoder_step), (*[0]byte)(C.pakr_7z_encoder_error))
			}
		}
	}

	if err == nil {
		name := C.CString(path)
		write := C.int(0)

		if writing {
			write = 1
		}

		err = archive.check(C.pakr_archive_open(archive.archive, name, write))

		C.free(unsafe.Pointer(name))
	}

	if err == nil && archive.filter != 0 {
		found := false
		count := int(C.archive_filter_count(archive.archive))

		for index := range count {
			if int(C.archive_filter_code(archive.archive, C.int(index))) == archive.filter {
				found = true
			}
		}

		if !found {
			err = fmt.Errorf("input is not a %s stream", format)
		}
	}

	if err != nil {
		if writing {
			return nil, archive.Abort(err)
		}

		archive.Close()

		return nil, err
	}

	return archive, nil
}
