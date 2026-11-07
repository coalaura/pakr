#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p "$root/.native"
work=$(mktemp -d "$root/.native/fetch-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/scripts/patches" "$work/.native/downloads" "$work/upstream"
cp "$root/scripts/fetch.sh" "$work/scripts/"

fail() {
	printf '%s\n' "$*" >&2
	exit 1
}

write_patch() {
	local path=$1 before=$2 after=$3
	printf '%s\n' '--- a/value' '+++ b/value' '@@ -1 +1 @@' "-$before" "+$after" > "$path"
}

fetch() {
	if ! bash "$work/scripts/fetch.sh" > "$work/fetch.log" 2>&1; then
		cat "$work/fetch.log" >&2
		fail 'fetch failed'
	fi
}

printf 'upstream\n' > "$work/upstream/value"
tar -cf "$work/upstream.tar" -C "$work" upstream
checksum=$(sha256sum "$work/upstream.tar" | cut -d ' ' -f 1)
names=(libarchive libbsc kanzi bzip3 plain)
for name in "${names[@]}"; do
	cp "$work/upstream.tar" "$work/.native/downloads/$name-1.tar"
	printf '%s 1 %s https://invalid.example/unused\n' "$name" "$checksum" >> "$work/scripts/native.lock"
done

# Dependent hunks prove deterministic ordering and exactly-once application.
write_patch "$work/scripts/patches/libarchive-extraction.patch" upstream hardened
write_patch "$work/scripts/patches/libarchive-rar-checksum.patch" hardened checked
write_patch "$work/scripts/patches/libbsc-bounds.patch" upstream bounded
write_patch "$work/scripts/patches/libbsc-throughput.patch" bounded fast
write_patch "$work/scripts/patches/kanzi-memory.patch" upstream memory
write_patch "$work/scripts/patches/bzip3-allocation.patch" upstream allocation
fetch
[[ $(< "$work/.native/src/libarchive/value") == checked ]] || fail 'libarchive patches missing'
[[ $(< "$work/.native/src/libbsc/value") == fast ]] || fail 'BSC patches missing'
[[ $(< "$work/.native/src/kanzi/value") == memory ]] || fail 'Kanzi patch missing'
[[ $(< "$work/.native/src/bzip3/value") == allocation ]] || fail 'BZIP3 patch missing'

for name in "${names[@]}"; do
	touch "$work/.native/src/$name/cached"
done
fetch
for name in "${names[@]}"; do
	[[ -f "$work/.native/src/$name/cached" ]] || fail "unchanged $name cache discarded"
done

# Changing either libarchive patch must discard previously patched sources.
printf '\n' >> "$work/scripts/patches/libarchive-extraction.patch"
fetch
[[ ! -f "$work/.native/src/libarchive/cached" ]] || fail 'extraction patch change not detected'
[[ $(< "$work/.native/src/libarchive/value") == checked ]] || fail 'clean source was not patched'
touch "$work/.native/src/libarchive/cached"
write_patch "$work/scripts/patches/libarchive-rar-checksum.patch" hardened revised
fetch
[[ ! -f "$work/.native/src/libarchive/cached" ]] || fail 'RAR patch change not detected'
[[ $(< "$work/.native/src/libarchive/value") == revised ]] || fail 'stale RAR source reused'
[[ -f "$work/.native/src/plain/cached" ]] || fail 'unrelated source cache discarded'

for name in libbsc kanzi bzip3; do
	patches=("$work/scripts/patches/$name-"*.patch)
	printf '\n' >> "${patches[0]}"
	fetch
	[[ ! -f "$work/.native/src/$name/cached" ]] || fail "$name patch change not detected"
done

# Failed patching cannot leave a reusable completion stamp.
write_patch "$work/scripts/patches/libarchive-rar-checksum.patch" nonexistent broken
if bash "$work/scripts/fetch.sh" > "$work/fetch.log" 2>&1; then
	fail 'invalid patch accepted'
fi
shopt -s nullglob
stamps=("$work/.native/src/libarchive/".pakr-*)
(( ${#stamps[@]} == 0 )) || fail 'failed patch left a source stamp'
write_patch "$work/scripts/patches/libarchive-rar-checksum.patch" hardened recovered
fetch
[[ $(< "$work/.native/src/libarchive/value") == recovered ]] || fail 'retry did not start clean'

# Exercise native.sh's real cache decisions with cheap stand-ins for compilation.
cp "$root/scripts/native.sh" "$work/scripts/"
mkdir -p "$work/bin" "$work/scripts/bzip2" "$work/scripts/parallel" "$work/scripts/kanzi" "$work/tools/toolchain" "$work/internal/native/parallel" "$work/internal/native/kanzi"
touch "$work/scripts/bzip2/CMakeLists.txt" "$work/scripts/parallel/CMakeLists.txt" "$work/scripts/kanzi/CMakeLists.txt" "$work/tools/toolchain/main.go" "$work/tools/toolchain/go.sum"
touch "$work/internal/native/parallel/parallel.cc" "$work/internal/native/parallel.h" "$work/internal/native/kanzi/kanzi.cc" "$work/internal/native/kanzi.h"
mkdir -p "$work/.native/src/isal/include" "$work/.native/host/bin" "$work/.native/linux-arm64/lib"
touch "$work/.native/src/isal/include/igzip_lib.h" "$work/.native/src/isal/include/crc.h"
touch "$work/.native/linux-arm64/lib/libpakr_parallel.a" "$work/.native/linux-arm64/lib/libpakr_kanzi.a"
printf '#!/usr/bin/env bash\nprintf "test-toolchain\\n"\n' > "$work/bin/zig"
printf '#!/usr/bin/env bash\nexit 0\n' > "$work/bin/go"
cp "$work/bin/go" "$work/bin/cmake"
printf '#!/usr/bin/env bash\nmkdir -p "$2/bin"\ntouch "$2/bin/isa-l.a"\n' > "$work/bin/make"
chmod +x "$work/bin/"*

build() {
	if ! PATH="$work/bin:$PATH" JOBS=1 bash "$work/scripts/native.sh" linux arm64 > "$work/build.log" 2>&1; then
		cat "$work/build.log" >&2
		fail 'mock native build failed'
	fi
}

build
stamps=("$work/.native/linux-arm64/".complete-*)
(( ${#stamps[@]} == 1 )) || fail 'native completion stamp missing'
previous_stamp=${stamps[0]}
build
[[ -f "$previous_stamp" ]] || fail 'unchanged native cache discarded'
[[ $(< "$work/build.log") == 'Native libraries ready: linux/arm64' ]] || fail 'unchanged native libraries rebuilt'

touch "$work/.native/src/libarchive/cached"
write_patch "$work/scripts/patches/libarchive-extraction.patch" upstream safer
write_patch "$work/scripts/patches/libarchive-rar-checksum.patch" safer rebuilt
build
[[ ! -f "$previous_stamp" ]] || fail 'patch change did not invalidate native build cache'
[[ ! -f "$work/.native/src/libarchive/cached" ]] || fail 'native rebuild used stale source'
[[ $(< "$work/.native/src/libarchive/value") == rebuilt ]] || fail 'native rebuild missed new patches'
stamps=("$work/.native/linux-arm64/".complete-*)
(( ${#stamps[@]} == 1 )) || fail 'native rebuild did not finish'

# Cached downloads still require the pinned checksum on every invocation.
printf 'corrupt' >> "$work/.native/downloads/libarchive-1.tar"
if bash "$work/scripts/fetch.sh" > "$work/fetch.log" 2>&1; then
	fail 'corrupt cached source accepted'
fi
printf 'native source cache regression tests passed\n'
