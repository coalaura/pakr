#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cache="$root/.native"
mkdir -p "$cache/downloads" "$cache/src"

while read -r name version checksum url; do
	[[ -z "$name" || "$name" == \#* ]] && continue
	archive="$cache/downloads/$name-$version.tar"
	if [[ ! -f "$archive" ]]; then
		curl --fail --location --retry 3 --proto '=https' "$url" -o "$archive.part"
		printf '%s  %s\n' "$checksum" "$archive.part" | sha256sum --check --status
		mv "$archive.part" "$archive"
	fi
	printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --status
	if [[ ! -f "$cache/src/$name/.pakr-$checksum" ]]; then
		rm -rf "$cache/src/$name"
		mkdir -p "$cache/src/$name"
		tar -xf "$archive" --strip-components=1 -C "$cache/src/$name"
		touch "$cache/src/$name/.pakr-$checksum"
	fi
done < "$root/scripts/native.lock"

if [[ ! -f "$cache/src/libbsc/.pakr-bounds" ]]; then
	patch -d "$cache/src/libbsc" -p1 < "$root/scripts/patches/libbsc-bounds.patch"
	touch "$cache/src/libbsc/.pakr-bounds"
fi

if [[ ! -f "$cache/src/bzip3/.pakr-allocation-check" ]]; then
	patch -d "$cache/src/bzip3" -p1 < "$root/scripts/patches/bzip3-allocation.patch"
	touch "$cache/src/bzip3/.pakr-allocation-check"
fi

if [[ ! -f "$cache/src/libarchive/.pakr-rar-checksum" ]]; then
	patch -d "$cache/src/libarchive" -p1 < "$root/scripts/patches/libarchive-rar-checksum.patch"
	touch "$cache/src/libarchive/.pakr-rar-checksum"
fi
