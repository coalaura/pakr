#include <lzma.h>

static size_t FL2_decompressBlockMt(FL2_DStream *const stream, size_t const worker)
{
	FL2_decMt *const parallel = stream->decmt;
	FL2_decJob *const job = &parallel->threads[worker];
	const int final = parallel->isFinal && worker == parallel->numThreads - 1;
	lzma_stream decoder = LZMA_STREAM_INIT;
	lzma_options_lzma options = {0};
	options.dict_size = (uint32_t)LZMA2_getDictSizeFromProp(parallel->prop);
	lzma_filter filters[] = {
		{LZMA_FILTER_LZMA2, &options},
		{LZMA_VLI_UNKNOWN, NULL}
	};
	lzma_ret result = lzma_raw_decoder(&decoder, filters);
	if (result != LZMA_OK) {
		lzma_end(&decoder);
		return result == LZMA_MEM_ERROR ? FL2_ERROR(memory_allocation) : FL2_ERROR(corruption_detected);
	}

	/* Each job owns its decoder and dictionary until it returns. The pool owns
	 * the input chain and output buffer, and joins every job before freeing them. */
	decoder.next_out = job->outBuf;
	decoder.avail_out = job->bufSize;
	FL2_decInbuf *node = job->inBlock.first;
	size_t position = job->inBlock.startPos;
	int complete = 0;

	while (node && !parallel->canceled) {
		/* Overlap bytes belong to the next node and must be consumed only once. */
		if (node != job->inBlock.last && node->length < LZMA_REQUIRED_INPUT_MAX) {
			result = LZMA_DATA_ERROR;
			break;
		}
		size_t end = node == job->inBlock.last ? job->inBlock.endPos : node->length - LZMA_REQUIRED_INPUT_MAX;
		if (node != job->inBlock.last && position >= end && position <= node->length) {
			position -= end;
			node = node->next;
			continue;
		}
		if (end < position || end > node->length) {
			result = LZMA_DATA_ERROR;
			break;
		}

		decoder.next_in = node->inBuf + position;
		decoder.avail_in = end - position;
		while (decoder.avail_in && result == LZMA_OK && !parallel->canceled) {
			size_t before = decoder.avail_in;
			result = lzma_code(&decoder, LZMA_RUN);
			if (before == decoder.avail_in && result == LZMA_OK) {
				result = LZMA_DATA_ERROR;
			}
		}

		if (decoder.avail_in != 0) break;
		if (node == job->inBlock.last) {
			complete = 1;
			break;
		}
		if (result != LZMA_OK) break;

		node = node->next;
		position = 0;
	}

	/* Only an independently framed, non-final segment may receive a synthetic
	 * end marker. A real stream end must be present in the archive bytes. */
	int valid = complete && decoder.total_out == job->bufSize;
	if (valid && !final && result == LZMA_OK) {
		unsigned char terminator = 0;
		decoder.next_in = &terminator;
		decoder.avail_in = 1;
		result = lzma_code(&decoder, LZMA_RUN);
		valid = result == LZMA_STREAM_END && decoder.avail_in == 0 && decoder.total_out == job->bufSize;
	} else {
		valid = valid && final && result == LZMA_STREAM_END;
	}

	lzma_end(&decoder);
	if (parallel->canceled) return FL2_ERROR(canceled);
	if (!valid) return FL2_ERROR(corruption_detected);

	FL2_atomic_add(stream->progress, (long)job->bufSize);
	return FL2_error_no_error;
}
