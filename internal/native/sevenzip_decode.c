#include "sevenzip_decode.h"
#include <fast-lzma2.h>
#include <stdio.h>
#include <stdlib.h>

struct pakr_7z_decoder {
	FL2_DStream *stream;
	uintptr_t handle;
	char error[192];
};

extern int pakrArchiveCancelled(uintptr_t handle);
extern unsigned pakr7zWorkers(uintptr_t handle, int64_t offset, uint64_t packed, uint64_t unpacked, unsigned char property, uint64_t *limit);

pakr_7z_decoder *pakr_7z_decoder_new(uintptr_t handle) {
	pakr_7z_decoder *decoder = calloc(1, sizeof(*decoder));
	if (decoder) decoder->handle = handle;
	return decoder;
}

void pakr_7z_decoder_stop(void *opaque) {
	pakr_7z_decoder *decoder = opaque;
	if (decoder->stream) FL2_freeDStream(decoder->stream);
	decoder->stream = NULL;
}

void pakr_7z_decoder_free(pakr_7z_decoder *decoder) {
	if (!decoder) return;
	pakr_7z_decoder_stop(decoder);
	free(decoder);
}

const char *pakr_7z_decoder_error(void *opaque) {
	pakr_7z_decoder *decoder = opaque;
	return decoder->error;
}

int pakr_7z_decoder_start(void *opaque, int64_t offset, uint64_t packed, uint64_t unpacked, unsigned char property) {
	pakr_7z_decoder *decoder = opaque;
	pakr_7z_decoder_stop(decoder);
	uint64_t limit = 0;
	unsigned workers = pakr7zWorkers(decoder->handle, offset, packed, unpacked, property, &limit);
	if (pakrArchiveCancelled(decoder->handle)) {
		snprintf(decoder->error, sizeof(decoder->error), "7z decompression cancelled");
		return -1;
	}
	if (workers < 2) return 0;
	/* Fresh contexts avoid retaining a previous folder's segment buffers. */
	decoder->stream = FL2_createDStreamMt(workers);
	if (!decoder->stream) return 0;
	FL2_setDStreamMemoryLimitMt(decoder->stream, (size_t)limit);
	size_t result = FL2_initDStream_withProp(decoder->stream, property);
	if (FL2_isError(result)) {
		pakr_7z_decoder_stop(decoder);
		return 0;
	}
	return 1;
}

int pakr_7z_decoder_step(void *opaque, const void *input, size_t *input_size, void *output, size_t *output_size) {
	pakr_7z_decoder *decoder = opaque;
	if (pakrArchiveCancelled(decoder->handle)) {
		snprintf(decoder->error, sizeof(decoder->error), "7z decompression cancelled");
		return -1;
	}
	FL2_inBuffer source = {input, *input_size, 0};
	FL2_outBuffer destination = {output, *output_size, 0};
	/* A batch is bounded by the admitted segment sizes and input/output cap.
	 * Avoid scheduling an extra asynchronous job for every 64 KiB archive read. */
	size_t result = FL2_decompressStream(decoder->stream, &destination, &source);
	*input_size = source.pos;
	*output_size = destination.pos;
	if (FL2_isError(result)) {
		snprintf(decoder->error, sizeof(decoder->error), "%s", FL2_getErrorName(result));
		return -1;
	}
	return result == 0;
}
