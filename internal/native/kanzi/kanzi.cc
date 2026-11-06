#include "../kanzi.h"
#include "io/CompressedInputStream.hpp"
#include "io/CompressedOutputStream.hpp"
#include <algorithm>
#include <cstdio>
#include <memory>
#include <streambuf>

extern "C" int64_t pakrKanziRead(uintptr_t handle, void *data, size_t size);
extern "C" int64_t pakrKanziWrite(uintptr_t handle, void *data, size_t size);
extern "C" int pakrKanziCancelled(uintptr_t handle);

namespace {
class CallbackBuffer final : public std::streambuf {
	uintptr_t handle;
public:
	explicit CallbackBuffer(uintptr_t value) : handle(value) {}
	std::streamsize xsgetn(char *data, std::streamsize size) override {
		std::streamsize total = 0;
		while (total < size) {
			int64_t count = pakrKanziRead(handle, data + total, size_t(size - total));
			if (count < 0) throw std::runtime_error("Kanzi input failed");
			if (count == 0) break;
			total += count;
		}
		return total;
	}
	std::streamsize xsputn(const char *data, std::streamsize size) override {
		int64_t count = pakrKanziWrite(handle, const_cast<char *>(data), size_t(size));
		if (count != size) throw std::runtime_error("Kanzi output failed");
		return size;
	}
};

class Cancellation final : public kanzi::Listener<kanzi::Event> {
	uintptr_t handle;
public:
	explicit Cancellation(uintptr_t value) : handle(value) {}
	void processEvent(const kanzi::Event&) override {
		if (pakrKanziCancelled(handle)) throw std::runtime_error("Kanzi operation cancelled");
	}
};

class HeaderBuffer final : public std::streambuf {
public:
	HeaderBuffer(const void *data, size_t size) {
		char *begin = const_cast<char *>(static_cast<const char *>(data));
		setg(begin, begin, begin + size);
	}
};

class HeaderProbe final : public kanzi::CompressedInputStream, public kanzi::Listener<kanzi::Event> {
	pakr_knz_info *info;
public:
	HeaderProbe(std::istream& input, pakr_knz_info *value) : CompressedInputStream(input), info(value) {
		addListener(*this);
	}
	void probe() { readHeader(); }
	void processEvent(const kanzi::Event& event) override {
		const auto *header = event.getInfo();
		if (header == nullptr) return;
		if (header->bsVersion != 7) throw std::runtime_error("Unsupported Kanzi bitstream version (expected 7)");
		info->block_size = header->blockSize;
		info->original_size = header->originalSize;
		std::snprintf(info->entropy, sizeof(info->entropy), "%s", header->entropyType.c_str());
		std::snprintf(info->transform, sizeof(info->transform), "%s", header->transformType.c_str());
	}
};

void report(char *error, const char *message) {
	std::snprintf(error, 256, "%s", message);
}
}

struct pakr_knz {
	CallbackBuffer buffer;
	std::istream input;
	std::ostream output;
	Cancellation cancellation;
	std::unique_ptr<kanzi::CompressedInputStream> decoder;
	std::unique_ptr<kanzi::CompressedOutputStream> encoder;
	explicit pakr_knz(uintptr_t handle) : buffer(handle), input(&buffer), output(&buffer), cancellation(handle) {
		input.exceptions(std::ios::badbit);
		output.exceptions(std::ios::badbit | std::ios::failbit);
	}
};

extern "C" int pakr_knz_probe(const void *data, size_t size, pakr_knz_info *info, char *error) {
	try {
		HeaderBuffer buffer(data, size);
		std::istream input(&buffer);
		HeaderProbe probe(input, info);
		probe.probe();
		return 0;
	} catch (const std::exception& failure) { report(error, failure.what()); }
	catch (...) { report(error, "Unknown Kanzi header error"); }
	return -1;
}

extern "C" pakr_knz *pakr_knz_open(uintptr_t handle, int encode, int jobs, const pakr_knz_info *info, char *error) {
	try {
		auto stream = std::make_unique<pakr_knz>(handle);
		if (encode) {
			uint64_t size = info->original_size > 0 ? uint64_t(info->original_size) : 0;
			stream->encoder = std::make_unique<kanzi::CompressedOutputStream>(stream->output, jobs, info->entropy, info->transform, info->block_size, 64, size);
			stream->encoder->addListener(stream->cancellation);
		} else {
			stream->decoder = std::make_unique<kanzi::CompressedInputStream>(stream->input, jobs);
			stream->decoder->addListener(stream->cancellation);
		}
		return stream.release();
	} catch (const std::exception& failure) { report(error, failure.what()); }
	catch (...) { report(error, "Unknown Kanzi initialization error"); }
	return nullptr;
}

extern "C" int pakr_knz_write(pakr_knz *stream, const void *data, size_t size, char *error) {
	try {
		stream->encoder->write(static_cast<const char *>(data), std::streamsize(size));
		if (!*stream->encoder) throw std::runtime_error("Kanzi write failed");
		return 0;
	} catch (const std::exception& failure) { report(error, failure.what()); }
	catch (...) { report(error, "Unknown Kanzi encoding error"); }
	return -1;
}

extern "C" int64_t pakr_knz_read(pakr_knz *stream, void *data, size_t size, char *error) {
	try {
		stream->decoder->read(static_cast<char *>(data), std::streamsize(size));
		if (stream->decoder->bad()) throw std::runtime_error("Kanzi read failed");
		return stream->decoder->gcount();
	} catch (const std::exception& failure) { report(error, failure.what()); }
	catch (...) { report(error, "Unknown Kanzi decoding error"); }
	return -1;
}

extern "C" uint64_t pakr_knz_consumed(pakr_knz *stream) {
	return stream->decoder->getRead();
}

extern "C" int pakr_knz_close(pakr_knz *stream, char *error) {
	std::unique_ptr<pakr_knz> owner(stream);
	try {
		if (stream->encoder) stream->encoder->close();
		if (stream->decoder) stream->decoder->close();
		return 0;
	} catch (const std::exception& failure) { report(error, failure.what()); }
	catch (...) { report(error, "Unknown Kanzi close error"); }
	return -1;
}
