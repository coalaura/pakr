#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
target=${1:-linux}
arch=${2:-amd64}
mode=${3:-build}
bash "$root/scripts/native.sh" "$target" "$arch"
cd "$root"

case "$mode" in
	build)
		mkdir -p dist
		suffix=
		if [[ "$target" == windows ]]; then suffix=.exe; fi
		builder build go "$target" --arch "$arch" --cgo --compat --no-gen --output "dist/pakr-$target-$arch$suffix" -ldflags "-X main.version=${VERSION:-dev}"
		;;
	test)
		if [[ "$target" != "$(go env GOOS)" || "$arch" != "$(go env GOARCH)" ]]; then
			printf 'Tests must target the host OS and architecture\n' >&2
			exit 1
		fi
		builder test go --cgo --compat --no-gen ./...
		;;
	*) printf 'Usage: bash scripts/build.sh [linux|windows|darwin] [amd64|arm64] [build|test]\n' >&2; exit 1 ;;
esac
