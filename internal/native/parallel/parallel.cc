#include "parallel.h"
#include <cstdio>
#include <memory>
#include <fcntl.h>
#include <filereader/Standard.hpp>
#include <rapidgzip/ParallelGzipReader.hpp>
#include <indexed_bzip2/ParallelBZ2Reader.hpp>
#ifdef _WIN32
#include <windows.h>
#include <io.h>
#endif

extern "C" void *pakr_parallel_open(const char *path, int format, int threads, char *error, size_t capacity) {
	try {
		UniqueFileReader source;
#ifdef _WIN32
		int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, nullptr, 0);
		if (length == 0) { throw std::runtime_error("invalid UTF-8 path"); }
		std::wstring wide(length, L'\0');
		MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide.data(), length);
		int descriptor = _wopen(wide.c_str(), _O_RDONLY | _O_BINARY);
		if (descriptor < 0) { throw std::runtime_error("cannot open compressed input"); }
		try { source = std::make_unique<StandardFileReader>(descriptor); }
		catch (...) { _close(descriptor); throw; }
		_close(descriptor);
#else
		source = std::make_unique<StandardFileReader>(std::string(path));
#endif
		if (format == 1) {
			auto reader = std::make_unique<rapidgzip::ParallelGzipReader<>>(std::move(source), threads);
			reader->setCRC32Enabled(true);
			return static_cast<FileReader *>(reader.release());
		}
		auto reader = std::make_unique<ParallelBZ2Reader>(std::move(source), threads);
		return static_cast<FileReader *>(reader.release());
	} catch (const std::exception &exception) {
		std::snprintf(error, capacity, "%s", exception.what());
		return nullptr;
	}
}

extern "C" int64_t pakr_parallel_read(void *reader, void *output, size_t capacity, char *error, size_t error_capacity) {
	try {
		return static_cast<FileReader *>(reader)->read(static_cast<char *>(output), capacity);
	} catch (const std::exception &exception) {
		std::snprintf(error, error_capacity, "%s", exception.what());
		return -1;
	}
}

extern "C" void pakr_parallel_close(void *reader) {
	delete static_cast<FileReader *>(reader);
}
