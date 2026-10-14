#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
target=${1:-linux}
arch=${2:-amd64}
mode=${3:-build}

case "$mode" in
	build) ;;
	test)
		if [[ "$target" != "$(go env GOOS)" || "$arch" != "$(go env GOARCH)" ]]; then
			printf 'tests must target the host os and architecture\n' >&2
			exit 1
		fi
		;;
	*) printf 'usage: bash scripts/build.sh [linux|windows|darwin] [amd64|arm64] [build|test] [go flags...]\n' >&2; exit 1 ;;
esac

bash "$root/scripts/native.sh" "$target" "$arch"
cd "$root"
source ".native/build/$target-$arch/toolchain/env.sh"

case "$mode" in
	build)
		mkdir -p dist
		suffix=
		if [[ "$target" == windows ]]; then suffix=.exe; fi
		go build "${GO_BUILD_FLAGS[@]}" -ldflags "$GO_LDFLAGS -X main.version=${VERSION:-dev}" -o "dist/pakr-$target-$arch$suffix" "${@:4}" .
		;;
	test)
		go test "${GO_BUILD_FLAGS[@]}" -ldflags "$GO_LDFLAGS" "${@:4}" ./...
		;;
esac
