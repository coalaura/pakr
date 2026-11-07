#ifndef PAKR_CODEC_H
#define PAKR_CODEC_H

#include <stddef.h>
#include <stdint.h>

typedef struct pakr_codec pakr_codec;
pakr_codec *pakr_codec_new(int format, int encode, int level, int threads, int extreme);
pakr_codec *pakr_codec_new_lzip(int encode, int level, uint64_t memory, int64_t size);
pakr_codec *pakr_codec_new_lzma(int level, uint64_t memory);
const char *pakr_codec_error(pakr_codec *codec);
size_t pakr_codec_bound(pakr_codec *codec, size_t input_size);
int pakr_codec_parameter(pakr_codec *codec, int parameter);
int pakr_codec_step(pakr_codec *codec, const void *input, size_t *input_size, void *output, size_t *output_size, int finish);
void pakr_codec_free(pakr_codec *codec);

#endif
