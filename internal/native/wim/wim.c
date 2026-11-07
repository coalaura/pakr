#include "config.h"
#include "../wim.h"
#include "wimlib.h"
#include "wimlib/blob_table.h"
#include "wimlib/dentry.h"
#include "wimlib/inode.h"
#include "wimlib/metadata.h"
#include "wimlib/resource.h"
#include "wimlib/tagged_items.h"
#include "wimlib/wim.h"
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
#endif

extern int goWIMProgress(uintptr_t handle, uint64_t completed, unsigned threads, char *path, int scan);
extern int goWIMEntry(uintptr_t handle, pakr_wim_entry *entry);
extern void goWIMUnsupported(uintptr_t handle, char *path, char *reason);
extern int goWIMBlob(uintptr_t handle, unsigned char *hash, uint64_t offset, void *data, size_t size, int event);

struct tree_context {
	WIMStruct *wim;
	uintptr_t handle;
	uint64_t metadata_limit;
};

static wimlib_tchar *native_string(const char *text)
{
#ifdef _WIN32
	int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, text, -1, NULL, 0);
	if (!length) return NULL;
	wchar_t *result = malloc((size_t)length * sizeof(wchar_t));
	if (result) MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, text, -1, result, length);
	return result;
#else
	return strdup(text);
#endif
}

static char *utf8_string(const wimlib_tchar *text)
{
	if (!text) return strdup("");
#ifdef _WIN32
	int length = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, text, -1, NULL, 0, NULL, NULL);
	if (!length) return NULL;
	char *result = malloc((size_t)length);
	if (result) WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, text, -1, result, length, NULL, NULL);
	return result;
#else
	return strdup(text);
#endif
}

static enum wimlib_progress_status progress(enum wimlib_progress_msg message, union wimlib_progress_info *info, void *context)
{
	uintptr_t handle = (uintptr_t)context;
	int status;
	if (message == WIMLIB_PROGRESS_MSG_TEST_FILE_EXCLUSION) {
		char *path = utf8_string(info->test_file_exclusion.path);
		if (!path) return WIMLIB_PROGRESS_STATUS_ABORT;
		status = goWIMProgress(handle, 0, 0, path, 1);
		free(path);
		if (status == 2) {
			info->test_file_exclusion.will_exclude = true;
			return WIMLIB_PROGRESS_STATUS_CONTINUE;
		}
	} else if (message == WIMLIB_PROGRESS_MSG_WRITE_STREAMS) {
		status = goWIMProgress(handle, info->write_streams.completed_bytes, info->write_streams.num_threads, NULL, 0);
	} else {
		status = goWIMProgress(handle, 0, 0, NULL, 0);
	}
	return status ? WIMLIB_PROGRESS_STATUS_ABORT : WIMLIB_PROGRESS_STATUS_CONTINUE;
}

static int unsupported_entry(struct tree_context *context, const struct wimlib_dir_entry *entry, char *reason)
{
	char *path = utf8_string(entry->full_path);
	if (!path) return WIMLIB_ERR_NOMEM;
	goWIMUnsupported(context->handle, path, reason);
	free(path);
	return WIMLIB_ERR_UNSUPPORTED;
}

static int resource_budget(const struct blob_descriptor *blob, uint64_t limit)
{
	if (!blob || blob->blob_location != BLOB_IN_WIM) return 0;
	const struct wim_resource_descriptor *resource = blob->rdesc;
	if (resource->chunk_size > 64 * 1024 * 1024) return WIMLIB_ERR_UNSUPPORTED;
	/* The decoder allocates a chunk-offset table, including for solid resources. */
	if (resource->chunk_size && resource->uncompressed_size / resource->chunk_size >= limit / 16) return WIMLIB_ERR_NOMEM;
	return 0;
}

static int tree_entry(const struct wimlib_dir_entry *entry, void *opaque)
{
	struct tree_context *context = opaque;
	struct wim_dentry *dentry = get_dentry(context->wim, entry->full_path, WIMLIB_CASE_SENSITIVE);
	if (!dentry) return WIMLIB_ERR_INVALID_METADATA_RESOURCE;
	struct wim_inode *inode = dentry->d_inode;
	/* The public iterator omits some non-default streams. Inspect those too. */
	for (unsigned index = 0; index < inode->i_num_streams; index++) {
		struct wim_inode_stream *stream = &inode->i_streams[index];
		int status = resource_budget(stream_blob(stream, context->wim->blob_table), context->metadata_limit);
		if (status) return status;
		if (stream_is_named(stream)) return unsupported_entry(context, entry, "named stream");
		/* A zero main_hash is a placeholder when reparse data uses an extra stream. */
		if (index == 0 && stream->stream_type == STREAM_TYPE_UNKNOWN && is_zero_hash(stream_hash(stream))) continue;
		if (stream->stream_type != STREAM_TYPE_DATA && stream->stream_type != STREAM_TYPE_REPARSE_POINT) return unsupported_entry(context, entry, "unknown stream type");
		if (stream->stream_type == STREAM_TYPE_DATA && (entry->attributes & (0x10 | 0x400)) && stream_blob(stream, context->wim->blob_table)) return unsupported_entry(context, entry, "data on directory or reparse point");
		if (stream->stream_type == STREAM_TYPE_REPARSE_POINT && !(entry->attributes & 0x400)) return unsupported_entry(context, entry, "reparse stream on ordinary file");
	}
	if (inode_get_tagged_item(inode, TAG_XATTRS, 0, NULL) || inode_get_tagged_item(inode, TAG_WIMLIB_LINUX_XATTRS, 0, NULL)) return unsupported_entry(context, entry, "extended attributes");
	if (entry->num_named_streams) return unsupported_entry(context, entry, "alternate data stream");
	/* Security descriptors, object IDs and DOS aliases carry no separate contents.
	 * Like other file archives, extraction restores neither ACLs nor ownership. */
	char *path = utf8_string(entry->full_path);
	char *name = utf8_string(entry->filename);
	if (!path || !name) {
		free(path);
		free(name);
		return WIMLIB_ERR_NOMEM;
	}
	pakr_wim_entry result = {
		.path = path,
		.name = name,
		.size = entry->streams[0].resource.uncompressed_size,
		.group = entry->hard_link_group_id,
		.modified = entry->last_write_time.tv_sec,
		.nanos = entry->last_write_time.tv_nsec,
		.mode = entry->unix_mode,
		.attributes = entry->attributes,
		.reparse = entry->reparse_tag,
	};
	memcpy(result.hash, entry->streams[0].resource.sha1_hash, 20);
	int status = goWIMEntry(context->handle, &result);
	free(path);
	free(name);
	return status ? WIMLIB_ERR_ABORTED_BY_PROGRESS : 0;
}

static int begin_blob(struct blob_descriptor *blob, void *context)
{
	return goWIMBlob((uintptr_t)context, blob->hash, 0, NULL, 0, 0) ? WIMLIB_ERR_ABORTED_BY_PROGRESS : 0;
}

static int continue_blob(const struct blob_descriptor *blob, uint64_t offset, const void *data, size_t size, void *context)
{
	return goWIMBlob((uintptr_t)context, (unsigned char *)blob->hash, offset, (void *)data, size, 1) ? WIMLIB_ERR_ABORTED_BY_PROGRESS : 0;
}

static int end_blob(struct blob_descriptor *blob, int status, void *context)
{
	int result = goWIMBlob((uintptr_t)context, blob->hash, 0, NULL, 0, 2);
	return status ? status : (result ? WIMLIB_ERR_ABORTED_BY_PROGRESS : 0);
}

int pakr_wim_init(void)
{
	return wimlib_global_init(WIMLIB_INIT_FLAG_ASSUME_UTF8 | WIMLIB_INIT_FLAG_DONT_ACQUIRE_PRIVILEGES | WIMLIB_INIT_FLAG_DEFAULT_CASE_SENSITIVE);
}

int pakr_wim_create(int codec, unsigned level, WIMStruct **result, uintptr_t handle)
{
	int status = wimlib_set_default_compression_level(WIMLIB_COMPRESSION_TYPE_LZX, 50);
	if (!status) status = wimlib_set_default_compression_level(codec, level);
	if (status) return status;
	status = wimlib_create_new_wim(codec == WIMLIB_COMPRESSION_TYPE_LZMS ? WIMLIB_COMPRESSION_TYPE_LZX : codec, result);
	if (!status) wimlib_register_progress_function(*result, progress, (void *)handle);
	return status;
}

int pakr_wim_open(const char *path, WIMStruct **result, uintptr_t handle)
{
	wimlib_tchar *native = native_string(path);
	if (!native) return WIMLIB_ERR_NOMEM;
	int status = wimlib_open_wim_with_progress(native, WIMLIB_OPEN_FLAG_CHECK_INTEGRITY, result, progress, (void *)handle);
	free(native);
	return status;
}

int pakr_wim_capture(WIMStruct *wim, pakr_wim_source *sources, size_t count)
{
	struct wimlib_capture_source *native = calloc(count, sizeof(*native));
	if (!native) return WIMLIB_ERR_NOMEM;
	int status = 0;
	for (size_t index = 0; index < count; index++) {
		native[index].fs_source_path = native_string(sources[index].source);
		native[index].wim_target_path = native_string(sources[index].target);
		if (!native[index].fs_source_path || !native[index].wim_target_path) status = WIMLIB_ERR_NOMEM;
	}
	int flags = WIMLIB_ADD_FLAG_NO_ACLS | WIMLIB_ADD_FLAG_NORPFIX | WIMLIB_ADD_FLAG_NO_REPLACE | WIMLIB_ADD_FLAG_NO_UNSUPPORTED_EXCLUDE | WIMLIB_ADD_FLAG_TEST_FILE_EXCLUSION;
#ifndef _WIN32
	flags |= WIMLIB_ADD_FLAG_UNIX_DATA;
#endif
	if (!status) status = wimlib_add_image_multisource(wim, native, count, WIMLIB_WIM_ROOT_PATH, NULL, flags);
	for (size_t index = 0; index < count; index++) {
		free(native[index].fs_source_path);
		free(native[index].wim_target_path);
	}
	free(native);
	return status;
}

int pakr_wim_write(WIMStruct *wim, const char *path, int solid, unsigned threads)
{
	int status = wimlib_set_output_chunk_size(wim, 32768);
	if (!status && solid) status = wimlib_set_output_pack_compression_type(wim, WIMLIB_COMPRESSION_TYPE_LZMS);
	if (!status && solid) status = wimlib_set_output_pack_chunk_size(wim, 64 * 1024 * 1024);
	if (status) return status;
	wimlib_tchar *native = native_string(path);
	if (!native) return WIMLIB_ERR_NOMEM;
	int flags = WIMLIB_WRITE_FLAG_CHECK_INTEGRITY;
	if (solid) flags |= WIMLIB_WRITE_FLAG_SOLID;
	status = wimlib_write(wim, native, 1, flags, threads);
	free(native);
	return status;
}

int pakr_wim_tree(WIMStruct *wim, int image, uint64_t metadata_limit, uintptr_t handle)
{
	if (image < 1 || image > wim->hdr.image_count || !wim->image_metadata) return WIMLIB_ERR_INVALID_IMAGE;
	struct blob_descriptor *metadata = wim->image_metadata[image - 1]->metadata_blob;
	if (metadata->blob_location == BLOB_IN_WIM && metadata->size > metadata_limit) return WIMLIB_ERR_NOMEM;
	int status = resource_budget(metadata, metadata_limit);
	if (status) return status;
	struct tree_context context = {
		.wim = wim,
		.handle = handle,
		.metadata_limit = metadata_limit,
	};
	return wimlib_iterate_dir_tree(wim, image, WIMLIB_WIM_ROOT_PATH, WIMLIB_ITERATE_DIR_TREE_FLAG_RECURSIVE | WIMLIB_ITERATE_DIR_TREE_FLAG_RESOURCES_NEEDED, tree_entry, &context);
}

int pakr_wim_read(WIMStruct *wim, const unsigned char *hashes, size_t count, uintptr_t handle)
{
	LIST_HEAD(blobs);
	for (size_t index = 0; index < count; index++) {
		struct blob_descriptor *blob = lookup_blob(wim->blob_table, hashes + index * 20);
		if (!blob || blob->blob_location != BLOB_IN_WIM || blob->is_metadata) return WIMLIB_ERR_RESOURCE_NOT_FOUND;
		if (blob->rdesc->chunk_size > 64 * 1024 * 1024) return WIMLIB_ERR_UNSUPPORTED;
		list_add_tail(&blob->extraction_list, &blobs);
	}
	struct read_blob_callbacks callbacks = {
		.begin_blob = begin_blob,
		.continue_blob = continue_blob,
		.end_blob = end_blob,
		.ctx = (void *)handle,
	};
	return read_blob_list(&blobs, offsetof(struct blob_descriptor, extraction_list), &callbacks, VERIFY_BLOB_HASHES);
}

unsigned pakr_wim_images(WIMStruct *wim)
{
	return wim->hdr.image_count;
}

void pakr_wim_free(WIMStruct *wim)
{
	wimlib_free(wim);
}

uint64_t pakr_wim_memory(int codec, unsigned chunk, unsigned level)
{
	return wimlib_get_compressor_needed_memory(codec, chunk, level);
}

char *pakr_wim_error(int status)
{
	return utf8_string(wimlib_get_error_string(status));
}
