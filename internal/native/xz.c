#include "xz.h"
#include "levels.h"
#include <fast-lzma2.h>
#include <lzma.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MIB (UINT64_C(1) << 20)
#define INDEX_BLOCKS 1024
#define OUTPUT_CHUNK (64 << 10)

enum phase {
	/* Each framing phase is immediately followed by its next phase. */
	STREAM_HEADER,
	ENCODE,
	BLOCK_HEADER,
	BLOCK_DATA,
	BLOCK_CHECK,
	NEXT_BLOCK,
	STREAM_TRAILER,
	NEXT_STREAM,
	DONE
};

struct pakr_xz {
	FL2_CStream *fast;
	lzma_stream legacy;
	uint64_t memory;
	lzma_vli compressed;
	lzma_vli uncompressed;
	uint64_t crc;
	uint64_t progress_in;
	uint64_t progress_out;
	uint32_t dictionary;
	size_t block_limit;
	size_t chunks;
	size_t capacity_limit;
	uint8_t **data;
	size_t records_size;
	unsigned records;
	unsigned workers;
	unsigned preset;
	int initialized;
	enum phase phase;
	size_t position;
	size_t length;
	/* Concatenate streams at the record limit: memory never grows with the file. */
	uint8_t index[18 * INDEX_BLOCKS];
	uint8_t framing[18 * INDEX_BLOCKS + 32];
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

static void little32(uint8_t *output, uint32_t value) {
	for (unsigned byte = 0; byte < 4; ++byte) output[byte] = (uint8_t)(value >> (byte * 8));
}

static int stream_header(pakr_xz *encoder) {
	lzma_stream_flags flags = {0};
	flags.check = LZMA_CHECK_CRC64;
	if (lzma_stream_header_encode(&flags, encoder->framing) != LZMA_OK) return fail(encoder, "cannot encode XZ stream header");
	encoder->position = 0;
	encoder->length = LZMA_STREAM_HEADER_SIZE;
	encoder->records = 0;
	encoder->records_size = 0;
	encoder->phase = STREAM_HEADER;
	return 0;
}

static int block_header(pakr_xz *encoder) {
	lzma_options_lzma options = {0};
	options.dict_size = encoder->dictionary;
	lzma_filter filters[] = {{LZMA_FILTER_LZMA2, &options}, {LZMA_VLI_UNKNOWN, NULL}};
	lzma_block block = {0};
	block.check = LZMA_CHECK_CRC64;
	block.filters = filters;
	/* Both sizes are required by XZ Utils' threaded streaming decoder. */
	block.compressed_size = encoder->compressed;
	block.uncompressed_size = encoder->uncompressed;
	uint8_t property;
	if (lzma_properties_encode(filters, &property) != LZMA_OK || property != FL2_getCCtxDictProp(encoder->fast)) return fail(encoder, "XZ dictionary property mismatch");
	if (lzma_block_header_size(&block) != LZMA_OK || lzma_block_header_encode(&block, encoder->framing) != LZMA_OK) return fail(encoder, "cannot encode XZ block header");
	lzma_vli unpadded = block.header_size + encoder->compressed + 8;
	if (encoder->records == INDEX_BLOCKS || lzma_vli_encode(unpadded, NULL, encoder->index, &encoder->records_size, sizeof(encoder->index)) != LZMA_OK || lzma_vli_encode(encoder->uncompressed, NULL, encoder->index, &encoder->records_size, sizeof(encoder->index)) != LZMA_OK) return fail(encoder, "XZ Index record limit exceeded");
	++encoder->records;
	encoder->position = 0;
	encoder->length = block.header_size;
	encoder->phase = BLOCK_HEADER;
	return 0;
}

static void block_check(pakr_xz *encoder) {
	encoder->position = 0;
	encoder->length = 0;
	size_t padding = (4 - (encoder->compressed & 3)) & 3;
	while (encoder->length < padding) encoder->framing[encoder->length++] = 0;
	for (unsigned byte = 0; byte < 8; ++byte) encoder->framing[encoder->length++] = (uint8_t)(encoder->crc >> (byte * 8));
	encoder->phase = BLOCK_CHECK;
}

static int stream_trailer(pakr_xz *encoder) {
	encoder->position = 0;
	encoder->length = 1;
	encoder->framing[0] = 0;
	if (lzma_vli_encode(encoder->records, NULL, encoder->framing, &encoder->length, sizeof(encoder->framing)) != LZMA_OK) return fail(encoder, "cannot encode XZ Index count");
	memcpy(encoder->framing + encoder->length, encoder->index, encoder->records_size);
	encoder->length += encoder->records_size;
	while (encoder->length & 3) encoder->framing[encoder->length++] = 0;
	little32(encoder->framing + encoder->length, lzma_crc32(encoder->framing, encoder->length, 0));
	encoder->length += 4;
	lzma_stream_flags flags = {0};
	flags.check = LZMA_CHECK_CRC64;
	flags.backward_size = encoder->length;
	if (lzma_stream_footer_encode(&flags, encoder->framing + encoder->length) != LZMA_OK) return fail(encoder, "cannot encode XZ footer");
	encoder->length += LZMA_STREAM_HEADER_SIZE;
	encoder->phase = STREAM_TRAILER;
	return 0;
}

static int start_block(pakr_xz *encoder) {
	if (check_fast(encoder, FL2_initCStream(encoder->fast, 0))) return -1;
	encoder->initialized = 1;
	encoder->compressed = 0;
	encoder->uncompressed = 0;
	encoder->crc = 0;
	encoder->progress_in = 0;
	encoder->progress_out = 0;
	encoder->phase = ENCODE;
	return 0;
}

static int grow_block(pakr_xz *encoder) {
	if (encoder->compressed == encoder->capacity_limit) return fail(encoder, "Fast LZMA2 exceeded the XZ block buffer bound");
	if (encoder->compressed < encoder->chunks * OUTPUT_CHUNK) return 0;
	/* Fixed chunks avoid realloc copies and transient old-plus-new allocations.
	 * Retain them across blocks; small/compressible inputs allocate only what they use. */
	uint8_t *data = malloc(OUTPUT_CHUNK);
	if (!data) return fail(encoder, "cannot allocate XZ block buffer");
	encoder->data[encoder->chunks++] = data;
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
		/* Three dictionaries amortize resets while exposing independent decode jobs.
		 * Only compressed bytes are buffered, lazily, within this admitted bound. */
		encoder->block_limit = (size_t)encoder->dictionary * 3;
		encoder->capacity_limit = FL2_compressBound(encoder->block_limit);
		size_t chunks = (encoder->capacity_limit + OUTPUT_CHUNK - 1) / OUTPUT_CHUNK;
		reserve += chunks * (OUTPUT_CHUNK + sizeof(*encoder->data)) + sizeof(*encoder);
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
	encoder->data = calloc((encoder->capacity_limit + OUTPUT_CHUNK - 1) / OUTPUT_CHUNK, sizeof(*encoder->data));
	if (!encoder->data) {
		fail(encoder, "cannot allocate XZ block buffer table");
		return encoder;
	}
	if (FL2_getCCtxThreadCount(encoder->fast) != (unsigned)threads) {
		fail(encoder, "Fast LZMA2 did not admit the requested workers");
		return encoder;
	}
	if (check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_compressionLevel, encoder->preset)) || check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_dictionarySize, encoder->dictionary)) || check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_doXXHash, 0)) || check_fast(encoder, FL2_CCtx_setParameter(encoder->fast, FL2_p_omitProperties, 1))) return encoder;
	if (start_block(encoder)) return encoder;
	/* Timed waits return control to Go for cancellation, including finalization.
	 * The extra control thread coordinates the requested compression workers. */
	if (check_fast(encoder, FL2_setCStreamTimeout(encoder->fast, 20))) return encoder;
	stream_header(encoder);
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
		uint64_t progress_in, progress_out;
		lzma_get_progress(&encoder->legacy, &progress_in, &progress_out);
		int progressed = progress_in != encoder->progress_in || progress_out != encoder->progress_out;
		encoder->progress_in = progress_in;
		encoder->progress_out = progress_out;
		return progressed ? 3 : 2;
	}
	if (encoder->phase == DONE) return 1;
	if (encoder->phase == NEXT_STREAM) {
		if (finish) {
			encoder->phase = DONE;
			return 1;
		}
		if (start_block(encoder) || stream_header(encoder)) return -1;
		return 3;
	}
	if (encoder->phase == NEXT_BLOCK) {
		if (finish || encoder->records == INDEX_BLOCKS) {
			if (stream_trailer(encoder)) return -1;
		} else if (start_block(encoder)) return -1;
		return 3;
	}
	if (encoder->phase == STREAM_HEADER || encoder->phase == BLOCK_HEADER || encoder->phase == BLOCK_CHECK || encoder->phase == STREAM_TRAILER) {
		size_t count = encoder->length - encoder->position;
		if (count > available_output) count = available_output;
		if (count) memcpy(output, encoder->framing + encoder->position, count);
		encoder->position += count;
		*output_size = count;
		if (encoder->position == encoder->length) {
			++encoder->phase;
			encoder->position = 0;
		}
		return 0;
	}
	if (encoder->phase == BLOCK_DATA) {
		size_t count = encoder->compressed - encoder->position;
		if (count > available_output) count = available_output;
		size_t offset = encoder->position % OUTPUT_CHUNK;
		if (count > OUTPUT_CHUNK - offset) count = OUTPUT_CHUNK - offset;
		if (count) memcpy(output, encoder->data[encoder->position / OUTPUT_CHUNK] + offset, count);
		encoder->position += count;
		*output_size = count;
		if (encoder->position == encoder->compressed) block_check(encoder);
		return 0;
	}
	if (grow_block(encoder)) return -1;
	size_t remaining = encoder->block_limit - encoder->uncompressed;
	if (available_input > remaining) available_input = remaining;
	int ending = finish || remaining == 0;
	FL2_inBuffer in = {input, available_input, 0};
	size_t offset = encoder->compressed % OUTPUT_CHUNK;
	size_t capacity = encoder->capacity_limit - encoder->compressed + offset;
	if (capacity > OUTPUT_CHUNK) capacity = OUTPUT_CHUNK;
	FL2_outBuffer out = {encoder->data[encoder->compressed / OUTPUT_CHUNK], capacity, offset};
	size_t result = ending ? FL2_endStream(encoder->fast, &out) : FL2_compressStream(encoder->fast, &out, &in);
	*input_size = in.pos;
	unsigned long long progress_out;
	uint64_t progress_in = FL2_getCStreamProgress(encoder->fast, &progress_out);
	int progressed = in.pos || out.pos != offset || progress_in != encoder->progress_in || progress_out != encoder->progress_out;
	encoder->progress_in = progress_in;
	encoder->progress_out = progress_out;
	encoder->uncompressed += in.pos;
	encoder->compressed += out.pos - offset;
	if (in.pos) encoder->crc = lzma_crc64(input, in.pos, encoder->crc);
	/* Timeout is an error code in FL2, but consumed/produced bytes remain valid. */
	if (FL2_isTimedOut(result)) return progressed ? 3 : 2;
	if (check_fast(encoder, result)) return -1;
	if (ending && result == 0) {
		if (block_header(encoder)) return -1;
		return 3;
	}
	/* A successful call without byte or internal progress isn't an async wait. */
	return progressed ? 3 : 0;
}

void pakr_xz_free(pakr_xz *encoder) {
	if (!encoder) return;
	/* FL2_freeCStream alone does not stop jobs before releasing their dictionary. */
	if (encoder->initialized) FL2_cancelCStream(encoder->fast);
	FL2_freeCStream(encoder->fast);
	lzma_end(&encoder->legacy);
	for (size_t chunk = 0; chunk < encoder->chunks; ++chunk) free(encoder->data[chunk]);
	free(encoder->data);
	free(encoder);
}
