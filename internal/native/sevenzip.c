#include "sevenzip.h"
#include <fast-lzma2.h>
#include <lzma.h>
#include <stdio.h>
#include <stdlib.h>

#define MIB (UINT64_C(1) << 20)

struct pakr_7z_encoder {
	FL2_CStream *fast;
	lzma_stream legacy;
	uintptr_t handle;
	unsigned level;
	unsigned threads;
	char error[192];
};

/* Retain the original 7z dictionary progression with increasing parser effort.
 * FL2 cannot represent level 1's 256 KiB dictionary, so that level stays liblzma. */
static const unsigned presets[] = {0, 0, 3, 3, 6, 10, 4, 5, 6, 7};
static const uint32_t dictionaries[] = {0, 256 << 10, 1 << 20, 4 << 20, 4 << 20, 8 << 20, 8 << 20, 16 << 20, 32 << 20, 64 << 20};

extern int pakrArchiveCancelled(uintptr_t handle);

static int fail(pakr_7z_encoder *encoder, const char *message) {
	snprintf(encoder->error, sizeof(encoder->error), "%s", message);
	return -1;
}

static int check(pakr_7z_encoder *encoder, size_t result) {
	if (FL2_isError(result)) return fail(encoder, FL2_getErrorName(result));
	return 0;
}

static int cancelled(pakr_7z_encoder *encoder) {
	if (!pakrArchiveCancelled(encoder->handle)) return 0;
	if (encoder->fast) FL2_cancelCStream(encoder->fast);
	return fail(encoder, "7z compression cancelled");
}

pakr_7z_encoder *pakr_7z_encoder_new(int level, int threads, uint64_t memory, uintptr_t handle) {
	pakr_7z_encoder *encoder = calloc(1, sizeof(*encoder));
	if (!encoder) return NULL;
	encoder->handle = handle;
	if (level < 1 || level > 9 || threads < 1 || threads > FL2_MAXTHREADS) {
		fail(encoder, "7z worker count must be between 1 and 200 and level between 1 and 9");
		return encoder;
	}
	encoder->level = level;
	encoder->threads = threads;
	uint64_t required;
	/* Admission includes allocator/Go overhead and control/worker stacks. It never
	 * silently reduces effort or the requested worker count to fit the budget. */
	uint64_t reserve = (16 + UINT64_C(8) * (level == 1 ? 1 : threads)) * MIB;
	if (level == 1) {
		lzma_options_lzma options;
		lzma_lzma_preset(&options, 0);
		lzma_filter filters[] = {{LZMA_FILTER_LZMA2, &options}, {LZMA_VLI_UNKNOWN, NULL}};
		required = lzma_raw_encoder_memusage(filters);
	} else {
		FL2_compressionParameters parameters;
		if (check(encoder, FL2_getLevelParameters(presets[level], level >= 6, &parameters))) return encoder;
		parameters.dictionarySize = dictionaries[level];
		required = FL2_estimateCStreamSize_byParams(&parameters, threads, 0);
	}
	if (required == UINT64_MAX || memory < reserve || required > memory - reserve) {
		fail(encoder, "7z encoder exceeds memory budget; lower --threads or --level, or raise --memory");
	}
	return encoder;
}

void pakr_7z_encoder_free(pakr_7z_encoder *encoder) {
	if (!encoder) return;
	if (encoder->fast) FL2_freeCStream(encoder->fast);
	lzma_end(&encoder->legacy);
	free(encoder);
}

const char *pakr_7z_encoder_error(void *opaque) {
	pakr_7z_encoder *encoder = opaque;
	return encoder->error;
}

int pakr_7z_encoder_start(void *opaque, unsigned char *property) {
	pakr_7z_encoder *encoder = opaque;
	if (encoder->error[0] || cancelled(encoder)) return -1;
	unsigned level = encoder->level;
	if (level == 1) {
		lzma_end(&encoder->legacy);
		lzma_options_lzma options;
		lzma_lzma_preset(&options, 0);
		lzma_filter filters[] = {{LZMA_FILTER_LZMA2, &options}, {LZMA_VLI_UNKNOWN, NULL}};
		if (lzma_properties_encode(filters, property) != LZMA_OK || lzma_raw_encoder(&encoder->legacy, filters) != LZMA_OK) return fail(encoder, "cannot initialize 7z LZMA2 encoder");
		return 0;
	}
	if (!encoder->fast) {
		encoder->fast = FL2_createCStreamMt(encoder->threads, 0);
		if (!encoder->fast) return fail(encoder, "cannot allocate 7z Fast-LZMA2 encoder");
		if (FL2_getCCtxThreadCount(encoder->fast) != encoder->threads) return fail(encoder, "Fast-LZMA2 did not admit the requested workers");
		if (check(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_highCompression, level >= 6))) return -1;
		if (check(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_compressionLevel, presets[level])) || check(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_dictionarySize, dictionaries[level])) || check(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_doXXHash, 0)) || check(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_omitProperties, 1))) return -1;
		/* Explicit resets bound dependency chains without staging a solid block. */
		if (check(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_resetInterval, 4)) || check(encoder, FL2_setCStreamTimeout(encoder->fast, 20))) return -1;
	}
	if (check(encoder, FL2_initCStream(encoder->fast, 0))) return -1;
	*property = FL2_getCCtxDictProp(encoder->fast);
	return 0;
}

int pakr_7z_encoder_step(void *opaque, const void *input, size_t *input_size, void *output, size_t *output_size, int finish) {
	pakr_7z_encoder *encoder = opaque;
	if (cancelled(encoder)) return -1;
	if (!encoder->fast) {
		encoder->legacy.next_in = input;
		encoder->legacy.avail_in = *input_size;
		encoder->legacy.next_out = output;
		encoder->legacy.avail_out = *output_size;
		lzma_ret result = lzma_code(&encoder->legacy, finish ? LZMA_FINISH : LZMA_RUN);
		*input_size -= encoder->legacy.avail_in;
		*output_size -= encoder->legacy.avail_out;
		if (result != LZMA_OK && result != LZMA_STREAM_END) return fail(encoder, "7z LZMA2 encoding failed");
		return result == LZMA_STREAM_END;
	}
	FL2_inBuffer source = {input, *input_size, 0};
	FL2_outBuffer destination = {output, *output_size, 0};
	size_t result = 0;
	/* Timed waits let cancellation interrupt worker computation, including close.
	 * Never re-enter compressStream after endStream has begun. */
	if (!finish || source.pos < source.size) {
		do {
			result = FL2_compressStream(encoder->fast, &destination, &source);
			if (cancelled(encoder)) return -1;
		} while (FL2_isTimedOut(result));
		if (check(encoder, result)) return -1;
	}
	int done = 0;
	if (finish && source.pos == source.size) {
		do {
			result = FL2_endStream(encoder->fast, &destination);
			if (cancelled(encoder)) return -1;
		} while (FL2_isTimedOut(result));
		if (check(encoder, result)) return -1;
		done = result == 0;
	}
	*input_size = source.pos;
	*output_size = destination.pos;
	return done;
}
