#ifndef PAKR_WIM_H
#define PAKR_WIM_H
#include <stddef.h>
#include <stdint.h>

typedef struct WIMStruct WIMStruct;
typedef struct {
	char *source;
	char *target;
} pakr_wim_source;

typedef struct {
	const char *path;
	const char *name;
	uint64_t size;
	uint64_t group;
	int64_t modified;
	uint32_t nanos;
	uint32_t mode;
	uint32_t attributes;
	uint32_t reparse;
	unsigned char hash[20];
} pakr_wim_entry;

int pakr_wim_init(void);
int pakr_wim_create(int codec, unsigned level, WIMStruct **result, uintptr_t handle);
int pakr_wim_open(const char *path, WIMStruct **result, uintptr_t handle);
int pakr_wim_capture(WIMStruct *wim, pakr_wim_source *sources, size_t count);
int pakr_wim_write(WIMStruct *wim, const char *path, int solid, unsigned threads);
int pakr_wim_tree(WIMStruct *wim, int image, uint64_t metadata_limit, uintptr_t handle);
int pakr_wim_read(WIMStruct *wim, const unsigned char *hashes, size_t count, uintptr_t handle);
unsigned pakr_wim_images(WIMStruct *wim);
void pakr_wim_free(WIMStruct *wim);
uint64_t pakr_wim_memory(int codec, unsigned chunk, unsigned level);
char *pakr_wim_error(int status);
#endif
