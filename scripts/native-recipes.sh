#!/usr/bin/env bash
# Each recipe is fingerprinted separately. Keep component-specific inputs here.

recipe_zlib_ng() {
	cmake_options=(-DZLIB_COMPAT=ON -DZLIB_ENABLE_TESTS=OFF -DZLIBNG_ENABLE_TESTS=OFF)
	archives=(libz.a)
}

recipe_zstd() {
	cmake_source="$sources/zstd/build/cmake"
	cmake_options=(-DZSTD_BUILD_SHARED=OFF -DZSTD_BUILD_STATIC=ON -DZSTD_BUILD_PROGRAMS=OFF -DZSTD_BUILD_TESTS=OFF -DZSTD_MULTITHREAD_SUPPORT=ON)
	archives=(libzstd.a)
}

recipe_bzip2() {
	cmake_source="$root/scripts/bzip2"
	inputs=(scripts/bzip2/CMakeLists.txt)
	cmake_options=(-DSOURCE="$sources/bzip2")
	archives=(libbz2.a)
}

recipe_bzip3() {
	cmake_options=(-DBZIP3_BUILD_APPS=OFF -DBZIP3_ENABLE_PTHREAD=OFF -DBZIP3_ENABLE_ARCH_NATIVE=OFF)
	archives=(libbzip3.a)
}

recipe_libbsc() {
	cmake_options=(-DBSC_ENABLE_NATIVE_COMPILATION=OFF -DBSC_ENABLE_CPU_DISPATCH=ON -DBSC_ENABLE_OPENMP=OFF -DBSC_ENABLE_CUDA=OFF -DBSC_BUILD_SHARED_LIB=OFF)
	archives=(libbsc.a)
}

recipe_xz() {
	cmake_options=(-DXZ_NLS=OFF -DXZ_TOOL_XZ=OFF -DXZ_TOOL_XZDEC=OFF -DXZ_TOOL_LZMADEC=OFF -DXZ_TOOL_LZMAINFO=OFF -DXZ_DOC=OFF -DXZ_EXTERNAL_SHA256=OFF)
	archives=(liblzma.a)
}

recipe_fast_lzma2() {
	cmake_source="$root/scripts/fast-lzma2"
	inputs=(scripts/fast-lzma2/CMakeLists.txt)
	cmake_options=(-DSOURCE="$sources/fast-lzma2")
	archives=(libfast-lzma2.a)
}

recipe_lz4() {
	cmake_source="$sources/lz4/build/cmake"
	cmake_options=(-DLZ4_BUILD_CLI=OFF -DLZ4_BUILD_LEGACY_LZ4C=OFF -DLZ4_STATIC_MULTITHREAD=ON)
	archives=(liblz4.a)
}

recipe_brotli() {
	cmake_options=(-DBROTLI_DISABLE_TESTS=ON)
	archives=(libbrotlienc.a libbrotlidec.a libbrotlicommon.a)
}

recipe_libdeflate() {
	cmake_options=(-DLIBDEFLATE_BUILD_SHARED_LIB=OFF -DLIBDEFLATE_BUILD_GZIP=OFF)
	archives=(libdeflate.a)
}

recipe_nasm() {
	build_kind=nasm
	archives=()
}

recipe_isal() {
	build_kind=isal
	archives=(libisal.a)
	isal_arch=
	if [[ "$target" == windows ]]; then isal_arch=mingw; fi
	if [[ "$target" == darwin ]]; then isal_arch=Darwin; fi
	if [[ "$arch" == arm64 ]]; then
		isal_arch=noarch
	else
		dependencies=(nasm)
	fi
}

recipe_libarchive() {
	dependencies=(zlib-ng bzip2 xz zstd lz4)
	archives=(libarchive.a)
	cmake_options=(
		-DENABLE_TEST=OFF -DENABLE_TAR=OFF -DENABLE_CPIO=OFF -DENABLE_CAT=OFF -DENABLE_UNZIP=OFF
		-DENABLE_OPENSSL=OFF -DENABLE_MBEDTLS=OFF -DENABLE_NETTLE=OFF -DENABLE_CNG=OFF
		-DENABLE_LIBB2=OFF -DENABLE_LZO=OFF -DENABLE_LIBXML2=OFF -DENABLE_EXPAT=OFF
		-DENABLE_PCREPOSIX=OFF -DENABLE_PCRE2POSIX=OFF -DENABLE_ICONV=OFF
		-DENABLE_ACL=OFF -DENABLE_XATTR=OFF -DENABLE_WIN32_XMLLITE=OFF
		-DENABLE_ZLIB=ON -DENABLE_BZip2=ON -DENABLE_LZMA=ON -DENABLE_ZSTD=ON -DENABLE_LZ4=ON
	)
}

recipe_parallel() {
	# rapidgzip and indexed_bzip2 now live in the header-only librapidarchive submodule.
	source_name=librapidarchive
	dependencies=(zlib-ng bzip2 libdeflate isal)
	cmake_source="$root/scripts/parallel"
	inputs=(scripts/parallel/CMakeLists.txt internal/native/parallel/parallel.cc internal/native/parallel.h)
	cmake_options=(-DSOURCE="$sources/librapidarchive")
	archives=(libpakr_parallel.a)
}

recipe_kanzi() {
	cmake_source="$root/scripts/kanzi"
	inputs=(scripts/kanzi/CMakeLists.txt internal/native/kanzi/kanzi.cc internal/native/kanzi.h)
	cmake_options=(-DSOURCE="$sources/kanzi")
	archives=(libpakr_kanzi.a libkanzi.a)
}

recipe_wim() {
	source_name=wimlib
	cmake_source="$root/scripts/wimlib"
	inputs=(scripts/wimlib/CMakeLists.txt internal/native/wim/wim.c internal/native/wim.h)
	cmake_options=(-DSOURCE="$sources/wimlib")
	archives=(libwim.a)
}
