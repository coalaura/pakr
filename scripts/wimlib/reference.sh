#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
source="$root/.native/build/wimlib-reference"
bash "$root/scripts/fetch.sh"
mkdir -p "$source"
tar -xf "$root/.native/downloads/wimlib-1.14.5.tar" --strip-components=1 -C "$source"

# Build the pristine upstream CLI independently of pakr's metadata patch and adapter.
cd "$source"
CC="$root/.native/build/linux-amd64/toolchain/cc" LDFLAGS=-static ./configure --without-ntfs-3g --without-fuse --disable-shared --enable-static
make -j "${JOBS:-2}" wimlib-imagex
