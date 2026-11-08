#include "xz.h"
#include "levels.h"
#include <fast-lzma2.h>
#include <lzma.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MIB (UINT64_C(1) << 20)

struct pakr_xz {
	FL2_CStream *fast;
	lzma_stream legacy;
	uint64_t memory;
	lzma_vli compressed;
	lzma_vli uncompressed;
	uint64_t crc;
	uint32_t dictionary;
	uint32_t block_header_size;
	unsigned workers;
	unsigned preset;
	int initialized;
	int phase;
	size_t position;
	size_t length;
	/* One block keeps Index storage constant even for arbitrarily long input. */
	uint8_t framing[128];
	char error[192];
};

/* Preserve pakr's dictionary progression, independently of FL2's presets.
 * Level 1 remains liblzma: FL2's minimum dictionary is 1 MiB, not 256 KiB. */
static const unsigned presets[] = {0, 0, 1, 3, 4, 5, 6, 7, 8, 9};
static const uint32_t dictionaries[] = {0, 256 << 10, 1 << 20, 4 << 20, 4 << 20, 8 << 20, 8 << 20, 16 << 20, 32 << 20, 64 << 20};

static int fail(pakr_xz *encoder, const char *message) {
	snprintf(encoder->error, sizeof(encoder->error), "%s", message);
	return -1;
}

static int check_fast(pakr_xz *encoder, size_t result) {
	if (FL2_isError(result)) return fail(encoder, FL2_getErrorName(result));
	return 0;
}

static int header(pakr_xz *encoder) {
	lzma_stream_flags flags = {0};
	flags.check = LZMA_CHECK_CRC64;
	lzma_options_lzma options = {0};
	options.dict_size = encoder->dictionary;
	lzma_filter filters[] = {{LZMA_FILTER_LZMA2, &options}, {LZMA_VLI_UNKNOWN, NULL}};
	lzma_block block = {0};
	block.check = flags.check;
	block.filters = filters;
	/* Sizes are unknown until the streaming encoder reaches its end marker. */
	block.compressed_size = LZMA_VLI_UNKNOWN;
	block.uncompressed_size = LZMA_VLI_UNKNOWN;
	uint8_t property;
	if (lzma_properties_encode(filters, &property) != LZMA_OK || property != FL2_getCCtxDictProp(encoder->fast)) return fail(encoder, "XZ dictionary property mismatch");
	if (lzma_stream_header_encode(&flags, encoder->framing) != LZMA_OK || lzma_block_header_size(&block) != LZMA_OK || lzma_block_header_encode(&block, encoder->framing + LZMA_STREAM_HEADER_SIZE) != LZMA_OK) return fail(encoder, "cannot encode XZ header");
	encoder->block_header_size = block.header_size;
	encoder->length = LZMA_STREAM_HEADER_SIZE + block.header_size;
	return 0;
}

static int trailer(pakr_xz *encoder) {
	encoder->position = 0;
	encoder->length = 0;
	size_t padding = (4 - (encoder->compressed & 3)) & 3;
	while (encoder->length < padding) encoder->framing[encoder->length++] = 0;
	for (unsigned byte = 0; byte < 8; ++byte) encoder->framing[encoder->length++] = (uint8_t)(encoder->crc >> (byte * 8));
	lzma_index *index = lzma_index_init(NULL);
	if (!index) return fail(encoder, "cannot allocate XZ Index");
	lzma_vli unpadded = encoder->block_header_size + encoder->compressed + 8;
	lzma_ret result = lzma_index_append(index, NULL, unpadded, encoder->uncompressed);
	lzma_stream_flags flags = {0};
	flags.check = LZMA_CHECK_CRC64;
	flags.backward_size = lzma_index_size(index);
	if (result == LZMA_OK) result = lzma_index_buffer_encode(index, encoder->framing, &encoder->length, sizeof(encoder->framing) - LZMA_STREAM_HEADER_SIZE);
	lzma_index_end(index, NULL);
	if (result != LZMA_OK || lzma_stream_footer_encode(&flags, encoder->framing + encoder->length) != LZMA_OK) return fail(encoder, "cannot encode XZ Index/footer");
	encoder->length += LZMA_STREAM_HEADER_SIZE;
	encoder->phase = 2;
	return 0;
}

pakr_xz *pakr_xz_new(int level, int threads, int extreme, uint64_t memory) {
	pakr_xz *encoder = calloc(1, sizeof(*encoder));
	if (!encoder) return NULL;
	if (level < 1 || level > 9 || threads < 1 || threads > FL2_MAXTHREADS) {
		fail(encoder, "XZ worker count must be between 1 and 200 and level between 1 and 9");
		return encoder;
	}
	encoder->dictionary = dictionaries[level];
	encoder->workers = threads;
	/* Include Go buffers, allocator overhead, control thread and worker stacks.
	 * Admission never reduces dictionary, effort or requested worker count. */
	uint64_t reserve = (16 + UINT64_C(8) * threads) * MIB;
	uint64_t required;
	lzma_mt legacy = {0};
	if (extreme || level == 1) {
		encoder->preset = pakr_level(4, level);
		legacy.threads = threads;
		legacy.preset = encoder->preset | (extreme ? LZMA_PRESET_EXTREME : 0);
		legacy.check = LZMA_CHECK_CRC64;
		legacy.timeout = 20;
		required = lzma_stream_encoder_mt_memusage(&legacy);
	} else {
		encoder->preset = presets[level];
		FL2_compressionParameters parameters;
		if (check_fast(encoder, FL2_getLevelParameters(encoder->preset, 0, &parameters))) return encoder;
		parameters.dictionarySize = encoder->dictionary;
		required = FL2_estimateCStreamSize_byParams(&parameters, threads, 0);
	}
	if (required == UINT64_MAX || memory < reserve || required > memory - reserve) {
		fail(encoder, "XZ encoder exceeds memory budget; lower --threads or --level, or raise --memory");
		return encoder;
	}
	encoder->memory = required + reserve;
	if (extreme || level == 1) {
		if (lzma_stream_encoder_mt(&encoder->legacy, &legacy) != LZMA_OK) fail(encoder, "cannot initialize liblzma XZ encoder");
		return encoder;
	}
	encoder->fast = FL2_createCStreamMt(threads, 0);
	if (!encoder->fast) {
		fail(encoder, "cannot allocate Fast LZMA2 stream");
		return encoder;
	}
	if (FL2_getCCtxThreadCount(encoder->fast) != (unsigned)threads) {
		fail(encoder, "Fast LZMA2 did not admit the requested workers");
		return encoder;
	}
	if (check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_compressionLevel, encoder->preset)) || check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_dictionarySize, encoder->dictionary)) || check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_doXXHash, 0)) || check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_omitProperties, 1))) return encoder;
	if (check_fast(encoder, FL2_initCStream(encoder->fast, 0))) return encoder;
	encoder->initialized = 1;
	/* Timed waits return control to Go for cancellation, including finalization.
	 * The extra control thread coordinates the requested compression workers. */
	if (check_fast(encoder, FL2_setCStreamTimeout(encoder->fast, 20))) return encoder;
	header(encoder);
	return encoder;
}

const char *pakr_xz_error(pakr_xz *encoder) {
	return encoder->error;
}

int pakr_xz_parameter(pakr_xz *encoder, int parameter) {
	if (parameter == 3) return encoder->fast != NULL;
	if (parameter == 4) return encoder->dictionary;
	if (parameter == 5) return encoder->preset;
	return encoder->workers;
}

uint64_t pakr_xz_memory(pakr_xz *encoder) {
	return encoder->memory;
}

int pakr_xz_step(pakr_xz *encoder, const void *input, size_t *input_size, void *output, size_t *output_size, int finish) {
	size_t available_input = *input_size;
	size_t available_output = *output_size;
	*input_size = 0;
	*output_size = 0;
	if (encoder->error[0]) return -1;
	if (!encoder->fast) {
		encoder->legacy.next_in = input;
		encoder->legacy.avail_in = available_input;
		encoder->legacy.next_out = output;
		encoder->legacy.avail_out = available_output;
		lzma_ret result = lzma_code(&encoder->legacy, finish ? LZMA_FINISH : LZMA_RUN);
		*input_size = available_input - encoder->legacy.avail_in;
		*output_size = available_output - encoder->legacy.avail_out;
		encoder->legacy.next_in = NULL;
		encoder->legacy.next_out = NULL;
		if (result == LZMA_STREAM_END) return 1;
		if (result != LZMA_OK) return fail(encoder, "liblzma XZ encoding failed");
		return 2;
	}
	if (encoder->phase == 3) return 1;
	if (encoder->phase == 0 || encoder->phase == 2) {
		size_t count = encoder->length - encoder->position;
		if (count > available_output) count = available_output;
		if (count) memcpy(output, encoder->framing + encoder->position, count);
		encoder->position += count;
		*output_size = count;
		if (encoder->position == encoder->length) ++encoder->phase;
		return encoder->phase == 3 ? 1 : 0;
	}
	FL2_inBuffer in = {input, available_input, 0};
	FL2_outBuffer out = {output, available_output, 0};
	size_t result = finish ? FL2_endStream(encoder->fast, &out) : FL2_compressStream(encoder->fast, &out, &in);
	*input_size = in.pos;
	*output_size = out.pos;
	if (in.pos > LZMA_VLI_MAX - encoder->uncompressed || out.pos > LZMA_VLI_MAX - encoder->compressed - encoder->block_header_size - 8) return fail(encoder, "XZ block exceeds format size limit");
	encoder->uncompressed += in.pos;
	encoder->compressed += out.pos;
	if (in.pos) encoder->crc = lzma_crc64(input, in.pos, encoder->crc);
	/* Timeout is an error code in FL2, but consumed/produced bytes remain valid. */
	if (FL2_isTimedOut(result)) return 2;
	if (check_fast(encoder, result)) return -1;
	if (finish && result == 0 && trailer(encoder)) return -1;
	return 2;
}

void pakr_xz_free(pakr_xz *encoder) {
	if (!encoder) return;
	/* FL2_freeCStream alone does not stop jobs before releasing their dictionary. */
	if (encoder->initialized) FL2_cancelCStream(encoder->fast);
	FL2_freeCStream(encoder->fast);
	lzma_end(&encoder->legacy);
	free(encoder);
}
