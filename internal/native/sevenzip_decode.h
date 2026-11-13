#ifndef PAKR_SEVENZIP_DECODE_H
#define PAKR_SEVENZIP_DECODE_H
#include <archive.h>
#include <stddef.h>
#include <stdint.h>

typedef struct pakr_7z_decoder pakr_7z_decoder;
pakr_7z_decoder *pakr_7z_decoder_new(uintptr_t handle);
void pakr_7z_decoder_free(pakr_7z_decoder *decoder);
int pakr_7z_decoder_start(void *opaque, int64_t offset, uint64_t packed, uint64_t unpacked, unsigned char property);
int pakr_7z_decoder_step(void *opaque, const void *input, size_t *input_size, void *output, size_t *output_size);
void pakr_7z_decoder_stop(void *opaque);
const char *pakr_7z_decoder_error(void *opaque);
int pakr_archive_read_support_7zip_decoder(struct archive *, void *, int (*)(void *, int64_t, uint64_t, uint64_t, unsigned char), int (*)(void *, const void *, size_t *, void *, size_t *), void (*)(void *), const char *(*)(void *));
#endif
