#include "seekable.h"
#include <zstd.h>
/* Use the pinned Zstandard implementation, without another hashing dependency. */
#define XXH_INLINE_ALL
#include "../../.native/src/zstd/lib/common/xxhash.h"

void *pakr_hash_new(void) {
	XXH64_state_t *state = XXH64_createState();
	if (state) XXH64_reset(state, 0);
	return state;
}
void pakr_hash_update(void *state, const void *data, size_t size) { XXH64_update(state, data, size); }
uint32_t pakr_hash_digest(void *state) { return (uint32_t)XXH64_digest(state); }
void pakr_hash_free(void *state) { XXH64_freeState(state); }

int pakr_zstd_frame(const void *input, size_t size, void *output, size_t capacity) {
	size_t compressed = ZSTD_findFrameCompressedSize(input, size);
	if (ZSTD_isError(compressed) || compressed != size) return -1;
	size_t decoded = ZSTD_decompress(output, capacity, input, size);
	return !ZSTD_isError(decoded) && decoded == capacity ? 0 : -1;
}
