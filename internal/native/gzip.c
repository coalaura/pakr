#include "gzip.h"
#include <igzip_lib.h>
#include <stdlib.h>
#include <zlib.h>

struct pakr_gzip {
	struct isal_zstream isal;
	z_stream zlib;
	void *scratch;
	int level;
};

pakr_gzip *pakr_gzip_new(int level) {
	pakr_gzip *codec = calloc(1, sizeof(*codec));
	if (!codec) return NULL;
	codec->level = level;
	if (level <= 3) {
		codec->scratch = malloc(ISAL_DEF_LVL2_DEFAULT);
		if (!codec->scratch) {
			free(codec);
			return NULL;
		}
	} else if (deflateInit2(&codec->zlib, level, Z_DEFLATED, 31, 8, Z_DEFAULT_STRATEGY) != Z_OK) {
		free(codec);
		return NULL;
	}
	return codec;
}

size_t pakr_gzip_compress(pakr_gzip *codec, const void *input, size_t size, void *output, size_t capacity) {
	if (codec->level <= 3) {
		isal_deflate_stateless_init(&codec->isal);
		codec->isal.level = codec->level - 1;
		codec->isal.level_buf = codec->scratch;
		codec->isal.level_buf_size = ISAL_DEF_LVL2_DEFAULT;
		codec->isal.gzip_flag = IGZIP_GZIP;
		codec->isal.end_of_stream = 1;
		codec->isal.next_in = (void *)input;
		codec->isal.avail_in = size;
		codec->isal.next_out = output;
		codec->isal.avail_out = capacity;
		int status = isal_deflate_stateless(&codec->isal);
		codec->isal.next_in = NULL;
		codec->isal.next_out = NULL;
		return status == COMP_OK ? codec->isal.total_out : 0;
	}
	if (deflateReset(&codec->zlib) != Z_OK) return 0;
	codec->zlib.next_in = (void *)input;
	codec->zlib.avail_in = size;
	codec->zlib.next_out = output;
	codec->zlib.avail_out = capacity;
	int status = deflate(&codec->zlib, Z_FINISH);
	codec->zlib.next_in = NULL;
	codec->zlib.next_out = NULL;
	return status == Z_STREAM_END ? codec->zlib.total_out : 0;
}

void pakr_gzip_free(pakr_gzip *codec) {
	if (!codec) return;
	if (codec->level > 3) deflateEnd(&codec->zlib);
	free(codec->scratch);
	free(codec);
}
