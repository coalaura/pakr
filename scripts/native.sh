#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
target=${1:-linux}
arch=${2:-amd64}
jobs=${JOBS:-$(getconf _NPROCESSORS_ONLN)}
case "$target/$arch" in
	linux/amd64|linux/arm64|windows/amd64|windows/arm64|darwin/amd64|darwin/arm64) ;;
	*) printf 'Unsupported target: %s/%s\n' "$target" "$arch" >&2; exit 1 ;;
esac

prefix="$root/.native/$target-$arch"
build="$root/.native/build/$target-$arch"
toolchain="$build/toolchain"
mkdir -p "$prefix/include" "$prefix/lib" "$build"
bash "$root/scripts/fetch.sh"

signature=$(cat "$root/scripts/native.lock" "$root/scripts/native.sh" "$root/scripts/bzip2/CMakeLists.txt" "$root/scripts/patches/"*.patch "$root/tools/toolchain/main.go" "$root/tools/toolchain/go.sum"; zig version)
signature=$(printf '%s' "$signature" | sha256sum | cut -d ' ' -f 1)
(cd "$root/tools/toolchain" && go run . "$target" "$arch" "$toolchain")

build_cmake() {
	local name=$1 source=$2
	shift 2
	printf 'Building %s for %s/%s\n' "$name" "$target" "$arch"
	if ! (
		cmake --fresh -S "$source" -B "$build/$name" -G 'Unix Makefiles' \
			-DCMAKE_TOOLCHAIN_FILE="$toolchain/toolchain.cmake" \
			-DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib \
			-DCMAKE_FIND_ROOT_PATH="$prefix" -DCMAKE_PREFIX_PATH="$prefix" \
			-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DBUILD_TESTING=OFF "$@" &&
		cmake --build "$build/$name" --parallel "$jobs" &&
		cmake --install "$build/$name"
	) >"$build/$name.log" 2>&1; then
		tail -n 80 "$build/$name.log" >&2
		exit 1
	fi
}

build_parallel() {
	local bridge_signature
	bridge_signature=$(cat "$root/scripts/parallel/CMakeLists.txt" "$root/internal/native/parallel/parallel.cc" "$root/internal/native/parallel.h"; printf '%s' "$signature")
	bridge_signature=$(printf '%s' "$bridge_signature" | sha256sum | cut -d ' ' -f 1)
	if [[ -f "$prefix/.parallel-$bridge_signature" && -f "$prefix/lib/libpakr_parallel.a" ]]; then
		return
	fi
	build_cmake parallel "$root/scripts/parallel" -DSOURCE="$root/.native/src/rapidgzip"
	rm -f "$prefix"/.parallel-*
	touch "$prefix/.parallel-$bridge_signature"
}

if [[ -f "$prefix/.complete-$signature" ]]; then
	printf 'Native libraries ready: %s/%s\n' "$target" "$arch"
	build_parallel
	exit 0
fi

sources="$root/.native/src"
if [[ ! -f "$sources/libdeflate/.pakr-clang22" ]]; then
	patch -d "$sources/libdeflate" -p1 <"$root/scripts/patches/libdeflate-clang22.patch"
	touch "$sources/libdeflate/.pakr-clang22"
fi
build_cmake zlib-ng "$sources/zlib-ng" -DZLIB_COMPAT=ON -DZLIB_ENABLE_TESTS=OFF -DZLIBNG_ENABLE_TESTS=OFF
build_cmake zstd "$sources/zstd/build/cmake" -DZSTD_BUILD_SHARED=OFF -DZSTD_BUILD_STATIC=ON -DZSTD_BUILD_PROGRAMS=OFF -DZSTD_BUILD_TESTS=OFF -DZSTD_MULTITHREAD_SUPPORT=ON
build_cmake bzip2 "$root/scripts/bzip2" -DSOURCE="$sources/bzip2"
build_cmake xz "$sources/xz" -DXZ_NLS=OFF -DXZ_TOOL_XZ=OFF -DXZ_TOOL_XZDEC=OFF -DXZ_TOOL_LZMADEC=OFF -DXZ_TOOL_LZMAINFO=OFF -DXZ_DOC=OFF -DXZ_EXTERNAL_SHA256=OFF
build_cmake lz4 "$sources/lz4/build/cmake" -DLZ4_BUILD_CLI=OFF -DLZ4_BUILD_LEGACY_LZ4C=OFF
build_cmake brotli "$sources/brotli" -DBROTLI_DISABLE_TESTS=ON
build_cmake libdeflate "$sources/libdeflate" -DLIBDEFLATE_BUILD_SHARED_LIB=OFF -DLIBDEFLATE_BUILD_GZIP=OFF

if [[ "$arch" == amd64 ]]; then
	nasm="$root/.native/host/bin/nasm"
	if [[ ! -x "$nasm" ]]; then
		printf 'Building host assembler\n'
		mkdir -p "$root/.native/build/nasm"
		(cd "$root/.native/build/nasm" && "$sources/nasm/configure" --prefix="$root/.native/host" && make -j "$jobs" && make install) >"$build/nasm.log" 2>&1
	fi
else
	nasm=false
fi

printf 'Building ISA-L for %s/%s\n' "$target" "$arch"
rm -rf "$build/isal"
mkdir -p "$build/isal"
cp -a "$sources/isal/." "$build/isal/"
isal_arch=
if [[ "$target" == windows ]]; then isal_arch=mingw; fi
if [[ "$target" == darwin ]]; then isal_arch=Darwin; fi
if [[ "$arch" == arm64 ]]; then isal_arch=noarch; fi
if ! make -C "$build/isal" -f Makefile.unx -j "$jobs" bin/isa-l.a units=igzip arch="$isal_arch" CC="$toolchain/cc" AR="$toolchain/ar" AS="$nasm" >"$build/isal.log" 2>&1; then
	tail -n 80 "$build/isal.log" >&2
	exit 1
fi
cp "$build/isal/bin/isa-l.a" "$prefix/lib/libisal.a"
cp "$sources/isal/include/igzip_lib.h" "$sources/isal/include/crc.h" "$prefix/include/"

build_cmake libarchive "$sources/libarchive" \
	-DENABLE_TEST=OFF -DENABLE_TAR=OFF -DENABLE_CPIO=OFF -DENABLE_CAT=OFF -DENABLE_UNZIP=OFF \
	-DENABLE_OPENSSL=OFF -DENABLE_MBEDTLS=OFF -DENABLE_NETTLE=OFF -DENABLE_CNG=OFF \
	-DENABLE_LIBB2=OFF -DENABLE_LZO=OFF -DENABLE_LIBXML2=OFF -DENABLE_EXPAT=OFF \
	-DENABLE_PCREPOSIX=OFF -DENABLE_PCRE2POSIX=OFF -DENABLE_ICONV=OFF \
	-DENABLE_ACL=OFF -DENABLE_XATTR=OFF -DENABLE_WIN32_XMLLITE=OFF \
	-DENABLE_ZLIB=ON -DENABLE_BZip2=ON -DENABLE_LZMA=ON -DENABLE_ZSTD=ON -DENABLE_LZ4=ON

rm -f "$prefix"/.complete-*
touch "$prefix/.complete-$signature"
build_parallel
