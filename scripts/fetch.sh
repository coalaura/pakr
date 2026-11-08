#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
shopt -s nullglob

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
	patches=("$root/scripts/patches/$name-"*.patch)
	source_stamp=$(
		printf '%s\n' "$checksum"
		sha256sum < "$root/scripts/fetch.sh"
		for source_patch in "${patches[@]}"; do
			printf '%s\n' "${source_patch##*/}"
			sha256sum < "$source_patch"
		done
	)
	source_stamp=$(printf '%s' "$source_stamp" | sha256sum | cut -d ' ' -f 1)
	if [[ ! -f "$cache/src/$name/.pakr-$source_stamp" ]]; then
		rm -rf "$cache/src/$name"
		mkdir -p "$cache/src/$name"
		tar -xf "$archive" --strip-components=1 -C "$cache/src/$name"
		for source_patch in "${patches[@]}"; do
			patch --batch -d "$cache/src/$name" -p1 < "$source_patch"
		done
		touch "$cache/src/$name/.pakr-$source_stamp"
	fi
done < "$root/scripts/native.lock"
