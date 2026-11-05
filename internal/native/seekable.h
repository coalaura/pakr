#ifndef PAKR_SEEKABLE_H
#define PAKR_SEEKABLE_H
#include <stddef.h>
#include <stdint.h>
void *pakr_hash_new(void);
void pakr_hash_update(void *state, const void *data, size_t size);
uint32_t pakr_hash_digest(void *state);
void pakr_hash_free(void *state);
int pakr_zstd_frame(const void *input, size_t size, void *output, size_t capacity);
#endif
