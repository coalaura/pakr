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

Stdin is streamed directly for sequential formats; ZIP/ZIPX, RAR and 7z require temporary-file spooling. Raw streams extract to the input filename with its final extension removed or to `stdin` for piped input.

Extraction defaults to at most 16 TiB of total payload, 1 TiB per file and 10 million entries, including directories and links. Adjust these with `--max-output`, `--max-file-size` and `--max-entries`; `0` disables the corresponding limit. Byte limits accept integer bytes or `KiB`, `MiB`, `GiB` and `TiB` suffixes, for example `pakr unpack backup.zip -o restored --max-output 32TiB --max-file-size 2TiB`. Declared sizes are checked before extraction and streams without sizes are checked as they produce output. ZIP workers share one budget. Exceeding a limit cancels extraction and removes incomplete files; already completed entries remain. Payload limits exclude container headers/padding and do not impose a whole-process RSS limit on native decoders or metadata.

### Options

Flags may follow input paths.

| Flag | Description |
| --- | --- |
| `-o`, `--output` | Output archive or `-` for stdout when packing; destination directory when unpacking |
| `-f`, `--format` | Explicit format or alias |
| `-t`, `--threads` | Worker budget: `auto` (default), `all` or 1-256 |
| `-l`, `--level` | Compression effort: 1-9, default 3; packing only |
| `-p`, `--preset` | `fast` (1), `balanced` (3) or `best` (9); exclusive with `--level` |
| `--max` | Maximum practical effort: all logical CPUs within codec resource budgets, level 9 and XZ's extreme preset; explicit `--threads`, `--level` or `--preset` settings override their respective defaults regardless of argument order; packing only |
| `--seekable` | Opt-in official seekable Zstandard output for `zst` and `tar.zst`; packing only |
| `--memory` | Codec allocation budget for packing and unpacking (BZIP3 and Lzip): `auto` (default) or positive bytes / a `KiB`, `MiB`, `GiB`, `TiB` size |
| `--max-output` | Total extracted payload limit, default `16TiB`; unpacking only |
| `--max-file-size` | Individual extracted file limit, default `1TiB`; unpacking only |
| `--max-entries` | Archive entry limit, default `10000000` (10 mil); unpacking only |
| `-q`, `--quiet` | Hide status, progress and errors; failures still return a nonzero exit code |
| `--formats` | Compare formats, speeds and size savings in a compact table, then exit |
| `-v`, `--version` | Print the version |

`--threads auto` follows Go's concurrency setting (`GOMAXPROCS`), while `--threads all` uses all logical CPUs visible to the process. Both are capped at 256 workers.

## Formats

| Format | Compression | Decompression |
| --- | --- | --- |
| `tar` | Go `archive/tar` | Go `archive/tar` |
| `gz`, `gzip` | ISA-L at levels 1-4; zlib-ng at levels 5-9 | zlib-ng for small/piped streams; rapidgzip for large files |
| `zst`, `zstd` | libzstd | libzstd |
| `bz2`, `bzip2` | libbz2, parallel concatenated streams | libbz2 for small/piped streams; indexed_bzip2 for large files |
| `bz3`, `bzip3` | libbzip3, parallel independent blocks | libbzip3, parallel independent blocks |
| `xz`, `lzma2` | liblzma, XZ container | liblzma |
| `lz`, `lzip` | liblzma raw LZMA1, Lzip v1 container | liblzma, including concatenated members |
| `zip` | Go ZIP container; libdeflate or streaming zlib-ng | Go ZIP container; libdeflate or streaming zlib-ng; libarchive for advanced methods |
| `zipx` | Extraction only | ZIP container with advanced methods through libarchive |
| `rar` | Extraction only | RAR4/RAR5 through libarchive |
| `lz4` | liblz4 multithreaded frame API | liblz4 frame API |
| `7z` | libarchive with LZMA2 | libarchive |
| `br`, `brotli` | libbrotlienc | libbrotlidec |

Every raw codec can be combined with tar: `tar.gz`, `tar.zst`, `tar.bz2`, `tar.bz3`, `tar.xz`, `tar.lz`, `tar.lz4` and `tar.br`. Aliases include `tgz`, `tbz`, `tbz2`, `tbz3`, `txz`, `tlz` and `tzst`. Lzip (`.lz` or `.lzip`) is distinct from XZ, LZ4 and legacy `.lzma`. `lzma2` selects an XZ container, not a headerless LZMA2 stream or legacy `.lzma` file. Use `pakr pack file -o file.lz`, `pakr pack directory -o backup.tar.lz` or `pakr pack directory -f tlz -o - | pakr unpack - -f tar.lz -o restored`.

ZIPX is an extended ZIP container. Both `.zip` and `.zipx` accept Store (0), Deflate (8), BZIP2 (12), LZMA (14), ZSTD (93), XZ (95) and PPMd (98), including mixed-method archives. Store/Deflate entries retain the optimized parallel ZIP path; advanced entries use libarchive sequentially after central-directory path and size preflight. Unsupported methods, including Deflate64, MP3 (94), JPEG (96) and WavPack (97), fail explicitly. Encrypted entries are rejected. ZIP64 and self-extracting ZIP offsets are supported where the underlying readers accept them; advanced-method symbolic links are limited to those libarchive can decode.

RAR versions are detected from their signatures; use `pakr unpack archive.rar -o restored` or `pakr unpack - -f rar -o restored`. Single-volume RAR4/RAR5 files, directories, Unicode names and supported links are extracted with available permissions and modification times. Compatibility is limited by libarchive: encrypted archives, multipart/split volumes, RAR4 solid compression and unsupported RAR5 solid variants fail. Neighboring volumes are never opened. An end header is required to detect truncation, so legacy RAR4 archives without one are rejected. RAR4 names without a Unicode field are interpreted as UTF-8; legacy OEM codepages are not supported. ZIPX and RAR creation is rejected whether selected with `-f` or inferred from the output filename.

Levels represent effort rather than equivalent settings across codecs; higher settings may need substantially more memory. The thread budget is an upper bound for eligible work and some codecs remain single-threaded. Gzip uses parallel, independent 1 MiB members in a standard concatenated stream. Gzip and ZIP compression cap in-flight workers at 16 to bound buffering.

The public levels 1 through 9 select these native settings (3 remains the balanced default):

| Backend | Native settings for public levels 1, 2, 3, 4, 5, 6, 7, 8, 9 |
| --- | --- |
| Zstandard | -3, 1, 3, 6, 9, 12, 16, 19, maximum (currently 22) |
| Brotli | 0, 1, 3, 4, 5, 6, 8, 10, 11 |
| ZIP libdeflate | 1, 2, 3, 4, 5, 7, 9, 10, 12 |
| LZ4 | -4, -1, 0, 3, 5, 7, 9, 10, maximum HC (currently 12) |
| GZIP | ISA-L 0, 1, 2, 3; then zlib-ng 5, 6, 7, 8, 9 |
| BZIP2 / streaming ZIP DEFLATE | 1, 2, 3, 4, 5, 6, 7, 8, 9 |
| BZIP3 block size (MiB) | 1, 4, 16, 32, 64, 128, 256, 384, 511 |
| XZ / 7z LZMA2 | 0, 1, 3, 4, 5, 6, 7, 8, 9 |
| Lzip dictionary (MiB) | 1, 1.5, 2, 3, 4, 8, 16, 24, 32 |
| Lzip nice match length | 32, 32, 32, 32, 32, 64, 128, 163, 273 |

Zero and negative settings in this table still compress; they do not select store mode. Zstandard -3 and LZ4 acceleration 5 (frame level -4) are practical fast endpoints, rather than their much broader acceleration limits. Explicit `--level 9` or `--preset best` overrides `--max`'s XZ extreme setting. Libarchive's LZMA2 writer and Brotli are single-threaded.

Ordinary ZIP entries up to 8 MiB use reusable libdeflate contexts; larger entries stream through zlib-ng. ZIP extraction validates conflicting paths before dispatching independent files and creates ordinary ZIP symlinks after file workers finish. Zstandard and XZ reduce native encoder worker counts using a 2 GiB memory estimate; BZIP2 and LZ4 bound worker buffers/state to approximately 256 MiB. These are codec working-memory budgets, not whole-process RSS guarantees; archive metadata, parallel decoder indexes and libarchive's 7z, advanced ZIP and RAR decoders are outside these budgets. XZ decoding permits up to 2 GiB and reduces parallelism above 512 MiB; ordinary Zstandard decoding limits windows to 1 GiB.

`--memory auto` resolves a fresh budget when a BZIP3 or Lzip encoder or decoder is initialized: one quarter of the smallest reliable available-memory estimate, capped at 8 GiB. This leaves approximately 75% for the OS, other processes and allocations outside the codec budget. If every probe fails, the fallback is 256 MiB; a detected low value, including zero, is never raised to a minimum. For example, 512 MiB of headroom gives a 128 MiB budget, 4 GiB gives 1 GiB and 64 GiB gives the capped 8 GiB. Explicit sizes bypass detection. Other codecs retain the fixed budgets described above and do not probe memory.

Linux uses physical `MemAvailable` and remaining cgroup v1/v2 memory limits, resolving the process's membership, mount paths and applicable visible ancestors; swap is excluded. Windows uses available physical RAM, available commit and queryable Job Object process/job limits, conservatively subtracting peak job-accounted usage. macOS uses free and inactive VM pages, falls back to free pages under reported memory pressure and considers measurable address-space limits. Inaccessible limits, cgroup ancestors hidden by a namespace/mount and unqueryable ancestor Windows jobs cannot be accounted for. These are snapshots: concurrent allocations, changing limits and imperfect reclaimability estimates mean the budget is not a hard process-memory limit or a guarantee against OOM.

BZIP3 uses the official `BZ3v1` format: a 9-byte file header and ordered, CRC-checked independent blocks. Encoding and decoding reuse one native state and one in-place buffer per active worker, with bounded queues and deterministic output. Each worker reserves `bz3_min_memory_needed(block) + bz3_bound(block) + 2*block + 1MiB`, plus a shared 1 MiB allowance. This includes worst-case libsais scratch and queued results. Workers are reduced to fit the resolved budget, but the requested block size is never silently reduced. If one worker cannot fit, initialization fails before native state allocation and reports the required and available budget. Level 9 or `--max` needs approximately 4.02 GiB even for one worker; where appropriate, override automatic selection with, for example, `pakr pack data -f bz3 --max --memory 8GiB`. The decoder applies the same preflight to the file's declared block size. Direct BZIP3 API calls with a zero memory argument use the same automatic policy.

Empty BZIP3 output is just a header. Inputs of at most 65 KiB declare a 65 KiB block and use at most one worker, avoiding large decoder allocations; the requested level's budget is still checked first. Larger inputs retain the selected block size. Native block operations finish before cancellation can reclaim their state; blocking input/output also depends on the underlying stream. Incomplete headers/blocks, invalid lengths and CRC failures are rejected. The format has no end marker or total-length field, so truncation exactly between complete blocks cannot be detected. BZIP2 and BZIP3 are distinct formats.

Lzip uses fixed `lc=3`, `lp=0`, `pb=2`, fast mode with HC3 at level 1, fast mode with HC4 at level 2, and normal mode with BT4 at levels 3-9; liblzma selects match-search depth automatically. `--preset` selects the usual normalized level; `--max` selects level 9 without an additional Lzip extreme mode. Encoding produces one member and remains single-threaded. Known raw input sizes cap dictionaries at the smallest representable size covering the input, at least 4 KiB, without limiting useful history; unknown-size streams and TAR retain the selected dictionary. Encoder and decoder estimates from liblzma, plus a 4 MiB streaming allowance, must fit `--memory` before expensive native allocations. Settings are never reduced to fit the budget; failure reports required and available bytes. Each concatenated member is checked against the same decoder budget, with legal dictionaries from 4 KiB through 512 MiB. Headers must be version 1, payloads must terminate with the LZMA1 end marker, and CRC32, uncompressed size and compressed member size must match. Truncation and trailing garbage are errors, including after TAR's end marker. Cancellation is checked between bounded native steps; blocking I/O still depends on the underlying stream.

`--seekable` writes independent 8 MiB Zstandard frames followed by the official seek table. Ordinary Zstandard tools can decode the result. Encoder contexts and native workers are reused between frames. Pakr detects the table in files and decodes frames with reusable contexts and up to eight workers, reducing concurrency to keep retained frame buffers within 256 MiB. Single-worker buffered decoding avoids worker goroutines; piped input still decodes sequentially. The writer spools only the seek index to a temporary file. External frames with compressed or decoded sizes above 65 MiB use sequential streaming. Independent frames can reduce compression ratio and ordinary Zstandard can still be faster, so seekable output remains opt-in.

Packing retains one metadata record per entry for deterministic ordering, complete preflight checks and accurate progress totals. A single input tree does not need a separate duplicate-path set; multiple input roots retain that check. Collection remains proportional to entry count rather than using a second traversal or disk-backed index.

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
- Regular files, directories and internal relative symlinks are supported; tar and supported RAR hardlinks are accepted during extraction. Symlinks on Windows require OS permission.
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

Integration tests require `gzip`, `zstd`, `bzip2`, `bzip3`, `xz`, `lzip`, `lz4`, `brotli` and `bsdtar` on `PATH`. Set `BZIP3_CLI` or `LZIP_CLI` to an official executable outside `PATH` if needed. Tests exercise the native backends, independently generated fixtures, archive round trips, truncated streams, Unicode paths, overwrite refusal, path traversal and CLI pipelines. Benchmarks report throughput, Go allocations and output/input size; benchmark representative data on your deployment platform when choosing settings.

The integration-only `TestBzip3BlockLevels` exercises native allocations at every level and needs several GiB of free memory. For separate encode/decode throughput, compressed size, process peak RSS and 1/4-worker comparisons against the official CLI and pakr's BZIP2, Zstandard and XZ, run `python3 scripts/bench-bzip3.py --pakr "$PWD/dist/pakr-linux-amd64" --bzip3 /path/to/bzip3` on Linux with Python 3.9+ and a C compiler available as `cc`. It prints CSV using 64 MiB synthetic text, repeated repository source, mixed source/random and incompressible corpora, with three-run medians. Temporary inputs and outputs are removed automatically; file I/O and process startup are included and Go allocation counters do not measure native memory.

For Lzip, run `python3 scripts/bench-lzip.py --pakr "$PWD/dist/pakr-linux-amd64" --lzip /path/to/lzip --plzip /path/to/plzip`. This uses the same measurement helper with 4 KiB and 64 MiB text, source, mixed and random inputs at levels 1, 3 and 9, matching reference dictionary sizes and match lengths (encoder algorithms still differ). Optional plzip comparisons use one and four workers to expose independent-member speed, memory and ratio tradeoffs. `BenchmarkLzip` separately measures in-memory raw encoding/decoding and Go allocations without process or filesystem overhead.

## TODOs

- Add [BSC](https://github.com/IlyaGrebnov/libbsc) support.
- Add [Kanzi](https://github.com/flanglet/kanzi-cpp) support (LZ, BWT, ROLZ).
- Add CAB extraction.
- Add ISO9660 extraction.
- Add WIM / ESD extraction.
- Support PPMd for packing and unpacking.
- Investigate [fast-lzma2](https://github.com/conor42/fast-lzma2) for XZ / 7z improvements.

## Third-party software

See [THIRD_PARTY.md](THIRD_PARTY.md) for dependency attribution and license locations. BZIP3 is statically linked under LGPLv3. Every binary release must also provide the matching `pakr-source-<version>.tar.gz` containing application and dependency sources, license texts and rebuild materials. The [BZIP3 licensing and relinking section](THIRD_PARTY.md#bzip3-licensing-and-relinking) grants the necessary application-code permissions and explains how to rebuild with a modified library. The executable remains a single binary; the source bundle is a distribution obligation, not a runtime dependency.
