#include "bsc.h"

#include <stdlib.h>
#include <string.h>

#define PAKR_BSC_BUFFERS 16

typedef struct {
	void *data;
	size_t capacity;
	int used;
} pakr_bsc_buffer;

struct pakr_bsc_workspace {
	pakr_bsc_buffer buffers[PAKR_BSC_BUFFERS];
	size_t capacity;
	size_t limit;
};

// A workspace belongs to a Go slot, not an OS thread. It is active only during
// one synchronous native call; libbsc's internal threading is disabled.
static _Thread_local pakr_bsc_workspace *pakr_bsc_active;

static void *pakr_bsc_allocate(size_t size) {
	pakr_bsc_workspace *workspace = pakr_bsc_active;
	if (workspace == NULL) {
		return malloc(size);
	}

	pakr_bsc_buffer *best = NULL;
	pakr_bsc_buffer *replace = NULL;
	for (int index = 0; index < PAKR_BSC_BUFFERS; index++) {
		pakr_bsc_buffer *buffer = &workspace->buffers[index];
		if (buffer->used) {
			continue;
		}

		if (buffer->capacity >= size && (best == NULL || buffer->capacity < best->capacity)) {
			best = buffer;
		}
		if (replace == NULL || buffer->capacity > replace->capacity) {
			replace = buffer;
		}
	}

	if (best != NULL) {
		best->used = 1;
		return best->data;
	}
	if (replace == NULL || size > workspace->limit) {
		return NULL;
	}

	// Evict idle storage before growing, keeping retained and live scratch
	// together within the same per-worker memory allowance.
	free(replace->data);
	workspace->capacity -= replace->capacity;
	memset(replace, 0, sizeof(*replace));
	for (int index = 0; index < PAKR_BSC_BUFFERS && size > workspace->limit - workspace->capacity; index++) {
		pakr_bsc_buffer *buffer = &workspace->buffers[index];
		if (!buffer->used) {
			free(buffer->data);
			workspace->capacity -= buffer->capacity;
			memset(buffer, 0, sizeof(*buffer));
		}
	}
	if (size > workspace->limit - workspace->capacity) {
		return NULL;
	}

	replace->data = malloc(size);
	if (replace->data == NULL) {
		return NULL;
	}

	replace->capacity = size;
	replace->used = 1;
	workspace->capacity += size;
	return replace->data;
}

static void *pakr_bsc_zero_allocate(size_t size) {
	void *data = pakr_bsc_allocate(size);
	if (data != NULL) {
		memset(data, 0, size);
	}
	return data;
}

static void pakr_bsc_release(void *data) {
	if (data == NULL) {
		return;
	}

	pakr_bsc_workspace *workspace = pakr_bsc_active;
	if (workspace != NULL) {
		for (int index = 0; index < PAKR_BSC_BUFFERS; index++) {
			pakr_bsc_buffer *buffer = &workspace->buffers[index];
			if (buffer->data == data) {
				buffer->used = 0;
				return;
			}
		}
	}
	free(data);
}

int pakr_bsc_init(int features) {
	return bsc_init_full(features, pakr_bsc_allocate, pakr_bsc_zero_allocate, pakr_bsc_release);
}

pakr_bsc_workspace *pakr_bsc_create(size_t limit) {
	pakr_bsc_workspace *workspace = calloc(1, sizeof(*workspace));
	if (workspace != NULL) {
		workspace->limit = limit > sizeof(*workspace) ? limit - sizeof(*workspace) : 0;
	}
	return workspace;
}

void pakr_bsc_destroy(pakr_bsc_workspace *workspace) {
	if (workspace == NULL) {
		return;
	}
	for (int index = 0; index < PAKR_BSC_BUFFERS; index++) {
		free(workspace->buffers[index].data);
	}
	free(workspace);
}

int pakr_bsc_compress(pakr_bsc_workspace *workspace, const unsigned char *input, unsigned char *output, int size, int hash, int match, int coder, int features) {
	pakr_bsc_active = workspace;
	int result = bsc_compress(input, output, size, hash, match, LIBBSC_BLOCKSORTER_BWT, coder, features);
	pakr_bsc_active = NULL;
	return result;
}

int pakr_bsc_decompress(pakr_bsc_workspace *workspace, const unsigned char *input, int packed, unsigned char *output, int size, int features) {
	pakr_bsc_active = workspace;
	int result = bsc_decompress(input, packed, output, size, features);
	pakr_bsc_active = NULL;
	return result;
}
