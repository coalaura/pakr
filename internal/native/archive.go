package native

/*
#include <archive.h>
#include <archive_entry.h>
#include <locale.h>
#include <stdlib.h>
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
	"errors"
	"fmt"
	"io"
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
}

type Archive struct {
	archive *C.struct_archive
	writing bool
}

// libarchive converts entry names through the C locale. Initialize it before
// any compression workers start; never change it while the process is running.
var archiveLocaleAvailable = C.pakr_archive_locale() != 0

func (archive *Archive) WriteHeader(header Header) error {
	entry := C.archive_entry_new()
	if entry == nil {
		return errors.New("allocate archive entry")
	}

	defer C.archive_entry_free(entry)

	name := C.CString(header.Name)
	defer C.free(unsafe.Pointer(name))

	C.archive_entry_set_pathname_utf8(entry, name)
	C.archive_entry_set_size(entry, C.la_int64_t(header.Size))
	C.archive_entry_set_perm(entry, C.pakr_mode_t(header.Mode&0o777))
	C.archive_entry_set_mtime(entry, C.time_t(header.Modified.Unix()), C.long(header.Modified.Nanosecond()))

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
		C.archive_entry_set_symlink_utf8(entry, link)
		C.archive_entry_set_size(entry, 0)
	default:
		return errors.New("unsupported 7z entry type")
	}

	return archive.check(C.archive_write_header(archive.archive, entry))
}

func (archive *Archive) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	count := C.archive_write_data(archive.archive, unsafe.Pointer(&data[0]), C.size_t(len(data)))
	if count < 0 {
		return 0, archive.failure()
	}

	if int(count) != len(data) {
		return int(count), io.ErrShortWrite
	}

	return int(count), nil
}

func (archive *Archive) Next() (Header, error) {
	var entry *C.struct_archive_entry

	status := C.archive_read_next_header(archive.archive, &entry)
	if status == C.ARCHIVE_EOF {
		return Header{}, io.EOF
	}

	err := archive.check(status)
	if err != nil {
		return Header{}, err
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
	}

	kind := C.archive_entry_filetype(entry)

	switch kind {
	case C.AE_IFREG:
		header.Kind = Regular
	case C.AE_IFDIR:
		header.Kind = Directory
	case C.AE_IFLNK:
		header.Kind = Symlink
		header.Link = C.GoString(C.archive_entry_symlink_utf8(entry))
	default:
		return Header{}, fmt.Errorf("unsupported archive entry type: %s", header.Name)
	}

	link := C.archive_entry_hardlink_utf8(entry)
	if link != nil {
		header.Kind = Hardlink
		header.Link = C.GoString(link)
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

func (archive *Archive) Close() error {
	if archive.archive == nil {
		return nil
	}

	var err error

	if archive.writing {
		err = archive.check(C.archive_write_close(archive.archive))
		C.archive_write_free(archive.archive)
	} else {
		err = archive.check(C.archive_read_close(archive.archive))
		C.archive_read_free(archive.archive)
	}

	archive.archive = nil

	return err
}

func (archive *Archive) check(status C.int) error {
	if status < C.ARCHIVE_OK {
		return archive.failure()
	}

	return nil
}

func (archive *Archive) failure() error {
	message := C.archive_error_string(archive.archive)
	if message == nil {
		return errors.New("libarchive operation failed")
	}

	return errors.New(C.GoString(message))
}

func OpenArchive(path string, writing bool, level, threads int) (*Archive, error) {
	if !archiveLocaleAvailable {
		return nil, errors.New("a UTF-8 C locale is required for 7z archives")
	}

	archive := &Archive{writing: writing}

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
			options := C.CString("7zip:compression=lzma2,7zip:compression-level=" + strconv.Itoa(level) + ",7zip:threads=" + strconv.Itoa(threads))

			status = C.archive_write_set_options(archive.archive, options)

			C.free(unsafe.Pointer(options))
		}
	} else {
		status = C.archive_read_support_format_7zip(archive.archive)
	}

	err := archive.check(status)
	if err == nil {
		name := C.CString(path)
		write := C.int(0)

		if writing {
			write = 1
		}

		err = archive.check(C.pakr_archive_open(archive.archive, name, write))

		C.free(unsafe.Pointer(name))
	}

	if err != nil {
		archive.Close()

		return nil, err
	}

	return archive, nil
}
