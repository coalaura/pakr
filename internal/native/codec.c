#include "codec.h"
#include "levels.h"
#include "lzip.h"
#include "xz.h"
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <zlib.h>
#define ZSTD_STATIC_LINKING_ONLY
#include <zstd.h>
#include <bzlib.h>
#include <lzma.h>
#define LZ4F_STATIC_LINKING_ONLY
#include <lz4frame.h>
#include <lz4hc.h>
#include <brotli/encode.h>
#include <brotli/decode.h>
#include <libdeflate.h>

struct pakr_codec {
	int format;
	int encode;
	int started;
	int initialized;
	int level;
	int threads;
	int extreme;
	char error[160];
	z_stream zlib;
	bz_stream bzip;
	lzma_stream xz;
	ZSTD_CCtx *zstd_encoder;
	ZSTD_DCtx *zstd_decoder;
	LZ4F_cctx *lz4_encoder;
	LZ4F_dctx *lz4_decoder;
	LZ4F_preferences_t lz4_preferences;
	BrotliEncoderState *brotli_encoder;
	BrotliDecoderState *brotli_decoder;
	pakr_lzip *lzip;
	pakr_xz *xz_encoder;
	int pending;
};

static int fail(pakr_codec *codec, const char *message) {
	snprintf(codec->error, sizeof(codec->error), "%s", message);
	return -1;
}

pakr_codec *pakr_codec_new(int format, int encode, int level, int threads, int extreme) {
	if (format == 4 && encode) return pakr_codec_new_xz(level, threads, extreme, UINT64_C(2) << 30);
	pakr_codec *codec = calloc(1, sizeof(*codec));
	if (!codec) {
		return NULL;
	}
	codec->format = format;
	codec->encode = encode;
	level = pakr_level(format, level);
	codec->level = level;
	codec->threads = 1;
	codec->extreme = format == 4 && extreme;
	int result = 0;
	switch (format) {
	case 1:
	case 7:
		if (encode) {
			result = deflateInit2(&codec->zlib, level, Z_DEFLATED, format == 7 ? -15 : 31, 8, Z_DEFAULT_STRATEGY);
		} else {
			result = inflateInit2(&codec->zlib, format == 7 ? -15 : 31);
		}
		codec->initialized = result == Z_OK;
		break;
	case 2:
		if (encode) {
			codec->zstd_encoder = ZSTD_createCCtx();
			if (!codec->zstd_encoder) { result = -1; break; }
			ZSTD_compressionParameters parameters = ZSTD_getCParams(level, 0, 0);
			/* Account for match tables, job input/output, and overlap before enabling workers. */
			uint64_t per_worker = ZSTD_estimateCCtxSize_usingCParams(parameters) + (UINT64_C(6) << parameters.windowLog);
			uint64_t budget = UINT64_C(2) << 30;
			while (threads > 1 && per_worker > budget / threads) --threads;
			codec->threads = threads;
			if (ZSTD_isError(ZSTD_CCtx_setParameter(codec->zstd_encoder, ZSTD_c_compressionLevel, level)) || ZSTD_isError(ZSTD_CCtx_setParameter(codec->zstd_encoder, ZSTD_c_checksumFlag, 1)) || ZSTD_isError(ZSTD_CCtx_setParameter(codec->zstd_encoder, ZSTD_c_nbWorkers, threads > 1 ? threads : 0))) {
				result = -1;
			}
		} else {
			codec->zstd_decoder = ZSTD_createDCtx();
			result = codec->zstd_decoder ? 0 : -1;
			if (codec->zstd_decoder && ZSTD_isError(ZSTD_DCtx_setParameter(codec->zstd_decoder, ZSTD_d_windowLogMax, 30))) result = -1;
		}
		break;
	case 3:
		result = encode ? BZ2_bzCompressInit(&codec->bzip, level, 0, 30) : BZ2_bzDecompressInit(&codec->bzip, 0, 0);
		codec->initialized = result == BZ_OK;
		break;
	case 4: {
		lzma_mt options = {0};
		options.threads = threads;
		options.flags = LZMA_CONCATENATED;
		options.memlimit_threading = UINT64_C(512) << 20;
		options.memlimit_stop = UINT64_C(2) << 30;
		result = lzma_stream_decoder_mt(&codec->xz, &options);
		codec->initialized = result == LZMA_OK;
		break;
	}
	case 5:
		if (encode) {
			result = LZ4F_isError(LZ4F_createCompressionContext(&codec->lz4_encoder, LZ4F_VERSION));
			codec->lz4_preferences.frameInfo.contentChecksumFlag = LZ4F_contentChecksumEnabled;
			codec->lz4_preferences.frameInfo.blockSizeID = LZ4F_max256KB;
			codec->lz4_preferences.compressionLevel = level;
			/* Each worker can retain two jobs, with both input and output buffers. */
			int limit = (256U << 20) / ((4U << 20) + LZ4_sizeofStateHC());
			if ((unsigned)limit > LZ4F_getMaxNbWorkers()) limit = LZ4F_getMaxNbWorkers();
			codec->lz4_preferences.nbWorkers = threads > 1 ? (threads > limit ? limit : threads) : 0;
		} else {
			result = LZ4F_isError(LZ4F_createDecompressionContext(&codec->lz4_decoder, LZ4F_VERSION));
		}
		break;
	case 6:
		if (encode) {
			codec->brotli_encoder = BrotliEncoderCreateInstance(NULL, NULL, NULL);
			result = codec->brotli_encoder && BrotliEncoderSetParameter(codec->brotli_encoder, BROTLI_PARAM_QUALITY, level) ? 0 : -1;
		} else {
			codec->brotli_decoder = BrotliDecoderCreateInstance(NULL, NULL, NULL);
			result = codec->brotli_decoder ? 0 : -1;
		}
		break;
	default:
		result = -1;
	}
	if (result != 0) {
		fail(codec, "cannot initialize native codec");
	}
	return codec;
}

const char *pakr_codec_error(pakr_codec *codec) {
	if (codec->xz_encoder) return pakr_xz_error(codec->xz_encoder);
	if (codec->lzip) return pakr_lzip_error(codec->lzip);
	return codec->error;
}

pakr_codec *pakr_codec_new_xz(int level, int threads, int extreme, uint64_t memory) {
	pakr_codec *codec = calloc(1, sizeof(*codec));
	if (!codec) return NULL;
	codec->format = 4;
	codec->encode = 1;
	codec->level = pakr_level(4, level);
	codec->threads = threads;
	codec->extreme = extreme;
	codec->xz_encoder = pakr_xz_new(level, threads, extreme, memory);
	if (!codec->xz_encoder) fail(codec, "cannot allocate XZ state");
	return codec;
}

uint64_t pakr_codec_memory(pakr_codec *codec) {
	return codec->xz_encoder ? pakr_xz_memory(codec->xz_encoder) : 0;
}

int pakr_codec_pending(pakr_codec *codec) {
	return codec->pending;
}

pakr_codec *pakr_codec_new_lzip(int encode, int level, uint64_t memory, int64_t size) {
	pakr_codec *codec = calloc(1, sizeof(*codec));
	if (!codec) return NULL;
	codec->format = 11;
	codec->encode = encode;
	codec->level = level;
	codec->threads = 1;
	codec->lzip = pakr_lzip_new(encode, level, memory, size);
	if (!codec->lzip) fail(codec, "cannot allocate lzip state");
	return codec;
}

pakr_codec *pakr_codec_new_lzma(int level, uint64_t memory) {
	pakr_codec *codec = calloc(1, sizeof(*codec));
	if (!codec) return NULL;
	codec->format = 12;
	codec->encode = 1;
	codec->level = pakr_level(4, level);
	codec->threads = 1;
	lzma_options_lzma options;
	if (lzma_lzma_preset(&options, codec->level)) {
		fail(codec, "invalid LZMA-Alone preset");
		return codec;
	}
	lzma_filter filters[] = {{LZMA_FILTER_LZMA1, &options}, {LZMA_VLI_UNKNOWN, NULL}};
	uint64_t required = lzma_raw_encoder_memusage(filters);
	/* Reserve the Go input/output buffers as well as native framing state. */
	uint64_t reserve = UINT64_C(4) << 20;
	if (required == UINT64_MAX || memory < reserve || required > memory - reserve) {
		fail(codec, "LZMA-Alone encoder exceeds memory budget; lower --level or raise --memory");
		return codec;
	}
	if (lzma_alone_encoder(&codec->xz, &options) != LZMA_OK) fail(codec, "cannot initialize LZMA-Alone encoder");
	return codec;
}

size_t pakr_codec_bound(pakr_codec *codec, size_t input_size) {
	if (codec->format == 5) return LZ4F_compressBound(input_size, &codec->lz4_preferences);
	return 2 * input_size;
}

int pakr_codec_parameter(pakr_codec *codec, int parameter) {
	if (codec->xz_encoder && (parameter == 1 || parameter >= 3)) return pakr_xz_parameter(codec->xz_encoder, parameter);
	if (parameter == 2) return codec->extreme;
	if (codec->zstd_encoder) {
		int value = 0;
		ZSTD_CCtx_getParameter(codec->zstd_encoder, parameter ? ZSTD_c_nbWorkers : ZSTD_c_compressionLevel, &value);
		return value;
	}
	if (codec->lz4_encoder) return parameter ? codec->lz4_preferences.nbWorkers : codec->lz4_preferences.compressionLevel;
	return parameter ? codec->threads : codec->level;
}

int pakr_codec_step(pakr_codec *codec, const void *input, size_t *input_size, void *output, size_t *output_size, int finish) {
	if (codec->xz_encoder) {
		int status = pakr_xz_step(codec->xz_encoder, input, input_size, output, output_size, finish);
		codec->pending = status == 2;
		return status;
	}
	if (codec->lzip) return pakr_lzip_step(codec->lzip, input, input_size, output, output_size, finish);
	size_t available_input = *input_size;
	size_t available_output = *output_size;
	int status = 0;
	switch (codec->format) {
	case 1:
	case 7: {
		codec->zlib.next_in = (Bytef *)input;
		codec->zlib.avail_in = available_input;
		codec->zlib.next_out = output;
		codec->zlib.avail_out = available_output;
		int result = codec->encode ? deflate(&codec->zlib, finish ? Z_FINISH : Z_NO_FLUSH) : inflate(&codec->zlib, Z_NO_FLUSH);
		*input_size -= codec->zlib.avail_in;
		*output_size -= codec->zlib.avail_out;
		codec->zlib.next_in = NULL;
		codec->zlib.next_out = NULL;
		if (result == Z_STREAM_END) { status = 1; }
		else if (result != Z_OK && result != Z_BUF_ERROR) { status = fail(codec, codec->zlib.msg ? codec->zlib.msg : "invalid deflate stream"); }
		break;
	}
	case 2: {
		ZSTD_inBuffer in = {input, available_input, 0};
		ZSTD_outBuffer out = {output, available_output, 0};
		size_t result = codec->encode ? ZSTD_compressStream2(codec->zstd_encoder, &out, &in, finish ? ZSTD_e_end : ZSTD_e_continue) : ZSTD_decompressStream(codec->zstd_decoder, &out, &in);
		*input_size = in.pos;
		*output_size = out.pos;
		if (ZSTD_isError(result)) { status = fail(codec, ZSTD_getErrorName(result)); }
		else if (result == 0 && (!codec->encode || finish)) { status = 1; }
		break;
	}
	case 3: {
		codec->bzip.next_in = (char *)input;
		codec->bzip.avail_in = available_input;
		codec->bzip.next_out = output;
		codec->bzip.avail_out = available_output;
		int result = codec->encode ? BZ2_bzCompress(&codec->bzip, finish ? BZ_FINISH : BZ_RUN) : BZ2_bzDecompress(&codec->bzip);
		*input_size -= codec->bzip.avail_in;
		*output_size -= codec->bzip.avail_out;
		codec->bzip.next_in = NULL;
		codec->bzip.next_out = NULL;
		if (result == BZ_STREAM_END) { status = 1; }
		else if (result < 0) { status = fail(codec, "invalid bzip2 stream"); }
		break;
	}
	case 4:
	case 12: {
		codec->xz.next_in = input;
		codec->xz.avail_in = available_input;
		codec->xz.next_out = output;
		codec->xz.avail_out = available_output;
		lzma_ret result = lzma_code(&codec->xz, finish ? LZMA_FINISH : LZMA_RUN);
		*input_size -= codec->xz.avail_in;
		*output_size -= codec->xz.avail_out;
		codec->xz.next_in = NULL;
		codec->xz.next_out = NULL;
		if (result == LZMA_STREAM_END) { status = 1; }
		else if (result != LZMA_OK && result != LZMA_BUF_ERROR) { status = fail(codec, codec->format == 12 ? "LZMA-Alone encoding failed" : "invalid xz stream or memory limit exceeded"); }
		break;
	}
	case 5: {
		size_t result;
		if (!codec->encode) {
			result = LZ4F_decompress(codec->lz4_decoder, output, output_size, input, input_size, NULL);
			if (result == 0) { status = 1; }
		} else if (!codec->started) {
			result = LZ4F_compressBegin(codec->lz4_encoder, output, available_output, &codec->lz4_preferences);
			if (LZ4F_isError(result) && codec->lz4_preferences.nbWorkers) {
				LZ4F_freeCompressionContext(codec->lz4_encoder);
				codec->lz4_encoder = NULL;
				codec->lz4_preferences.nbWorkers = 0;
				result = LZ4F_createCompressionContext(&codec->lz4_encoder, LZ4F_VERSION);
				if (!LZ4F_isError(result)) result = LZ4F_compressBegin(codec->lz4_encoder, output, available_output, &codec->lz4_preferences);
			}
			*input_size = 0;
			*output_size = result;
			codec->started = 1;
		} else if (available_input != 0) {
			result = LZ4F_compressUpdate(codec->lz4_encoder, output, available_output, input, available_input, NULL);
			*output_size = result;
		} else if (finish) {
			result = LZ4F_compressEnd(codec->lz4_encoder, output, available_output, NULL);
			*output_size = result;
			status = result < available_output;
		} else {
			result = 0;
			*output_size = 0;
		}
		if (LZ4F_isError(result)) {
			*output_size = 0;
			status = fail(codec, LZ4F_getErrorName(result));
		}
		break;
	}
	case 6: {
		const uint8_t *next_in = input;
		uint8_t *next_out = output;
		if (codec->encode) {
			if (!BrotliEncoderCompressStream(codec->brotli_encoder, finish ? BROTLI_OPERATION_FINISH : BROTLI_OPERATION_PROCESS, &available_input, &next_in, &available_output, &next_out, NULL)) {
				status = fail(codec, "brotli encoding failed");
			} else if (BrotliEncoderIsFinished(codec->brotli_encoder)) { status = 1; }
		} else {
			BrotliDecoderResult result = BrotliDecoderDecompressStream(codec->brotli_decoder, &available_input, &next_in, &available_output, &next_out, NULL);
			if (result == BROTLI_DECODER_RESULT_SUCCESS) { status = 1; }
			else if (result == BROTLI_DECODER_RESULT_ERROR) { status = fail(codec, BrotliDecoderErrorString(BrotliDecoderGetErrorCode(codec->brotli_decoder))); }
		}
		*input_size -= available_input;
		*output_size -= available_output;
		break;
	}
	}
	/* Native contexts must not retain pointers into Go-managed buffers between calls. */
	return status;
}

void pakr_codec_free(pakr_codec *codec) {
	if (!codec) { return; }
	if (codec->initialized) {
		if (codec->format == 1 || codec->format == 7) {
			if (codec->encode) { deflateEnd(&codec->zlib); } else { inflateEnd(&codec->zlib); }
		} else if (codec->format == 3) {
			if (codec->encode) { BZ2_bzCompressEnd(&codec->bzip); } else { BZ2_bzDecompressEnd(&codec->bzip); }
		}
	}
	if (codec->format == 4 || codec->format == 12) { lzma_end(&codec->xz); }
	ZSTD_freeCCtx(codec->zstd_encoder);
	ZSTD_freeDCtx(codec->zstd_decoder);
	LZ4F_freeCompressionContext(codec->lz4_encoder);
	LZ4F_freeDecompressionContext(codec->lz4_decoder);
	BrotliEncoderDestroyInstance(codec->brotli_encoder);
	BrotliDecoderDestroyInstance(codec->brotli_decoder);
	pakr_lzip_free(codec->lzip);
	pakr_xz_free(codec->xz_encoder);
	free(codec);
}
