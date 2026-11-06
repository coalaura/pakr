#ifndef PAKR_KANZI_H
#define PAKR_KANZI_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
typedef struct pakr_knz pakr_knz;
typedef struct {
	int block_size;
	int64_t original_size;
	char entropy[16];
	char transform[128];
} pakr_knz_info;
int pakr_knz_probe(const void *data, size_t size, pakr_knz_info *info, char *error);
pakr_knz *pakr_knz_open(uintptr_t handle, int encode, int jobs, const pakr_knz_info *info, char *error);
int pakr_knz_write(pakr_knz *stream, const void *data, size_t size, char *error);
int64_t pakr_knz_read(pakr_knz *stream, void *data, size_t size, char *error);
uint64_t pakr_knz_consumed(pakr_knz *stream);
int pakr_knz_close(pakr_knz *stream, char *error);
#ifdef __cplusplus
}
#endif
#endif
