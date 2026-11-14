#undef NDEBUG
#include <assert.h>
#include <lzma.h>
#include <stdlib.h>
#include <string.h>

static size_t allocations;
static size_t live;
static size_t fail_at;
static int cancel_during_decode;
static void *active_parallel;

static lzma_ret decode(lzma_stream *stream, lzma_action action);

static void *allocate(void *opaque, size_t count, size_t size)
{
	(void)opaque;
	if (++allocations == fail_at) return NULL;
	void *result = malloc(count * size);
	if (result) ++live;
	return result;
}

static void release(void *opaque, void *pointer)
{
	(void)opaque;
	if (pointer) --live;
	free(pointer);
}

static lzma_ret initialize(lzma_stream *stream, const lzma_filter *filters)
{
	static const lzma_allocator allocator = {allocate, release, NULL};
	stream->allocator = &allocator;
	return lzma_raw_decoder(stream, filters);
}

#define lzma_raw_decoder initialize
#define lzma_code decode
#include "fl2_decompress.c"
#undef lzma_raw_decoder
#undef lzma_code

static lzma_ret decode(lzma_stream *stream, lzma_action action)
{
	lzma_ret result = lzma_code(stream, action);
	if (cancel_during_decode) ((FL2_decMt *)active_parallel)->canceled = 1;
	return result;
}

static void verify(const unsigned char *input, size_t size, size_t expected, int final, int success, size_t split, size_t start)
{
	FL2_decMt parallel = {0};
	FL2_DStream stream = {0};
	unsigned char output[4097];
	FL2_decInbuf *first = malloc(sizeof(*first) + size + start + LZMA_REQUIRED_INPUT_MAX);
	FL2_decInbuf *last = malloc(sizeof(*last) + size + start + LZMA_REQUIRED_INPUT_MAX);
	assert(first && last);
	memset(first->inBuf, 0xff, start);
	memcpy(first->inBuf + start, input, size);
	first->length = start + size;
	first->next = NULL;
	parallel.numThreads = 1;
	parallel.isFinal = final;
	parallel.threads[0].outBuf = output;
	parallel.threads[0].bufSize = expected;
	parallel.threads[0].inBlock.first = first;
	parallel.threads[0].inBlock.last = first;
	parallel.threads[0].inBlock.startPos = start;
	parallel.threads[0].inBlock.endPos = start + size;
	if (split) {
		first->length = split + LZMA_REQUIRED_INPUT_MAX;
		last->length = start + size - split;
		last->next = NULL;
		memcpy(last->inBuf, first->inBuf + split, last->length);
		first->next = last;
		parallel.threads[0].inBlock.last = last;
		parallel.threads[0].inBlock.endPos = last->length;
	}
	stream.decmt = &parallel;
	active_parallel = &parallel;
	size_t result = FL2_decompressBlockMt(&stream, 0);
	assert((!FL2_isError(result)) == success);
	if (cancel_during_decode) assert(FL2_getErrorCode(result) == FL2_error_canceled);
	assert(live == 0);
	if (success) {
		for (size_t index = 0; index < expected; ++index) assert(output[index] == (unsigned char)index);
	}
	free(first);
	free(last);
}

int main(void)
{
	unsigned char input[4096];
	unsigned char compressed[8192];
	for (size_t index = 0; index < sizeof(input); ++index) input[index] = (unsigned char)index;
	lzma_options_lzma options;
	assert(!lzma_lzma_preset(&options, 3));
	options.dict_size = 4096;
	lzma_filter filters[] = {
		{LZMA_FILTER_LZMA2, &options},
		{LZMA_VLI_UNKNOWN, NULL}
	};
	size_t size = 0;
	assert(lzma_raw_buffer_encode(filters, NULL, input, sizeof(input), compressed, &size, sizeof(compressed)) == LZMA_OK);
	assert(compressed[size - 1] == 0);
	verify(compressed, size, sizeof(input), 1, 1, 0, 0);
	verify(compressed, size - 1, sizeof(input), 0, 1, 0, 0);
	verify(compressed, size - 1, sizeof(input), 1, 0, 0, 0);
	verify(compressed, size, sizeof(input), 0, 0, 0, 0);
	verify(compressed, size, sizeof(input) - 1, 1, 0, 0, 0);
	verify(compressed, size, sizeof(input) + 1, 1, 0, 0, 0);
	compressed[size] = 0;
	verify(compressed, size + 1, sizeof(input), 1, 0, 0, 0);
	for (size_t length = 0; length + 1 < size; ++length) verify(compressed, length, sizeof(input), 0, 0, 0, 0);
	for (size_t split = 1; split + LZMA_REQUIRED_INPUT_MAX < size; ++split) {
		verify(compressed, size, sizeof(input), 1, 1, split, 0);
		verify(compressed, size - 1, sizeof(input), 0, 1, split, 0);
	}
	verify(compressed, size, sizeof(input), 1, 1, 1, LZMA_REQUIRED_INPUT_MAX);
	compressed[5] = 255;
	verify(compressed, size, sizeof(input), 1, 0, 0, 0);
	compressed[5] = 0x5d;
	allocations = 0;
	verify(compressed, size, sizeof(input), 1, 1, 0, 0);
	size_t count = allocations;
	for (fail_at = 1; fail_at <= count; ++fail_at) {
		allocations = 0;
		verify(compressed, size, sizeof(input), 1, 0, 0, 0);
	}
	fail_at = 0;
	cancel_during_decode = 1;
	verify(compressed, size, sizeof(input), 1, 0, 1, 0);
	return 0;
}
