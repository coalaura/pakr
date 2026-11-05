#include "levels.h"
#include <zstd.h>
#include <lz4hc.h>

int pakr_level(int backend, int level) {
	/* Fast endpoints are practical accelerations, not store modes. Keep level 3 balanced. */
	static const int levels[][9] = {
		{0},
		{1, 2, 3, 4, 5, 6, 7, 8, 9}, /* zlib-ng gzip */
		{-3, 1, 3, 6, 9, 12, 16, 19, 22},
		{1, 2, 3, 4, 5, 6, 7, 8, 9},
		{0, 1, 3, 4, 5, 6, 7, 8, 9},
		{-4, -1, 0, 3, 5, 7, 9, 10, 12},
		{0, 1, 3, 4, 5, 6, 8, 10, 11},
		{1, 2, 3, 4, 5, 6, 7, 8, 9}, /* streaming ZIP */
		{1, 2, 3, 4, 5, 7, 9, 10, 12}, /* buffered ZIP */
		{0, 1, 3, 4, 5, 6, 7, 8, 9}, /* libarchive LZMA2 */
		{0, 1, 2, 3, -1, -1, -1, -1, -1},
	};
	if (backend < 1 || backend > 10 || level < 1 || level > 9) return -999;
	if (backend == 2 && level == 9) return ZSTD_maxCLevel();
	if (backend == 5 && level == 9) return LZ4HC_CLEVEL_MAX;
	return levels[backend][level - 1];
}
