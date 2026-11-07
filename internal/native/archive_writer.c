#include <archive.h>
#include <stdint.h>

extern la_ssize_t pakrArchiveWrite(uintptr_t handle, void *data, size_t size);

static la_ssize_t write_archive(struct archive *archive, void *client, const void *data, size_t size) {
	(void)archive;
	return pakrArchiveWrite((uintptr_t)client, (void *)data, size);
}

int pakr_archive_open_writer(struct archive *archive, uintptr_t handle) {
	return archive_write_open(archive, (void *)handle, NULL, write_archive, NULL);
}
