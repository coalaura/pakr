#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
shopt -s nullglob inherit_errexit

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
target=${1:-linux}
arch=${2:-amd64}
case "$target/$arch" in
	linux/amd64|linux/arm64|windows/amd64|windows/arm64|darwin/amd64|darwin/arm64) ;;
	*) printf 'Unsupported target: %s/%s\n' "$target" "$arch" >&2; exit 1 ;;
esac

source "$root/scripts/native-jobs.sh"
source "$root/scripts/native-recipes.sh"
jobs=$(native_jobs)
prefix="$root/.native/$target-$arch"
build="$root/.native/build/$target-$arch"
sources="$root/.native/src"
toolchain="$build/toolchain"
state="$prefix/.state"
mkdir -p "$prefix/include" "$prefix/lib" "$build" "$state"
rm -f "$build/cache-ready"
printf 'Native workers: %s (requested maximum: %s)\n' "$jobs" "${JOBS:-4}"
bash "$root/scripts/fetch.sh"
(cd "$root/tools/toolchain" && go run -buildvcs=false . "$target" "$arch" "$toolchain")

digest() {
	sha256sum | cut -d ' ' -f 1
}

hash_files() {
	local file
	for file in "$@"; do
		printf '%s\0' "${file#"$root/"}"
		sha256sum < "$file"
	done
}

hash_tree() (
	local directory file resolved existing covered scratch index=0
	local -a directories
	# A trailing slash preserves newlines in directory names during command substitution.
	directory=$(cd -P -- "$1" && printf '%s/' "$PWD")
	directories=("$directory")
	scratch=$(mktemp -d "$build/fingerprint.XXXXXX")
	trap 'rm -rf -- "$scratch"' EXIT

	while (( index < ${#directories[@]} )); do
		directory=${directories[$index]}
		cd -- "$directory"
		printf 'tree\0%s\0' "$index"
		find . -type f -print0 | sort -z | xargs -0 -r sha256sum --zero --
		# Materialize the list so find/sort failures cannot be lost in process substitution.
		find . -type l -print0 | sort -z > "$scratch/links"
		: > "$scratch/files"
		while IFS= read -r -d '' file; do
			printf 'link\0%s\0' "$file"
			readlink -z -- "$file"
			if [[ -f "$file" ]]; then
				# Include contents even when a file link points outside the walked trees.
				printf '%s\0' "$file" >> "$scratch/files"
			elif [[ -d "$file" ]]; then
				resolved=$(cd -P -- "$file" && printf '%s/' "$PWD")
				covered=false
				for existing in "${directories[@]}"; do
					if [[ "$resolved" == "$existing"* ]]; then covered=true; break; fi
				done
				# External directories get their own non-following walk, once, even with cycles.
				if [[ "$covered" == false ]]; then directories+=("$resolved"); fi
			fi
		done < "$scratch/links"
		printf 'linked-files\0'
		xargs -0 -r sha256sum --zero -- < "$scratch/files"
		index=$((index + 1))
	done
)

compiler_arguments() (
	# Inspect the generated shell wrapper without invoking its compiler.
	exec() { printf '%s\n' "$@"; }
	source "$toolchain/cc"
)

toolchain_signature() {
	local argument previous= zig_lib
	printf '%s\n' "$target/$arch" "${BUILD_IMAGE_ID:-local}"
	hash_files "$root/scripts/native.sh" "$root/scripts/fetch.sh" "$root/tools/toolchain/main.go" "$root/tools/toolchain/go.mod" "$root/tools/toolchain/go.sum"
	for argument in cc cxx ar ranlib toolchain.cmake; do
		# Workspace paths do not change the target ABI; SDK paths and flags do.
		sed "s|$root|<workspace>|g" "$toolchain/$argument"
	done
	zig version
	sha256sum < "$(command -v zig)"
	# Zig's bundled headers and libc sources are also effective toolchain inputs.
	zig_lib=$(zig env | sed -nE 's/^[[:space:]]*(\.lib_dir =|"lib_dir":) "(.*)",$/\2/p')
	[[ -n "$zig_lib" ]]
	hash_tree "$zig_lib"
	while IFS= read -r argument; do
		if [[ "$previous" == --sysroot ]]; then hash_tree "$argument"; fi
		previous=$argument
	done < <(compiler_arguments)
	cmake --version
	make --version
	printf '%s\n' "${CFLAGS:-}" "${CXXFLAGS:-}" "${CPPFLAGS:-}" "${LDFLAGS:-}" "${SDKROOT:-}" "${MACOSX_DEPLOYMENT_TARGET:-}"
	printf '%s\n' "${ZIG_LIBC:-}" "${C_INCLUDE_PATH:-}" "${CPLUS_INCLUDE_PATH:-}" "${LIBRARY_PATH:-}" "${CPATH:-}" "${PKG_CONFIG_PATH:-}"
}

component_signature() {
	local dependency line name version checksum url
	printf '%s\n' "$common_signature"
	declare -f "recipe_${component//-/_}"
	while read -r name version checksum url; do
		if [[ "$name" == "$source_name" ]]; then
			printf '%s\n' "$name $version $checksum $url"
			break
		fi
	done < "$root/scripts/native.lock"
	[[ "$name" == "$source_name" ]] || return 1
	hash_files "$root/scripts/patches/$source_name-"*.patch
	for line in "${inputs[@]}"; do hash_files "$root/$line"; done
	for dependency in "${dependencies[@]}"; do
		printf '%s %s\n' "$dependency" "${signatures[$dependency]}"
	done
	if [[ "$build_kind" == nasm ]]; then
		uname -sm
		cc --version
		sha256sum < "$(command -v cc)"
	fi
}

phase() {
	local label=$1 started=$SECONDS status=0
	shift
	printf '[%s] %s started\n' "$component" "$label" >> "$log"
	"$@" >> "$log" 2>&1 || status=$?
	printf '[%s] %s: %ss (exit %s)\n' "$component" "$label" "$((SECONDS - started))" "$status" | tee -a "$log"
	if (( status != 0 )); then
		printf '[%s] failed; detailed log: %s\n' "$component" "$log" >&2
		tail -n 80 "$log" >&2
		exit "$status"
	fi
}

build_component() {
	case "$build_kind" in
		cmake)
			phase configure cmake -S "$cmake_source" -B "$build/$component" -G 'Unix Makefiles' \
				-DCMAKE_TOOLCHAIN_FILE="$toolchain/toolchain.cmake" \
				-DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib \
				-DCMAKE_FIND_ROOT_PATH="$prefix" -DCMAKE_PREFIX_PATH="$prefix" \
				-DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DBUILD_TESTING=OFF "${cmake_options[@]}"
			phase compile cmake --build "$build/$component" --parallel "$jobs" --verbose
			phase install env DESTDIR="$stage" cmake --install "$build/$component"
			;;
		isal)
			cp -a "$sources/isal/." "$build/$component/"
			phase compile make -C "$build/$component" -f Makefile.unx -j "$jobs" bin/isa-l.a units=igzip arch="$isal_arch" CC="$toolchain/cc" AR="$toolchain/ar" AS="$prefix/bin/nasm"
			mkdir -p "$stage$prefix/lib" "$stage$prefix/include"
			cp "$build/$component/bin/isa-l.a" "$stage$prefix/lib/libisal.a"
			cp "$sources/isal/include/igzip_lib.h" "$sources/isal/include/crc.h" "$stage$prefix/include/"
			;;
		nasm)
			pushd "$build/$component" >/dev/null
			phase configure "$sources/nasm/configure" --prefix="$prefix"
			phase compile make -j "$jobs"
			phase install make DESTDIR="$stage" install
			popd >/dev/null
			;;
	esac
}

verify_outputs() {
	local directory=$1 archive
	for archive in "${archives[@]}"; do
		[[ -s "$directory/lib/$archive" ]] || return 1
	done
	if [[ "$build_kind" == nasm ]]; then [[ -x "$directory/bin/nasm" ]] || return 1; fi
}

fingerprint_started=$SECONDS
common_signature=$(toolchain_signature | digest)
printf 'Toolchain fingerprint: %s (%ss)\n' "$common_signature" "$((SECONDS - fingerprint_started))"
declare -A signatures
components=(zlib-ng zstd bzip2 bzip3 libbsc xz lz4 brotli libdeflate)
if [[ "$arch" == amd64 ]]; then components+=(nasm); fi
components+=(isal libarchive parallel kanzi wim)

for component in "${components[@]}"; do
	source_name=$component
	build_kind=cmake
	cmake_source="$sources/$component"
	inputs=() dependencies=() archives=() cmake_options=()
	"recipe_${component//-/_}"
	signature=$(component_signature | digest)
	signatures[$component]=$signature
	marker="$state/$component.complete"
	manifest="$state/$component.manifest"
	manifest_valid=false
	reason='no completion marker'
	started=$SECONDS
	if [[ -f "$marker" ]]; then
		read -r previous_signature manifest_digest < "$marker" || true
		if [[ -s "$manifest" ]] && [[ "$(digest < "$manifest")" == "$manifest_digest" ]]; then manifest_valid=true; fi
		if [[ "$previous_signature" != "$signature" ]]; then
			reason='source, recipe, dependency or toolchain changed'
		elif [[ "$manifest_valid" != true ]]; then
			reason='missing or corrupt installed-file manifest'
		elif ! verify_outputs "$prefix" || ! (cd "$prefix" && sha256sum --check --status "$manifest"); then
			reason='missing or corrupt installed files'
		else
			printf '[%s] cache hit; verified successfully (%ss)\n' "$component" "$((SECONDS - started))"
			continue
		fi
	fi
	printf '[%s] rebuild: %s\n' "$component" "$reason"
	printf 'changed\n' > "$build/cache-changed"
	# Invalidate before touching any installed output. A failed retry cannot hit.
	rm -f "$marker"
	rm -rf "$build/$component" "$build/stage-$component"
	stage="$build/stage-$component"
	mkdir -p "$build/$component" "$stage"
	log="$build/$component.log"
	: > "$log"
	build_component
	verify_outputs "$stage$prefix"
	# Only installed outputs are cached, never object files or CMake build trees.
	(cd "$stage$prefix" && find . \( -type f -o -type l \) -print0 | sort -z | xargs -0 -r sha256sum) > "$manifest.tmp"
	[[ -s "$manifest.tmp" ]]
	# Only trust verified ownership when removing outputs dropped by a newer version.
	if [[ "$manifest_valid" == true ]]; then
		while read -r checksum file; do
			case "$file" in
				./include/*|./lib/*|./bin/*|./share/*)
					if [[ "$file" != *'/../'* ]]; then rm -f "$prefix/$file"; fi
					;;
			esac
		done < "$manifest"
	fi
	cp -a "$stage$prefix/." "$prefix/"
	(cd "$prefix" && sha256sum --check --status "$manifest.tmp")
	mv "$manifest.tmp" "$manifest"
	printf '%s %s\n' "$signature" "$(digest < "$manifest")" > "$marker.tmp"
	mv "$marker.tmp" "$marker"
	rm -rf "$stage"
	printf '[%s] completed successfully: %ss\n' "$component" "$((SECONDS - started))"
done

printf 'ready\n' > "$build/cache-ready"
printf 'Native libraries ready: %s/%s\n' "$target" "$arch"
