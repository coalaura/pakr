#ifndef PAKR_GZIP_H
#define PAKR_GZIP_H
#include <stddef.h>

typedef struct pakr_gzip pakr_gzip;
pakr_gzip *pakr_gzip_new(int level);
size_t pakr_gzip_compress(pakr_gzip *codec, const void *input, size_t size, void *output, size_t capacity);
void pakr_gzip_free(pakr_gzip *codec);
#endif
