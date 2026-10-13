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
