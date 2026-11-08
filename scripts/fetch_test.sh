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

# Cached downloads still require the pinned checksum on every invocation.
printf 'corrupt' >> "$work/.native/downloads/libarchive-1.tar"
if bash "$work/scripts/fetch.sh" > "$work/fetch.log" 2>&1; then
	fail 'corrupt cached source accepted'
fi
printf 'native source cache regression tests passed\n'
