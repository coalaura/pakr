#ifndef PAKR_LZIP_H
#define PAKR_LZIP_H

#include <stddef.h>
#include <stdint.h>

typedef struct pakr_lzip pakr_lzip;
pakr_lzip *pakr_lzip_new(int encode, int level, uint64_t memory, int64_t size);
const char *pakr_lzip_error(pakr_lzip *codec);
int pakr_lzip_step(pakr_lzip *codec, const void *input, size_t *input_size, void *output, size_t *output_size, int finish);
void pakr_lzip_free(pakr_lzip *codec);

#endif
