#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
version=${1:?usage: source.sh version}
case "$version" in *[!a-zA-Z0-9._-]*) printf 'invalid version\n' >&2; exit 1 ;; esac
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
bundle="$stage/pakr-source-$version"
mkdir -p "$bundle/.native/downloads" "$bundle/licenses" "$root/dist"

# Include the actual build inputs, including local changes in developer bundles.
git -c safe.directory="$root" -C "$root" ls-files -z --cached --others --exclude-standard | tar -C "$root" --null -T - -cf - | tar -xf - -C "$bundle"
bash "$root/scripts/fetch.sh"
while read -r name release checksum url; do
	[[ -z "$name" || "$name" == \#* ]] && continue
	cp "$root/.native/downloads/$name-$release.tar" "$bundle/.native/downloads/"
done < "$root/scripts/native.lock"
cp "$root/.native/src/bzip3/LICENSE" "$bundle/licenses/LGPL-3.0.txt"
cp "$root/.native/src/xz/COPYING.GPLv3" "$bundle/licenses/GPL-3.0.txt"
cp "$root/.native/src/bzip3/3rdparty/libsais-LICENSE" "$bundle/licenses/libsais.txt"
cp "$root/.native/src/kanzi/LICENSE" "$bundle/licenses/kanzi.txt"
cp "$root/.native/src/wimlib/COPYING.LGPL" "$bundle/licenses/LGPL-2.1.txt"
cp "$root/.native/src/wimlib/COPYING" "$bundle/licenses/wimlib.txt"
printf 'BZIP3 copyright (C) 2022-2024 Kamila Szewczyk; LGPLv3 or later.\nSee THIRD_PARTY.md for license notices, relinking permission and rebuilding instructions. Native notices are in the included source archives.\n' > "$bundle/licenses/NOTICE.txt"
printf 'wimlib 1.14.5, copyright Eric Biggers and contributors; LGPL-2.1-or-later library option, without NTFS-3G. Full copyright notices are in the included wimlib source archive.\n' >> "$bundle/licenses/NOTICE.txt"
(cd "$bundle" && go mod vendor)
(cd "$bundle/tools/toolchain" && go mod vendor)
{ go version; zig version; cmake --version; printf 'Release: %s\nBuilder image: %s\n' "$version" "${BUILD_IMAGE:-local}"; } > "$bundle/TOOLCHAIN.txt"
tar -czf "$root/dist/pakr-source-$version.tar.gz" -C "$stage" "pakr-source-$version"
