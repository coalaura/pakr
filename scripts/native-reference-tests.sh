#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
target=linux
arch=amd64

# Validate installed libraries and prepare patched sources and the toolchain, even on cache hits.
bash "$root/scripts/native.sh" "$target" "$arch"
source "$root/scripts/native-jobs.sh"
source "$root/scripts/native-recipes.sh"
jobs=$(native_jobs)
prefix="$root/.native/$target-$arch"
build="$root/.native/build/$target-$arch"
sources="$root/.native/src"
recipe_fast_lzma2

# Test configuration must not depend on a component build tree surviving the cache.
test_build="$build/fast-lzma2-tests"
cmake -S "$cmake_source" -B "$test_build" -G 'Unix Makefiles' \
	-DCMAKE_TOOLCHAIN_FILE="$build/toolchain/toolchain.cmake" \
	-DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib \
	-DCMAKE_FIND_ROOT_PATH="$prefix" -DCMAKE_PREFIX_PATH="$prefix" \
	-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DBUILD_TESTING=ON "${cmake_options[@]}"
cmake --build "$test_build" --parallel "$jobs"
ctest --test-dir "$test_build" --build-config Release --output-on-failure --no-tests=error -R '^fast-lzma2-decode$'
