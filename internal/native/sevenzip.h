#ifndef PAKR_SEVENZIP_H
#define PAKR_SEVENZIP_H

#include <stddef.h>
#include <stdint.h>
#include <archive.h>

typedef struct pakr_7z_encoder pakr_7z_encoder;

pakr_7z_encoder *pakr_7z_encoder_new(int level, int threads, uint64_t memory, uintptr_t handle);
void pakr_7z_encoder_free(pakr_7z_encoder *encoder);
const char *pakr_7z_encoder_error(void *encoder);
int pakr_7z_encoder_start(void *encoder, unsigned char *property);
int pakr_7z_encoder_step(void *encoder, const void *input, size_t *input_size, void *output, size_t *output_size, int finish);
void pakr_archive_write_7zip_encoder(struct archive *archive, void *encoder, int (*start)(void *, unsigned char *), int (*step)(void *, const void *, size_t *, void *, size_t *, int), const char *(*error)(void *));

#endif
