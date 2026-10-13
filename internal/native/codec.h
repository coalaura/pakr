#ifndef PAKR_CODEC_H
#define PAKR_CODEC_H

#include <stddef.h>
#include <stdint.h>

typedef struct pakr_codec pakr_codec;
pakr_codec *pakr_codec_new(int format, int encode, int level, int threads);
const char *pakr_codec_error(pakr_codec *codec);
int pakr_codec_step(pakr_codec *codec, const void *input, size_t *input_size, void *output, size_t *output_size, int finish);
void pakr_codec_free(pakr_codec *codec);
size_t pakr_deflate_bound(size_t size);
size_t pakr_deflate(const void *input, size_t size, void *output, size_t capacity, int level);
int pakr_inflate(const void *input, size_t size, void *output, size_t capacity);

#endif
