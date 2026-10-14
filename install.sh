#!/bin/sh
set -eu

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

download() {
	curl -fsSL --retry 3 --proto '=https' --proto-redir '=https' "$@"
}

main() {
	case "$(uname -s)" in
		Linux) os=linux ;;
		Darwin) os=darwin ;;
		*) fail 'supported systems: linux and macos' ;;
	esac

	case "$(uname -m)" in
		x86_64|amd64) arch=amd64 ;;
		aarch64|arm64) arch=arm64 ;;
		*) fail 'supported architectures: amd64 and arm64' ;;
	esac

	command -v curl >/dev/null 2>&1 || fail 'curl is required'

	if command -v sha256sum >/dev/null 2>&1; then
		checksum_tool=sha256sum
	elif command -v shasum >/dev/null 2>&1; then
		checksum_tool=shasum
	else
		fail 'sha256sum or shasum is required'
	fi

	repository=https://github.com/coalaura/pakr
	release=$(download -o /dev/null -w '%{url_effective}' "$repository/releases/latest")

	case "$release" in
		"$repository/releases/tag/"*) version=${release##*/} ;;
		*) fail 'could not resolve the latest release' ;;
	esac

	case "$version" in
		''|*[!a-zA-Z0-9._-]*) fail 'invalid release version' ;;
	esac

	asset="pakr-$os-$arch"
	url="$repository/releases/download/$version"
	temporary=$(mktemp -d "${TMPDIR:-/tmp}/pakr.XXXXXXXXXX")
	trap 'rm -rf "$temporary"' 0
	trap 'exit 1' HUP INT TERM

	printf 'installing pakr %s for %s/%s\n' "$version" "$os" "$arch" >&2
	download "$url/$asset" -o "$temporary/$asset"
	download "$url/SHA256SUMS" -o "$temporary/SHA256SUMS"

	expected=$(awk -v name="$asset" '$2 == name || $2 == ("*" name) { print $1 }' "$temporary/SHA256SUMS")
	case "$expected" in
		''|*[!a-fA-F0-9]*) fail 'invalid release checksum' ;;
	esac
	[ "${#expected}" -eq 64 ] || fail 'invalid release checksum'

	if [ "$checksum_tool" = sha256sum ]; then
		checksum=$(sha256sum "$temporary/$asset")
	else
		checksum=$(shasum -a 256 "$temporary/$asset")
	fi

	actual=${checksum%% *}
	[ "$actual" = "$expected" ] || fail 'checksum mismatch'

	if [ "$(id -u)" -eq 0 ] || [ -w /usr/local/bin ]; then
		install -d /usr/local/bin
		install -m 755 "$temporary/$asset" /usr/local/bin/pakr
	else
		command -v sudo >/dev/null 2>&1 || fail 'sudo is required to install to /usr/local/bin/pakr'
		sudo install -d /usr/local/bin
		sudo install -m 755 "$temporary/$asset" /usr/local/bin/pakr
	fi

	printf 'installed /usr/local/bin/pakr\n' >&2
}

main "$@"
