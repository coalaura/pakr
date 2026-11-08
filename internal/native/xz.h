#ifndef PAKR_XZ_H
#define PAKR_XZ_H

#include <stddef.h>
#include <stdint.h>

typedef struct pakr_xz pakr_xz;
pakr_xz *pakr_xz_new(int level, int threads, int extreme, uint64_t memory);
const char *pakr_xz_error(pakr_xz *encoder);
int pakr_xz_parameter(pakr_xz *encoder, int parameter);
uint64_t pakr_xz_memory(pakr_xz *encoder);
/* Step status: -1 error, 0 normal, 1 done, 2 timed async wait, 3 internal progress. */
int pakr_xz_step(pakr_xz *encoder, const void *input, size_t *input_size, void *output, size_t *output_size, int finish);
void pakr_xz_free(pakr_xz *encoder);

#endif
