#include "codec.h"
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <zlib.h>
#include <zstd.h>
#include <bzlib.h>
#include <lzma.h>
#include <lz4frame.h>
#include <brotli/encode.h>
#include <brotli/decode.h>
#include <libdeflate.h>

struct pakr_codec {
	int format;
	int encode;
	int started;
	int initialized;
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
};

static int fail(pakr_codec *codec, const char *message) {
	snprintf(codec->error, sizeof(codec->error), "%s", message);
	return -1;
}

pakr_codec *pakr_codec_new(int format, int encode, int level, int threads) {
	pakr_codec *codec = calloc(1, sizeof(*codec));
	if (!codec) {
		return NULL;
	}
	codec->format = format;
	codec->encode = encode;
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
			static const int levels[] = {0, 1, 2, 3, 5, 7, 9, 12, 16, 19};
			if (ZSTD_isError(ZSTD_CCtx_setParameter(codec->zstd_encoder, ZSTD_c_compressionLevel, levels[level])) || ZSTD_isError(ZSTD_CCtx_setParameter(codec->zstd_encoder, ZSTD_c_checksumFlag, 1)) || ZSTD_isError(ZSTD_CCtx_setParameter(codec->zstd_encoder, ZSTD_c_nbWorkers, threads > 1 ? threads : 0))) {
				result = -1;
			}
		} else {
			codec->zstd_decoder = ZSTD_createDCtx();
			result = codec->zstd_decoder ? 0 : -1;
		}
		break;
	case 3:
		result = encode ? BZ2_bzCompressInit(&codec->bzip, level, 0, 30) : BZ2_bzDecompressInit(&codec->bzip, 0, 0);
		codec->initialized = result == BZ_OK;
		break;
	case 4: {
		if (encode) {
			lzma_mt options = {0};
			options.threads = threads;
			options.preset = level;
			options.check = LZMA_CHECK_CRC64;
			result = lzma_stream_encoder_mt(&codec->xz, &options);
		} else {
			lzma_mt options = {0};
			options.threads = threads;
			options.flags = LZMA_CONCATENATED;
			options.memlimit_threading = UINT64_C(512) << 20;
			options.memlimit_stop = UINT64_C(2) << 30;
			result = lzma_stream_decoder_mt(&codec->xz, &options);
		}
		codec->initialized = result == LZMA_OK;
		break;
	}
	case 5:
		if (encode) {
			result = LZ4F_isError(LZ4F_createCompressionContext(&codec->lz4_encoder, LZ4F_VERSION));
			codec->lz4_preferences.frameInfo.contentChecksumFlag = LZ4F_contentChecksumEnabled;
			codec->lz4_preferences.frameInfo.blockSizeID = LZ4F_max256KB;
			codec->lz4_preferences.compressionLevel = level <= 3 ? 0 : level + 3;
		} else {
			result = LZ4F_isError(LZ4F_createDecompressionContext(&codec->lz4_decoder, LZ4F_VERSION));
		}
		break;
	case 6:
		if (encode) {
			codec->brotli_encoder = BrotliEncoderCreateInstance(NULL, NULL, NULL);
			result = codec->brotli_encoder && BrotliEncoderSetParameter(codec->brotli_encoder, BROTLI_PARAM_QUALITY, level == 9 ? 11 : level) ? 0 : -1;
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
	return codec->error;
}

int pakr_codec_step(pakr_codec *codec, const void *input, size_t *input_size, void *output, size_t *output_size, int finish) {
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
	case 4: {
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
		else if (result != LZMA_OK && result != LZMA_BUF_ERROR) { status = fail(codec, "invalid xz stream or memory limit exceeded"); }
		break;
	}
	case 5: {
		size_t result;
		if (!codec->encode) {
			result = LZ4F_decompress(codec->lz4_decoder, output, output_size, input, input_size, NULL);
			if (result == 0) { status = 1; }
		} else if (!codec->started) {
			result = LZ4F_compressBegin(codec->lz4_encoder, output, available_output, &codec->lz4_preferences);
			*input_size = 0;
			*output_size = result;
			codec->started = 1;
		} else if (available_input != 0) {
			result = LZ4F_compressUpdate(codec->lz4_encoder, output, available_output, input, available_input, NULL);
			*output_size = result;
		} else if (finish) {
			result = LZ4F_compressEnd(codec->lz4_encoder, output, available_output, NULL);
			*output_size = result;
			status = 1;
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
	if (codec->format == 4) { lzma_end(&codec->xz); }
	ZSTD_freeCCtx(codec->zstd_encoder);
	ZSTD_freeDCtx(codec->zstd_decoder);
	LZ4F_freeCompressionContext(codec->lz4_encoder);
	LZ4F_freeDecompressionContext(codec->lz4_decoder);
	BrotliEncoderDestroyInstance(codec->brotli_encoder);
	BrotliDecoderDestroyInstance(codec->brotli_decoder);
	free(codec);
}

size_t pakr_deflate_bound(size_t size) {
	return libdeflate_deflate_compress_bound(NULL, size);
}

size_t pakr_deflate(const void *input, size_t size, void *output, size_t capacity, int level) {
	struct libdeflate_compressor *compressor = libdeflate_alloc_compressor(level);
	if (!compressor) { return 0; }
	size_t written = libdeflate_deflate_compress(compressor, input, size, output, capacity);
	libdeflate_free_compressor(compressor);
	return written;
}

int pakr_inflate(const void *input, size_t size, void *output, size_t capacity) {
	struct libdeflate_decompressor *decompressor = libdeflate_alloc_decompressor();
	if (!decompressor) { return -1; }
	int result = libdeflate_deflate_decompress(decompressor, input, size, output, capacity, NULL);
	libdeflate_free_decompressor(decompressor);
	return result;
}
