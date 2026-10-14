<picture>
	<source media="(prefers-color-scheme: dark)" srcset=".github/banner.svg">
	<source media="(prefers-color-scheme: light)" srcset=".github/banner-light.svg">
	<img alt="pakr - loose files bundled by one band, with arrows for packing and unpacking archives." src=".github/banner-light.svg">
</picture>

A single-binary CLI for packing and unpacking archives, with native compression, consistent flags and concise progress output.

## Installation

Download binaries for **Linux, Windows and macOS**, in **amd64 and arm64**, from [GitHub Releases](https://github.com/coalaura/pakr/releases).

On Linux and macOS, install the latest release with:

```sh
curl -fsSL https://src.ws2.sh/pakr/install.sh | sh
```

The installer detects your platform, verifies the release's SHA-256 checksum and installs to `/usr/local/bin/pakr`, using `sudo` when needed. It requires `curl` and either `sha256sum` or `shasum`.

For manual installation, choose `pakr-<os>-<arch>` (`darwin` for macOS), make it executable and place it on your `PATH`. On Windows, download `pakr-windows-<arch>.exe` and rename it to `pakr.exe`. Releases include a `SHA256SUMS` file.

## Usage

```sh
# Pack files and directories.
pakr pack documents photos -o backup.tar.zst
pakr pack project -f tgz -p fast -t 8

# Compress a single file.
pakr pack report.csv -o report.csv.gz

# Extract an archive.
pakr unpack backup.tar.zst -o restored

# Pipe an archive between commands.
pakr pack project -f tar.zst -o - | pakr unpack - -f tar.zst -o restored

pakr --version
```

### Packing

`pakr pack <paths...>` infers the format from the output filename or defaults to `tar.zst`. Without an output path, it writes `archive.<format>`. Directories retain their basename; passing `.` archives the current directory's contents.

Raw compression formats such as `gz` accept exactly one regular file. Use a container such as `tar.gz`, `zip` or `7z` for multiple files or directories. Use `-o -` to write archive bytes to stdout.

### Unpacking

`pakr unpack <archive | ->` extracts into the current directory unless `-o` specifies a destination. It detects formats from the extension, then recognizable magic bytes. Specify `-f` for Brotli without an extension or compressed tar from stdin or a file without a recognized extension.

Stdin is spooled to a temporary file for seekable decoders. Raw streams extract to the input filename with its final extension removed or to `stdin` for piped input.

### Options

Flags may follow input paths.

| Flag | Description |
| --- | --- |
| `-o`, `--output` | Output archive or `-` for stdout when packing; destination directory when unpacking |
| `-f`, `--format` | Explicit format or alias |
| `-t`, `--threads` | Worker budget: `auto` (default) or 1-256 |
| `-l`, `--level` | Compression effort: 1-9, default 3; packing only |
| `-p`, `--preset` | `fast` (1), `balanced` (3) or `best` (9); exclusive with `--level` |
| `-q`, `--quiet` | Hide status, progress and errors; failures still return a nonzero exit code |
| `-v`, `--version` | Print the version |

## Formats

| Format | Compression | Decompression |
| --- | --- | --- |
| `tar` | Go `archive/tar` | Go `archive/tar` |
| `gz`, `gzip` | ISA-L at levels 1-3; zlib-ng at levels 4-9 | rapidgzip with its ISA-L fork |
| `zst`, `zstd` | libzstd | libzstd |
| `bz2`, `bzip2` | libbz2 | indexed_bzip2 |
| `xz`, `lzma2` | liblzma, XZ container | liblzma |
| `zip` | Go ZIP container; libdeflate or streaming zlib-ng | Go ZIP container; libdeflate or streaming zlib-ng |
| `lz4` | liblz4 frame API | liblz4 frame API |
| `7z` | libarchive with LZMA2 | libarchive |
| `br`, `brotli` | libbrotlienc | libbrotlidec |

Every raw codec can be combined with tar: `tar.gz`, `tar.zst`, `tar.bz2`, `tar.xz`, `tar.lz4` and `tar.br`. Aliases include `tgz`, `tbz`, `tbz2`, `txz` and `tzst`. `lzma2` selects an XZ container, not a headerless LZMA2 stream or legacy `.lzma` file.

Levels represent effort rather than equivalent settings across codecs; higher settings may need substantially more memory. The thread budget is an upper bound for eligible work and some codecs remain single-threaded. Gzip uses parallel, independent 1 MiB members in a standard concatenated stream. Gzip and ZIP compression cap in-flight workers at 16 to bound buffering.

## Status output

Status goes to stderr, keeping stdout clean for archive data. Packing previews up to three input paths; unpacking shows the archive and destination. Terminals also get a live progress line with a short phase label. Successful operations leave a summary:

```text
:: packed 1248 entries | 842.0 mib -> 219.0 mib (26.0%) | 3.2s | backup.tar.zst
:: unpacked 1248 entries | 842.0 mib | 1.8s | restored
```

Entries include directories and links; data sizes count regular file contents. The percentage is archive size relative to original data size, so values above 100% indicate expansion. Empty inputs show `(n/a)`. Paths retain their case. On failure or cancellation, the live line is cleared before the error message. `--quiet` suppresses all status output.

## Filesystem behavior

- Existing output archives and extracted files are never overwritten. Packing publishes through a no-clobber hard link, so the destination filesystem must support hard links.
- Extraction uses Go's traversal-resistant `os.Root`, rejects escaping paths and link targets and removes the current partial file after failure. Entries extracted earlier may remain.
- Regular files, directories and internal relative symlinks are supported; tar hardlinks are accepted during extraction. Symlinks on Windows require OS permission.
- Permission bits and modification times are preserved where supported. Ownership, ACLs, extended attributes, special files, encrypted archives and sparse-file optimization are not supported. Keep input files stable while packing.

## Building from source

Build from **Linux or WSL** with Go 1.27.2 or newer, Zig 0.17.0, Bash, CMake 3.24 or newer, Make, a host C compiler, curl, patch, tar, xz and standard Unix utilities including `sha256sum`. Go and Zig must be on `PATH`.

```sh
git clone https://github.com/coalaura/pakr.git
cd pakr
bash scripts/build.sh linux amd64
```

The script downloads pinned, checksum-verified native sources, builds their static libraries and compiles the binary with Go and Zig. NASM is built locally when needed; compression libraries do not need to be installed globally. Outputs go to `dist/pakr-<os>-<arch>` with `.exe` on Windows. Sources and build caches stay in `.native/`; native build logs are under `.native/build/<os>-<arch>/`.

The first two arguments select `linux`, `windows` or `darwin` and `amd64` or `arm64`. Set `VERSION` to embed a release version:

```sh
VERSION=v1.2.3 bash scripts/build.sh windows amd64
```

Darwin cross-compilation requires an existing macOS SDK; set `SDKROOT` to its path. Linux binaries use static musl linking. Windows binaries use normal OS DLLs, including the Universal C Runtime in Windows 10 and newer. macOS binaries use the OS-provided system runtime. Builds target baseline CPUs, while native libraries can select accelerated routines at runtime.

### Tests and benchmarks

Tests must target the host OS and architecture. Additional arguments after `test` are passed to `go test`:

```sh
bash scripts/build.sh linux amd64 test
bash scripts/build.sh linux amd64 test -tags integration
bash scripts/build.sh linux amd64 test -run '^$' -bench . -benchmem
```

Integration tests require `gzip`, `zstd`, `bzip2`, `xz`, `lz4`, `brotli` and `bsdtar` on `PATH`. Tests exercise the native backends, archive round trips, truncated streams, Unicode paths, overwrite refusal, path traversal and CLI pipelines. Benchmarks report throughput, Go allocations and output/input size; benchmark representative data on your deployment platform when choosing settings.

## Third-party software

See [THIRD_PARTY.md](THIRD_PARTY.md) for dependency attribution and license locations.
