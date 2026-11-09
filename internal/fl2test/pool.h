#include "fl2_pool.c"
#include "fl2_compress_internal.h"

/* Compile the production pool source into this test only. Keeping its layout
 * visible lets us defer worker creation without a scheduler-dependent sleep. */
static FL2POOL_ctx *deferred_pool(void) {
	FL2POOL_ctx *pool = calloc(1, sizeof(*pool));
	if (!pool) return NULL;
	FL2_pthread_mutex_init(&pool->queueMutex, NULL);
	FL2_pthread_cond_init(&pool->busyCond, NULL);
	FL2_pthread_cond_init(&pool->newJobsCond, NULL);
	return pool;
}

static int start_worker(FL2POOL_ctx *pool) {
	if (FL2_pthread_create(&pool->threads[0], NULL, FL2POOL_thread, pool)) return 1;
	pool->numThreads = 1;
	return 0;
}

struct job_gate {
	FL2_pthread_mutex_t mutex;
	FL2_pthread_cond_t condition;
	int started;
	int released;
	int completed;
};

static void gated_job(void *opaque, ptrdiff_t index) {
	struct job_gate *gate = opaque;
	(void)index;
	FL2_pthread_mutex_lock(&gate->mutex);
	gate->started = 1;
	FL2_pthread_cond_signal(&gate->condition);
	while (!gate->released) FL2_pthread_cond_wait(&gate->condition, &gate->mutex);
	++gate->completed;
	FL2_pthread_mutex_unlock(&gate->mutex);
}

static const char *queued_timeout(void) {
	FL2POOL_ctx *pool = deferred_pool();
	if (!pool) return "pool allocation failed";
	struct job_gate gate = {0};
	FL2_pthread_mutex_init(&gate.mutex, NULL);
	FL2_pthread_cond_init(&gate.condition, NULL);
	const char *failure = NULL;
	if (FL2POOL_waitAll(NULL, 20) || FL2POOL_waitAll(pool, 20)) failure = "idle pool reported pending work";
	FL2POOL_addRange(pool, gated_job, &gate, 0, 1);
	/* No worker exists until after both timeouts: the queue cannot be consumed. */
	if (!FL2POOL_waitAll(pool, 20) || !FL2POOL_waitAll(pool, 20)) failure = "queued work reported complete before worker startup";
	if (start_worker(pool)) {
		FL2POOL_free(pool);
		FL2_pthread_cond_destroy(&gate.condition);
		FL2_pthread_mutex_destroy(&gate.mutex);
		return "worker creation failed";
	}
	FL2_pthread_mutex_lock(&gate.mutex);
	while (!gate.started) FL2_pthread_cond_wait(&gate.condition, &gate.mutex);
	FL2_pthread_mutex_unlock(&gate.mutex);
	if (!FL2POOL_waitAll(pool, 20)) failure = "running work reported complete";
	FL2_pthread_mutex_lock(&gate.mutex);
	gate.released = 1;
	FL2_pthread_cond_signal(&gate.condition);
	FL2_pthread_mutex_unlock(&gate.mutex);
	if (FL2POOL_waitAll(pool, 0) || gate.completed != 1) failure = "untimed wait did not finish running work";
	FL2POOL_addRange(pool, gated_job, &gate, 0, 3);
	if (FL2POOL_waitAll(pool, 0) || gate.completed != 4) failure = "untimed wait did not drain all jobs";
	if (FL2POOL_waitAll(pool, 20)) failure = "completed pool reported pending work";
	FL2POOL_free(pool);
	FL2_pthread_cond_destroy(&gate.condition);
	FL2_pthread_mutex_destroy(&gate.mutex);
	return failure;
}

static const char *queued_finalization(void) {
	FL2_CStream *stream = FL2_createCStreamMt(2, 0);
	if (!stream) return "stream allocation failed";
	FL2_CCtx_setParameter(stream, FL2_p_compressionLevel, 1);
	FL2_CCtx_setParameter(stream, FL2_p_doXXHash, 0);
	FL2_CCtx_setParameter(stream, FL2_p_omitProperties, 1);
	FL2_initCStream(stream, 0);
	FL2_setCStreamTimeout(stream, 20);
	FL2POOL_free(stream->compressThread);
	stream->compressThread = deferred_pool();
	if (!stream->compressThread) {
		FL2_freeCStream(stream);
		return "pool allocation failed";
	}
	unsigned char payload[23000];
	unsigned char encoded[24000];
	memset(payload, 'x', sizeof(payload));
	FL2_inBuffer input = {payload, sizeof(payload), 0};
	FL2_outBuffer output = {encoded, sizeof(encoded), 0};
	const char *failure = NULL;
	size_t result = FL2_compressStream(stream, &output, &input);
	if (FL2_isError(result) || input.pos != sizeof(payload)) failure = "small input was not buffered";
	result = FL2_endStream(stream, &output);
	if (result == 0 && output.pos == 1 && encoded[0] == 0) failure = "finalization returned success with only an LZMA2 end marker for 23000 input bytes";
	else if (!FL2_isTimedOut(result) || output.pos != 0) failure = "finalization completed before queued compression started";
	if (start_worker(stream->compressThread)) {
		FL2POOL_free(stream->compressThread);
		stream->compressThread = NULL;
		FL2_freeCStream(stream);
		return "worker creation failed";
	}
	/* On the buggy source finalization has already reset the frame. Drain the
 * delayed job before freeing it, but do not continue that invalid stream. */
	if (!failure) {
		do {
			result = FL2_endStream(stream, &output);
		} while (FL2_isTimedOut(result) || result == 1);
		if (FL2_isError(result) || output.pos <= 1 || encoded[output.pos - 1] != 0) failure = "resumed finalization lost compressed output";
	}
	FL2_cancelCStream(stream);
	FL2_freeCStream(stream);
	return failure;
}
