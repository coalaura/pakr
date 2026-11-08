#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p "$root/.native"
work=$(mktemp -d "$root/.native/native-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/scripts/patches" "$work/.native/downloads" "$work/upstream/include" "$work/bin" "$work/zig-lib" "$work/tools/toolchain" "$work/internal"
cp "$root/scripts/"native*.sh "$root/scripts/fetch.sh" "$work/scripts/"
cp -a "$root/scripts/bzip2" "$root/scripts/fast-lzma2" "$root/scripts/parallel" "$root/scripts/kanzi" "$root/scripts/wimlib" "$work/scripts/"
cp -a "$root/internal/native" "$work/internal/"
cp "$root/tools/toolchain/"*go* "$work/tools/toolchain/"
printf 'upstream\n' > "$work/upstream/value"
printf 'header\n' > "$work/upstream/include/igzip_lib.h"
printf 'header\n' > "$work/upstream/include/crc.h"
printf '#!/bin/sh\nprintf "%%s\\n" "${1#--prefix=}" > prefix\n' > "$work/upstream/configure"
chmod +x "$work/upstream/configure"
tar -cf "$work/upstream.tar" -C "$work" upstream
checksum=$(sha256sum "$work/upstream.tar" | cut -d ' ' -f 1)
while read -r name version ignored url; do
	[[ -z "$name" || "$name" == \#* ]] && continue
	cp "$work/upstream.tar" "$work/.native/downloads/$name-$version.tar"
	printf '%s %s %s %s\n' "$name" "$version" "$checksum" "$url" >> "$work/scripts/native.lock"
done < "$root/scripts/native.lock"

sdk="$work/Mock SDK"
framework="$sdk/System/Library/Frameworks/Ruby.framework"
mkdir -p "$framework/Versions/A/Headers" "$work/external/headers"
printf 'sdk header\n' > "$framework/Versions/A/Headers/ruby.h"
ln -s A "$framework/Versions/Current"
ln -s Versions/Current/Headers "$framework/Headers"
ln -s ../.. "$framework/Versions/A/Frameworks"
ln -s missing "$sdk/broken"
ln -s System/Library/Frameworks/Ruby.framework/Headers/ruby.h "$sdk/header.h"
printf 'odd header\n' > "$sdk/"$'space tab\tline\nheader.h\n'
ln -s $'space tab\tline\nheader.h\n' "$sdk/"$'link\nname'
printf 'external file\n' > "$work/external/file.h"
printf 'external directory\n' > "$work/external/headers/external.h"
ln -s "$work/external/file.h" "$sdk/external.h"
ln -s "$work/external/headers" "$sdk/external-headers"
ln -s "$sdk" "$work/external/headers/sdk"
# Exercise the same link handling for Zig's bundled libraries.
cp -a "$sdk/." "$work/zig-lib/"
# Keep the SDK reachable only through --sysroot in the mock build checks.
rm "$work/zig-lib/external-headers"

cat > "$work/bin/go" <<'MOCK'
#!/usr/bin/env bash
set -eu
directory=${@: -1}
mkdir -p "$directory"
for compiler in cc cxx ar ranlib; do
	if [[ "${@: -3:1}" == darwin && ( "$compiler" == cc || "$compiler" == cxx ) ]]; then
		printf '#!/bin/sh\nexec zig %s --sysroot "%s/Mock SDK" "$@"\n' "$compiler" "$MOCK_ROOT" > "$directory/$compiler"
	else
		printf '#!/bin/sh\nexec zig %s "$@"\n' "$compiler" > "$directory/$compiler"
	fi
done
printf 'toolchain\n' > "$directory/toolchain.cmake"
MOCK
cat > "$work/bin/zig" <<'MOCK'
#!/usr/bin/env bash
if [[ "$1" == env ]]; then printf '    .lib_dir = "%s",\n' "$MOCK_ROOT/zig-lib"; else printf 'test-zig\n'; fi
MOCK
cat > "$work/bin/cmake" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == --version ]]; then printf 'mock cmake\n'; exit; fi
if [[ "$1" == --build ]]; then
	if [[ "${2##*/}" == "${FAIL_COMPONENT:-}" ]]; then exit 130; fi
	exit
fi
if [[ "$1" == --install ]]; then
	directory=$2
	read -r prefix < "$directory/prefix"
	component=${directory##*/}
	archives=()
	case "$component" in
		zlib-ng) archives=(libz.a) ;;
		zstd) archives=(libzstd.a) ;;
		bzip2) archives=(libbz2.a) ;;
		bzip3) archives=(libbzip3.a) ;;
		libbsc) archives=(libbsc.a) ;;
		xz) archives=(liblzma.a) ;;
		fast-lzma2) archives=(libfast-lzma2.a) ;;
		lz4) archives=(liblz4.a) ;;
		brotli) archives=(libbrotlienc.a libbrotlidec.a libbrotlicommon.a) ;;
		libdeflate) archives=(libdeflate.a) ;;
		libarchive) archives=(libarchive.a) ;;
		parallel) archives=(libpakr_parallel.a) ;;
		kanzi) archives=(libpakr_kanzi.a libkanzi.a) ;;
		wim) archives=(libwim.a) ;;
	esac
	mkdir -p "$DESTDIR$prefix/lib" "$DESTDIR$prefix/include"
	for archive in "${archives[@]}"; do printf '!<arch>\n%s\n' "$component" > "$DESTDIR$prefix/lib/$archive"; done
	printf 'header\n' > "$DESTDIR$prefix/include/$component.h"
	exit
fi
while (( $# )); do
	case "$1" in
		-B) directory=$2; shift ;;
		-DCMAKE_INSTALL_PREFIX=*) prefix=${1#*=} ;;
	esac
	shift
done
printf '%s\n' "$prefix" > "$directory/prefix"
MOCK
cat > "$work/bin/make" <<'MOCK'
#!/usr/bin/env bash
set -eu
if [[ "$1" == --version ]]; then printf 'mock make\n'; exit; fi
if [[ "$1" != -C ]]; then
	if [[ "${2:-}" == install ]]; then
		read -r prefix < prefix
		destination=${1#DESTDIR=}
		mkdir -p "$destination$prefix/bin"
		printf '#!/bin/sh\nexit 0\n' > "$destination$prefix/bin/nasm"
		chmod +x "$destination$prefix/bin/nasm"
	fi
	exit
fi
mkdir -p "$2/bin"
printf '!<arch>\nisal\n' > "$2/bin/isa-l.a"
MOCK
chmod +x "$work/bin/"*
export MOCK_ROOT="$work"
export PATH="$work/bin:$PATH"
export JOBS=1
source "$root/scripts/native-jobs.sh"
[[ $(native_job_limit 4 17179869184 4) == 4 ]]
[[ $(native_job_limit 2 17179869184 4) == 2 ]]
[[ $(native_job_limit 4 8589934592 4) == 3 ]]
[[ $(native_job_limit 4 3221225472 4) == 1 ]]
[[ $(native_job_limit 32 34359738368 2) == 2 ]]
[[ $(native_job_limit 4 0 4) == 1 ]]
if native_job_limit 4 17179869184 invalid 2>/dev/null; then exit 1; fi
prefix="$work/.native/linux-arm64"

fail() {
	cat "$work/build.log" >&2
	printf '%s\n' "$*" >&2
	exit 1
}

build() {
	bash "$work/scripts/native.sh" linux arm64 > "$work/build.log" 2>&1 || fail 'mock build failed'
}

expect_rebuilt() {
	local actual
	actual=$(sed -n 's/^\[\([^]]*\)\] rebuild:.*/\1/p' "$work/build.log" | paste -sd ' ')
	[[ "$actual" == "$*" ]] || fail "expected rebuilds '$*', got '$actual'"
	printf 'verified rebuilds: %s\n' "${actual:-none}"
}

# Run the actual helper in a fresh shell with the same error handling as native.sh.
sed -n '/^hash_tree() (/,/^)/p' "$work/scripts/native.sh" > "$work/hash-tree.sh"
fingerprint() {
	timeout 30 bash -c 'set -euo pipefail; shopt -s inherit_errexit; export LC_ALL=C; source "$1"; build=$2; signature=$(hash_tree "$3" | sha256sum); printf "%s\n" "$signature"' bash "$work/hash-tree.sh" "$work" "$1"
}

expect_fingerprint_changed() {
	local current
	current=$(fingerprint "$sdk") || fail 'SDK fingerprint failed'
	[[ "$current" != "$previous" ]] || fail 'SDK change did not invalidate fingerprint'
	previous=$current
}

previous=$(fingerprint "$sdk") || fail 'recursive SDK fingerprint failed'
[[ $(fingerprint "$sdk") == "$previous" ]] || fail 'SDK fingerprint is unstable'
rm "$sdk/broken"
ln -s missing "$sdk/broken"
[[ $(fingerprint "$sdk") == "$previous" ]] || fail 'directory entry order changed SDK fingerprint'
touch "$framework/Versions/A/Headers/ruby.h"
[[ $(fingerprint "$sdk") == "$previous" ]] || fail 'timestamps changed SDK fingerprint'
printf 'changed\n' >> "$framework/Versions/A/Headers/ruby.h"
expect_fingerprint_changed
ln -sfn System/Library/Frameworks/Ruby.framework/Versions/A/Headers/ruby.h "$sdk/header.h"
expect_fingerprint_changed
ln -sfn absent "$sdk/broken"
expect_fingerprint_changed
printf 'changed\n' >> "$work/external/file.h"
expect_fingerprint_changed
printf 'changed\n' >> "$work/external/headers/external.h"
expect_fingerprint_changed
mv "$sdk/"$'space tab\tline\nheader.h\n' "$sdk/"$'space tab\tline\nrenamed.h\n'
expect_fingerprint_changed
ln -sfn $'space tab\tline\nrenamed.h\n' "$sdk/"$'link\nname'
expect_fingerprint_changed
printf 'changed\n' >> "$sdk/"$'space tab\tline\nrenamed.h\n'
expect_fingerprint_changed
[[ $(fingerprint "$sdk") == "$previous" ]] || fail 'modified SDK fingerprint is unstable'

# Fail after producing valid output: neither traversal nor hashing may yield a cache key.
for command in find sort readlink sha256sum; do
	real_command=$(command -v "$command")
	printf '#!/usr/bin/env bash\n%q "$@"\nif (( $# )); then exit 1; fi\n' "$real_command" > "$work/bin/$command"
	chmod +x "$work/bin/$command"
	if fingerprint "$sdk" > "$work/build.log" 2>&1; then fail "$command failure accepted"; fi
	rm "$work/bin/$command"
done
if fingerprint "$work/missing-sdk" > "$work/build.log" 2>&1; then fail 'missing SDK accepted'; fi
printf 'verified tree fingerprints: stable, NUL-safe, cycle-safe, external inputs and errors\n'

build
expect_rebuilt zlib-ng zstd bzip2 bzip3 libbsc xz fast-lzma2 lz4 brotli libdeflate isal libarchive parallel kanzi wim
build
expect_rebuilt
printf '// Go-only change\n' >> "$work/internal/native/codec.go"
build
expect_rebuilt
printf '%s\n' '--- a/value' '+++ b/value' '@@ -1 +1 @@' '-upstream' '+patched' > "$work/scripts/patches/rapidgzip-test.patch"
build
expect_rebuilt parallel
printf '\n' >> "$work/internal/native/wim/wim.c"
build
expect_rebuilt wim
printf '\n' >> "$work/internal/native/kanzi/kanzi.cc"
build
expect_rebuilt kanzi
cp "$work/scripts/patches/rapidgzip-test.patch" "$work/scripts/patches/zlib-ng-test.patch"
build
expect_rebuilt zlib-ng libarchive parallel
printf '\n' >> "$work/scripts/bzip2/CMakeLists.txt"
build
expect_rebuilt bzip2 libarchive parallel
printf '\n' >> "$work/scripts/fast-lzma2/CMakeLists.txt"
build
expect_rebuilt fast-lzma2
printf '# toolchain revision\n' >> "$work/bin/zig"
build
expect_rebuilt zlib-ng zstd bzip2 bzip3 libbsc xz fast-lzma2 lz4 brotli libdeflate isal libarchive parallel kanzi wim
printf 'changed Zig header\n' >> "$work/zig-lib/System/Library/Frameworks/Ruby.framework/Versions/A/Headers/ruby.h"
build
expect_rebuilt zlib-ng zstd bzip2 bzip3 libbsc xz fast-lzma2 lz4 brotli libdeflate isal libarchive parallel kanzi wim
printf 'corrupt\n' >> "$prefix/lib/libzstd.a"
build
expect_rebuilt zstd
rm "$prefix/include/brotli.h"
build
expect_rebuilt brotli
rm "$prefix/.state/kanzi.manifest"
build
expect_rebuilt kanzi
sha256sum "$prefix/lib/libz.a" | sed "s|$prefix/|./|" >> "$prefix/.state/wim.manifest"
build
expect_rebuilt wim
[[ -s "$prefix/lib/libz.a" ]] || fail 'corrupt manifest removed another component output'
rm "$prefix/.state/parallel.complete"
if FAIL_COMPONENT=parallel bash "$work/scripts/native.sh" linux arm64 > "$work/build.log" 2>&1; then fail 'failed compilation accepted'; fi
[[ ! -e "$prefix/.state/parallel.complete" ]] || fail 'failed compilation left completion marker'
[[ ! -e "$work/.native/build/linux-arm64/cache-ready" ]] || fail 'failed build left cache-ready marker'
build
expect_rebuilt parallel
build
expect_rebuilt
for target in linux windows darwin; do
	for arch in amd64 arm64; do
		bash "$work/scripts/native.sh" "$target" "$arch" > "$work/build.log" 2>&1 || fail "mock $target/$arch build failed"
		bash "$work/scripts/native.sh" "$target" "$arch" > "$work/build.log" 2>&1 || fail "mock $target/$arch reuse failed"
		expect_rebuilt
		printf 'verified target: %s/%s\n' "$target" "$arch"
	done
done
printf 'changed SDK header\n' >> "$framework/Versions/A/Headers/ruby.h"
build
expect_rebuilt
bash "$work/scripts/native.sh" darwin amd64 > "$work/build.log" 2>&1 || fail 'mock Darwin SDK invalidation failed'
expect_rebuilt zlib-ng zstd bzip2 bzip3 libbsc xz fast-lzma2 lz4 brotli libdeflate nasm isal libarchive parallel kanzi wim
bash "$work/scripts/native.sh" darwin amd64 > "$work/build.log" 2>&1 || fail 'mock Darwin SDK reuse failed'
expect_rebuilt
ln -sfn ./Versions/Current/Headers "$framework/Headers"
bash "$work/scripts/native.sh" darwin amd64 > "$work/build.log" 2>&1 || fail 'mock Darwin SDK link invalidation failed'
expect_rebuilt zlib-ng zstd bzip2 bzip3 libbsc xz fast-lzma2 lz4 brotli libdeflate nasm isal libarchive parallel kanzi wim
bash "$work/scripts/native.sh" darwin amd64 > "$work/build.log" 2>&1 || fail 'mock Darwin SDK link reuse failed'
expect_rebuilt
for command in find sha256sum; do
	real_command=$(command -v "$command")
	printf '#!/usr/bin/env bash\nif [[ "$PWD" == "$MOCK_ROOT/Mock SDK" ]]; then exit 1; fi\nexec %q "$@"\n' "$real_command" > "$work/bin/$command"
	chmod +x "$work/bin/$command"
	if bash "$work/scripts/native.sh" darwin amd64 > "$work/build.log" 2>&1; then fail "SDK $command failure accepted by mock build"; fi
	[[ ! -e "$work/.native/build/darwin-amd64/cache-ready" ]] || fail 'failed fingerprint left cache-ready marker'
	if grep -q '^\[.*\] rebuild:' "$work/build.log"; then fail 'failed fingerprint reached component builds'; fi
	rm "$work/bin/$command"
done
bash "$work/scripts/native.sh" darwin amd64 > "$work/build.log" 2>&1 || fail 'mock Darwin fingerprint recovery failed'
expect_rebuilt
printf 'native component cache regression tests passed\n'
