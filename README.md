# pakr

A single-binary pack/unpack CLI with native compression, consistent flags and terminal-aware progress.

```sh
pakr pack documents photos -o backup.tar.zst
pakr pack project -f tgz -p fast -t 8
pakr pack report.csv -f gz -o report.csv.gz
pakr unpack backup.tar.zst -o restored
pakr pack project -f tar.zst -o - | pakr unpack - -f tar.zst -o restored
pakr --version
```

## Commands and flags

`pakr pack <paths...>` packs files and directories. The format is inferred from `--output` or defaults to `tar.zst` when no output filename is given. The default filename is `archive.<format>`. Directories retain their basename; passing `.` archives its contents. Raw compression formats accept exactly one regular file; use `tar.<codec>` for multiple files or directories.

`pakr unpack <archive | ->` extracts into the current directory unless `--output` is specified. It infers the format from the extension, then recognizable magic bytes. Use `--format` for Brotli without an extension and compressed tar from stdin or without a recognized extension. Stdin is spooled to a temporary file so seekable parallel decoders and ZIP can use the same extraction path. Raw streams extract to the filename with its final extension removed or `stdin` for piped input.

| Flag | Meaning |
| --- | --- |
| `-v`, `--version` | Print `pakr version <version>` |
| `-o`, `--output` | Pack: output file or `-` for stdout. Unpack: destination directory |
| `-f`, `--format` | Explicit format or alias |
| `-t`, `--threads` | `auto` (default) or a worker budget from 1 to 256 |
| `-l`, `--level` | Pack: compression effort from 1 to 9; default 3 |
| `-p`, `--preset` | Pack: `fast` = 1, `balanced` = 3, `best` = 9; exclusive with `--level` |
| `-q`, `--quiet` | Hide status, progress and errors; failures still return a nonzero exit code |

Flags may follow input paths. Status is written to stderr, including when redirected; piped archive bytes on stdout remain clean. Packing previews up to three supplied input paths and the destination, with a count of additional inputs. Unpacking shows the archive and destination. Long preview paths are shortened; summaries retain the full destination and paths preserve their case.

Terminals also show live progress with a short phase label: `scan`, `read`, `pack`, `unpack` or `finish`. Packing displays a byte-based progress bar. Extraction displays bytes, throughput and entry count without inventing a total for streams whose expanded size is unknown. On success, the live line is replaced by a summary of entries, data size, elapsed time and destination; packing includes the archive size and compression ratio. The percentage beside the before and after sizes is archive bytes as a percentage of original file bytes, for example `842.0 mib -> 219.0 mib (26.0%)`; values above `100.0%` indicate expansion and empty inputs show `(n/a)`. Entries include directories and links, while data size counts regular file contents. On failure or interruption, the live line is cleared before a short error or `cancelled` message. Generated labels and units are lowercase and ASCII.

## Formats and backends

| Format | Compression | Decompression |
| --- | --- | --- |
| `tar` | Go `archive/tar` | Go `archive/tar` |
| `gz`, `gzip` | ISA-L at levels 1-3; zlib-ng at levels 4-9 | rapidgzip with its ISA-L fork |
| `zst`, `zstd` | libzstd | libzstd |
| `bz2`, `bzip2` | libbz2 | indexed_bzip2 |
| `xz`, `lzma2` | liblzma, XZ container | liblzma |
| `zip` | Go ZIP container; libdeflate, zlib-ng streaming fallback | Go ZIP container; libdeflate, zlib-ng streaming fallback |
| `lz4` | liblz4 frame API | liblz4 frame API |
| `7z` | libarchive with LZMA2 | libarchive |
| `br`, `brotli` | libbrotlienc | libbrotlidec |

Every raw codec can be combined with tar: `tar.gz`, `tar.zst`, `tar.bz2`, `tar.xz`, `tar.lz4` and `tar.br`. Aliases include `tgz`, `tbz`, `tbz2`, `txz` and `tzst`. `lzma2` selects XZ containing LZMA2, not a headerless LZMA2 stream or legacy `.lzma` format.

Levels express effort, not identical algorithms or compression ratios. Zstandard maps levels 1-9 to native levels 1, 2, 3, 5, 7, 9, 12, 16 and 19. LZ4 uses its fast mode at levels 1-3 and HC levels 7-12 above that. Brotli level 9 maps to quality 11. Bzip2, XZ and 7z use native levels 1-9. Higher settings can need substantially more memory.

Gzip compression uses independent 1 MiB gzip members ordered into a standard concatenated gzip stream. This allows bounded parallel compression, with some ratio cost compared with a single continuous dictionary. Gzip and ZIP compression cap their in-flight worker count at 16 to bound buffering. ZIP uses libdeflate for entries up to 8 MiB and a streaming encoder for larger entries. Zstandard and XZ use their native worker pools; gzip and bzip2 decoding use parallel readers. Single-stream bzip2 compression, Brotli, LZ4 and Zstandard decoding remain single-threaded. A thread budget is an upper bound for eligible work, not a guarantee that every codec uses that many workers.

The current gzip encoder does not include pigzpp. Backend choices and defaults are starting points to benchmark, not a claim that pakr already outperforms every native tool.

## Build from Linux or WSL

Build-time prerequisites: Go 1.27.2 or newer, `builder` v0.5.5, Zig, Bash, CMake 3.24 or newer, Make, a host C compiler, curl, patch, tar and standard Unix utilities including `sha256sum`. `builder` and Zig must be on `PATH`. NASM is built locally from pinned source when needed. No compression libraries need to be installed globally.

```sh
# Run these in Linux, including a WSL shell.
bash scripts/build.sh linux amd64
bash scripts/build.sh windows amd64

# Embed a release version.
VERSION=v1.2.3 bash scripts/build.sh windows amd64

# Run tests on the Linux host.
bash scripts/build.sh linux amd64 test
```

Outputs are `dist/pakr-linux-amd64` and `dist/pakr-windows-amd64.exe`. The pipeline downloads pinned, SHA-256-verified source archives, builds static libraries into `.native/<os>-<arch>` and invokes `builder` for final CGO compilation. Native compiler wrappers come from builder's `goenv` API so native libraries and Go use the same Zig targets. Linux uses musl. Windows links the codec and C++ runtimes statically and uses normal OS DLLs, including the Universal C Runtime supplied with Windows 10 and newer. `--compat` preserves baseline CPU compatibility; libraries can select accelerated code at runtime.

Commit the Go/C/C++ sources, shell scripts, patches and lockfiles. `.native/`, `dist/`, downloaded sources, compiler wrappers and built libraries are ignored. An initial build takes substantially longer than subsequent builds. Native build logs are under `.native/build/<os>-<arch>/`.

The scripts also accept `arm64` and `darwin`. Those targets need separate verification. Darwin cross-compilation requires an existing macOS SDK discoverable by builder; the script does not download one. Compression libraries can be static on macOS, but the executable still uses the OS-provided system runtime.

The pinned rapidgzip release requires its author's ISA-L fork for block-boundary decoding. `scripts/native.lock` pins that exact revision. The small libdeflate patch adapts its SIMD target attributes to Clang 22 while preserving runtime dispatch. rapidgzip's C++ bridge is compiled into a cached static library with its required Clang constexpr limit during the native build. After building the native libraries for a target, direct `builder build`, `builder run` and `builder test` commands with `--cgo` work without any CGO flag allowlist. Re-run `scripts/native.sh` for that target after changing the bridge source or native dependencies.

## Verification and performance

Tests cover every format, empty and multi-buffer streams, concatenated gzip/bzip2, truncated streams, Unicode paths, overwrite refusal, archive path traversal, ZIP batches and the CLI pipeline. They exercise the real native backends with CGO pointer checking enabled.

Verified on Linux amd64 under WSL and Windows amd64: 90 Linux tests/subtests including native-tool interoperability, 79 Windows tests/subtests and clean `vet` checks on both platforms. Interoperability checks cover both directions with the six reference compression tools and `bsdtar` for tar, ZIP and 7z, including Unicode names, empty directories and symlinks on Linux. Binary inspection confirms a static Linux ELF and only OS-provided DLL imports on Windows. Darwin and arm64 have not been verified.

```sh
builder test go --cgo --compat --no-gen ./...
vet --cgo --tests
builder bench go --cgo --compat --no-gen ./internal/pak

# Linux interoperability tests require gzip, zstd, bzip2, xz, lz4, brotli and bsdtar on PATH.
builder test go --cgo --compat --no-gen -tags integration ./...
```

The packing benchmark uses a deterministic 32 MiB mixed corpus, four workers, real output files and a native `tar` baseline when available. It reports throughput, Go allocations and output/input size. Native allocations are not included in Go's allocation counters. Native tar measurements include process startup. Results are warm-cache measurements and depend on the filesystem; benchmark production data on the deployment platform before choosing speed or ratio defaults.

## Filesystem behavior

Existing output archives and files are never overwritten. Packing uses a temporary file beside the destination and publishes it with a no-clobber hard link, so that destination filesystem must support hard links. Extraction uses Go's traversal-resistant `os.Root`, rejects escaping paths and link targets and removes the current partial file if extraction fails. Earlier successfully extracted entries can remain after a later error.

Regular files, directories and internal relative symlinks are supported; tar hardlinks are accepted during extraction. Symlink creation on Windows requires OS permission. Permission bits and modification times are preserved where supported. Ownership, ACLs, extended attributes, special files, encrypted archives and sparse-file optimization are not implemented. Keep input files stable while packing.

See [THIRD_PARTY.md](THIRD_PARTY.md) for dependency attribution and license locations.
