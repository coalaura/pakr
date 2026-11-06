#include "lzip.h"
#include <lzma.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct pakr_lzip {
	lzma_stream stream;
	uint64_t memory;
	uint64_t compressed;
	uint64_t uncompressed;
	uint32_t crc;
	int encode;
	int phase;
	size_t position;
	uint8_t framing[20];
	char error[160];
};

static int fail(pakr_lzip *codec, const char *message) {
	snprintf(codec->error, sizeof(codec->error), "lzip: %s", message);
	return -1;
}

static uint64_t load(const uint8_t *data, unsigned length) {
	uint64_t value = 0;
	for (unsigned index = 0; index < length; ++index) value |= (uint64_t)data[index] << (8 * index);
	return value;
}

static void store(uint8_t *data, uint64_t value, unsigned length) {
	for (unsigned index = 0; index < length; ++index) data[index] = value >> (8 * index);
}

static int initialize(pakr_lzip *codec, lzma_options_lzma *options) {
	lzma_filter filters[] = {{LZMA_FILTER_LZMA1, options}, {LZMA_VLI_UNKNOWN, NULL}};
	uint64_t required = codec->encode ? lzma_raw_encoder_memusage(filters) : lzma_raw_decoder_memusage(filters);
	/* Include Go streaming buffers and framing state in the codec budget. */
	uint64_t reserve = UINT64_C(4) << 20;
	if (required == UINT64_MAX) return fail(codec, "unsupported LZMA1 parameters");
	if (codec->memory < reserve || required > codec->memory - reserve) {
		snprintf(codec->error, sizeof(codec->error), "lzip: needs %llu bytes of codec memory; budget is %llu bytes", (unsigned long long)(required + reserve), (unsigned long long)codec->memory);
		return -1;
	}
	lzma_ret result = codec->encode ? lzma_raw_encoder(&codec->stream, filters) : lzma_raw_decoder(&codec->stream, filters);
	if (result != LZMA_OK) return fail(codec, "cannot initialize LZMA1 state");
	return 0;
}

pakr_lzip *pakr_lzip_new(int encode, int level, uint64_t memory, int64_t size) {
	static const uint32_t dictionaries[] = {1 << 20, 3 << 19, 1 << 21, 3 << 20, 1 << 22, 1 << 23, 1 << 24, 3 << 23, 1 << 25};
	static const uint32_t lengths[] = {32, 32, 32, 32, 32, 64, 128, 163, 273};
	pakr_lzip *codec = calloc(1, sizeof(*codec));
	if (!codec) return NULL;
	codec->memory = memory;
	codec->encode = encode;
	codec->compressed = 6;
	if (!encode) return codec;
	if (level < 1 || level > 9) {
		fail(codec, "invalid compression level");
		return codec;
	}
	lzma_options_lzma options = {0};
	options.dict_size = dictionaries[level - 1];
	options.lc = 3;
	options.pb = 2;
	options.mode = level <= 2 ? LZMA_MODE_FAST : LZMA_MODE_NORMAL;
	options.mf = level == 1 ? LZMA_MF_HC3 : level == 2 ? LZMA_MF_HC4 : LZMA_MF_BT4;
	options.nice_len = lengths[level - 1];
	/* A known input cannot reference bytes preceding its beginning. Round up to a representable dictionary. */
	uint32_t needed = size < 0 || (uint64_t)size >= options.dict_size ? options.dict_size : (uint32_t)size;
	unsigned exponent = 12;
	while ((UINT32_C(1) << exponent) < needed) ++exponent;
	uint32_t dictionary = UINT32_C(1) << exponent;
	unsigned wedges = 0;
	while (wedges < 7 && dictionary - (dictionary >> 4) * (wedges + 1) >= needed && dictionary - (dictionary >> 4) * (wedges + 1) >= 4096) ++wedges;
	options.dict_size = dictionary - (dictionary >> 4) * wedges;
	memcpy(codec->framing, "LZIP\1", 5);
	codec->framing[5] = exponent | (wedges << 5);
	initialize(codec, &options);
	return codec;
}

const char *pakr_lzip_error(pakr_lzip *codec) {
	return codec->error;
}

int pakr_lzip_step(pakr_lzip *codec, const void *input, size_t *input_size, void *output, size_t *output_size, int finish) {
	size_t available_input = *input_size;
	size_t available_output = *output_size;
	*input_size = 0;
	*output_size = 0;
	if (codec->phase == 3) return 1;
	if (codec->phase != 1) {
		size_t length = codec->phase == 0 ? 6 : 20;
		size_t count = length - codec->position;
		if (codec->encode) {
			if (count > available_output) count = available_output;
			if (count) memcpy(output, codec->framing + codec->position, count);
			*output_size = count;
		} else {
			if (count > available_input) count = available_input;
			if (count) memcpy(codec->framing + codec->position, input, count);
			*input_size = count;
		}
		codec->position += count;
		if (codec->position != length) {
			if (!codec->encode && finish) return fail(codec, codec->phase == 0 ? "truncated member header" : "truncated member trailer");
			return 0;
		}
		codec->position = 0;
		if (codec->phase == 0 && !codec->encode) {
			if (memcmp(codec->framing, "LZIP", 4)) return fail(codec, "invalid member header or unexpected trailing data");
			if (codec->framing[4] != 1) return fail(codec, "unsupported header version (expected 1)");
			unsigned exponent = codec->framing[5] & 31;
			unsigned wedges = codec->framing[5] >> 5;
			if (exponent < 12 || exponent > 29) return fail(codec, "invalid dictionary size");
			uint32_t dictionary = UINT32_C(1) << exponent;
			dictionary -= (dictionary >> 4) * wedges;
			if (dictionary < 4096) return fail(codec, "invalid dictionary size");
			lzma_options_lzma options = {0};
			options.dict_size = dictionary;
			options.lc = 3;
			options.pb = 2;
			if (initialize(codec, &options)) return -1;
		} else if (codec->phase == 2 && !codec->encode) {
			if (load(codec->framing, 4) != codec->crc) return fail(codec, "CRC32 mismatch");
			if (load(codec->framing + 4, 8) != codec->uncompressed) return fail(codec, "uncompressed size mismatch");
			if (load(codec->framing + 12, 8) != codec->compressed + 20) return fail(codec, "compressed member size mismatch");
		}
		++codec->phase;
		return codec->phase == 3;
	}
	codec->stream.next_in = input;
	codec->stream.avail_in = available_input;
	codec->stream.next_out = output;
	codec->stream.avail_out = available_output;
	lzma_ret result = lzma_code(&codec->stream, finish ? LZMA_FINISH : LZMA_RUN);
	*input_size = available_input - codec->stream.avail_in;
	*output_size = available_output - codec->stream.avail_out;
	codec->stream.next_in = NULL;
	codec->stream.next_out = NULL;
	size_t plain = codec->encode ? *input_size : *output_size;
	size_t packed = codec->encode ? *output_size : *input_size;
	if (plain > UINT64_MAX - codec->uncompressed || packed > UINT64_MAX - 20 - codec->compressed) return fail(codec, "member size overflow");
	codec->uncompressed += plain;
	codec->compressed += packed;
	codec->crc = lzma_crc32(codec->encode ? input : output, plain, codec->crc);
	if (result == LZMA_STREAM_END) {
		codec->phase = 2;
		if (codec->encode) {
			store(codec->framing, codec->crc, 4);
			store(codec->framing + 4, codec->uncompressed, 8);
			store(codec->framing + 12, codec->compressed + 20, 8);
		}
	} else if (result != LZMA_OK && result != LZMA_BUF_ERROR) {
		return fail(codec, "invalid LZMA1 payload or end marker");
	} else if (finish && !*input_size && !*output_size) {
		return fail(codec, "truncated LZMA1 payload or missing end marker");
	}
	return 0;
}

void pakr_lzip_free(pakr_lzip *codec) {
	if (!codec) return;
	lzma_end(&codec->stream);
	free(codec);
}
