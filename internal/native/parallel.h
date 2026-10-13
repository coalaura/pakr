#ifndef PAKR_PARALLEL_H
#define PAKR_PARALLEL_H

#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
void *pakr_parallel_open(const char *path, int format, int threads, char *error, size_t capacity);
int64_t pakr_parallel_read(void *reader, void *output, size_t capacity, char *error, size_t error_capacity);
void pakr_parallel_close(void *reader);
#ifdef __cplusplus
}
#endif
#endif
