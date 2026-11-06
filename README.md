<picture>
	<source media="(prefers-color-scheme: dark)" srcset=".github/banner.svg">
	<source media="(prefers-color-scheme: light)" srcset=".github/banner-light.svg">
	<img alt="pakr - loose files bundled by one band, with arrows for packing and unpacking archives." src=".github/banner-light.svg">
</picture>

A single-binary CLI for packing and unpacking archives, with native compression, consistent flags and concise progress output.

[Installation](#installation) | [Quick start](#quick-start) | [Usage](#usage) | [Formats](#formats) | [Performance and memory](#performance-and-memory) | [Building](#building-from-source)

## Installation

Download binaries for **Linux, Windows and macOS**, in **amd64 and arm64**, from [GitHub Releases](https://github.com/coalaura/pakr/releases).

On Linux and macOS, install the latest release with:

```sh
curl -fsSL https://src.ws2.sh/pakr/install.sh | sh
```

The installer detects your platform, verifies the release's SHA-256 checksum and installs to `/usr/local/bin/pakr`, using `sudo` when needed. It requires `curl` and either `sha256sum` or `shasum`.

### Manual installation

Choose `pakr-<os>-<arch>` (`darwin` for macOS), make it executable and place it on your `PATH` as `pakr`. On Windows, download `pakr-windows-<arch>.exe` and rename it to `pakr.exe`. Releases include a `SHA256SUMS` file.

## Quick start

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

## Usage

### Packing

`pakr pack <paths...>` infers the format from the output filename or defaults to `tar.zst`. Without an output path, it writes `archive.<format>`. Directories retain their basename; passing `.` archives the current directory's contents.

Raw compression formats such as `gz` accept exactly one regular file. Use a container such as `tar.gz`, `zip` or `7z` for multiple files or directories. Use `-o -` to write archive bytes to stdout.

### Unpacking

`pakr unpack <archive | ->` extracts into the current directory unless `-o` specifies a destination. It detects formats from the extension, then recognizable magic bytes. Specify `-f` for Brotli without an extension or compressed tar from stdin or a file without a recognized extension.

Stdin is streamed directly for sequential formats; ZIP/ZIPX, RAR, 7z and BSC require temporary-file spooling. Raw streams extract to the input filename with its final extension removed or to `stdin` for piped input.

### Extraction limits

| Flag | Default | Limits |
| --- | --- | --- |
| `--max-output` | `16TiB` | Total extracted payload |
| `--max-file-size` | `1TiB` | Payload per file |
| `--max-entries` | `10000000` | Entries, including directories and links |

Set a limit to `0` to disable it. Byte limits accept integer bytes or `KiB`, `MiB`, `GiB` and `TiB` suffixes.

```sh
pakr unpack backup.zip -o restored --max-output 32TiB --max-file-size 2TiB
```

Declared sizes are checked before extraction; streams without sizes are checked as they produce output. ZIP workers share one budget. Exceeding a limit cancels extraction and removes incomplete files; already completed entries remain.

Payload limits exclude container headers and padding. They do not impose a whole-process memory (RSS) limit on native decoders or metadata.

### Options

Flags may follow input paths.

#### Common options

| Flag | Description |
| --- | --- |
| `-o`, `--output` | Output archive or `-` for stdout when packing; destination directory when unpacking |
| `-f`, `--format` | Explicit format or alias |
| `-t`, `--threads` | Worker budget: `auto` (default), `all` or 1-256 |
| `--memory` | BSC / BZIP3 / Lzip codec allocation budget: `auto` (default) or a positive byte count / `KiB`, `MiB`, `GiB`, `TiB` size |
| `-q`, `--quiet` | Hide status, progress and errors; failures still return a nonzero exit code |
| `--formats` | Compare formats, speeds and size savings in a compact table, then exit |
| `-v`, `--version` | Print the version |

`--threads auto` follows Go's concurrency setting (`GOMAXPROCS`); `--threads all` uses all logical CPUs visible to the process. Both are capped at 256 workers.

#### Packing options

| Flag | Description |
| --- | --- |
| `-l`, `--level` | Compression effort: 1-9, default 3 |
| `-p`, `--preset` | `fast` (1), `balanced` (3) or `best` (9); exclusive with `--level` |
| `--max` | All logical CPUs within codec budgets, level 9 and XZ's extreme preset |
| `--seekable` | Official seekable Zstandard output for `zst` and `tar.zst` |

Explicit `--threads`, `--level` or `--preset` settings override their respective `--max` defaults regardless of argument order. Explicit `--level 9` or `--preset best` also overrides XZ's extreme setting.

Unpacking-specific flags are listed under [Extraction limits](#extraction-limits).

## Formats

| Format | Compression | Decompression |
| --- | --- | --- |
| `tar` | Go `archive/tar` | Go `archive/tar` |
| `gz`, `gzip` | ISA-L at levels 1-4; zlib-ng at levels 5-9 | zlib-ng for small/piped streams; rapidgzip for large files |
| `zst`, `zstd` | libzstd | libzstd |
| `bz2`, `bzip2` | libbz2, parallel concatenated streams | libbz2 for small/piped streams; indexed_bzip2 for large files |
| `bz3`, `bzip3` | libbzip3, parallel independent blocks | libbzip3, parallel independent blocks |
| `bsc` | libbsc, parallel independent blocks | libbsc, parallel indexed blocks |
| `xz`, `lzma2` | liblzma, XZ container | liblzma |
| `lz`, `lzip` | liblzma raw LZMA1, Lzip v1 container | liblzma, including concatenated members |
| `zip` | Go ZIP container; libdeflate or streaming zlib-ng | Go ZIP container; libdeflate or streaming zlib-ng; libarchive for advanced methods |
| `zipx` | Extraction only | ZIP container with advanced methods through libarchive |
| `rar` | Extraction only | RAR4/RAR5 through libarchive |
| `lz4` | liblz4 multithreaded frame API | liblz4 frame API |
| `7z` | libarchive with LZMA2 | libarchive |
| `br`, `brotli` | libbrotlienc | libbrotlidec |

### Tar combinations and aliases

Every raw codec can be combined with tar:

| Format | Short aliases |
| --- | --- |
| `tar.gz` | `tgz` |
| `tar.zst` | `tzst` |
| `tar.bz2` | `tbz`, `tbz2` |
| `tar.bz3` | `tbz3` |
| `tar.bsc` | `tbsc` |
| `tar.xz` | `txz` |
| `tar.lz` | `tlz` |
| `tar.lz4` | - |
| `tar.br` | - |

Lzip (`.lz` or `.lzip`) is distinct from XZ, LZ4 and legacy `.lzma`. The `lzma2` alias selects an XZ container, not a headerless LZMA2 stream or legacy `.lzma` file. BZIP2 and BZIP3 are also distinct formats.

### ZIPX and RAR compatibility

ZIPX and RAR are extraction-only. Creation is rejected whether selected with `-f` or inferred from the output filename.

<details>
<summary>ZIP / ZIPX methods and limitations</summary>

ZIPX is an extended ZIP container. Both `.zip` and `.zipx` accept Store (0), Deflate (8), BZIP2 (12), LZMA (14), ZSTD (93), XZ (95) and PPMd (98), including mixed-method archives.

Store/Deflate entries use the optimized parallel ZIP path. Advanced entries use libarchive sequentially after central-directory path and size preflight.

Unsupported methods, including Deflate64, MP3 (94), JPEG (96) and WavPack (97), fail explicitly. Encrypted entries are rejected. ZIP64 and self-extracting ZIP offsets are supported where the underlying readers accept them; advanced-method symbolic links are limited to those libarchive can decode.

</details>

<details>
<summary>RAR versions and limitations</summary>

```sh
pakr unpack archive.rar -o restored
pakr unpack - -f rar -o restored
```

Versions are detected from their signatures. Single-volume RAR4/RAR5 files, directories, Unicode names and supported links are extracted with available permissions and modification times.

Compatibility is limited by libarchive: encrypted archives, multipart/split volumes, RAR4 solid compression and unsupported RAR5 solid variants fail. Neighboring volumes are never opened.

An end header is required to detect truncation, so legacy RAR4 archives without one are rejected. RAR4 names without a Unicode field are interpreted as UTF-8; legacy OEM codepages are not supported.

</details>

## Performance and memory

Levels represent effort rather than equivalent settings across codecs; higher settings may need substantially more memory. The thread budget is an upper bound for eligible work and some codecs remain single-threaded.

`--memory` applies to BSC, BZIP3 and Lzip for both packing and unpacking. Other codecs use fixed budgets. These are codec working-memory budgets, not whole-process RSS guarantees; metadata, other parallel decoder indexes and libarchive's 7z, advanced ZIP and RAR decoders are outside them. BSC's block index is included in its budget.

### Compression levels

<details>
<summary>Native settings for levels 1-9</summary>

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
| BSC block size (MiB) | 1, 4, 8, 16, 25, 32, 48, 64, 64 |
| BSC entropy coder | fast, fast, static, static, static, adaptive, adaptive, adaptive, adaptive |
| BSC LZP hash bits / match length | 15/72, 15/72, 15/72, 16/72, 16/72, 17/72, 18/72, 18/72, 18/128 |
| BSC preprocessing | levels 8-9 auto-detect sorting contexts; level 9 also detects and reorders structured records |
| XZ / 7z LZMA2 | 0, 1, 3, 4, 5, 6, 7, 8, 9 |
| Lzip dictionary (MiB) | 1, 1.5, 2, 3, 4, 8, 16, 24, 32 |
| Lzip nice match length | 32, 32, 32, 32, 32, 64, 128, 163, 273 |

Zero and negative settings still compress; they do not select store mode. Zstandard -3 and LZ4 acceleration 5 (frame level -4) are practical fast endpoints, rather than their much broader acceleration limits.

</details>

### Automatic memory budgets

For BSC, BZIP3 and Lzip, `--memory auto` uses **one quarter of the smallest reliable available-memory estimate, capped at 8 GiB**. The budget is refreshed whenever an encoder or decoder is initialized.

| Available memory | Codec budget |
| --- | --- |
| 512 MiB | 128 MiB |
| 4 GiB | 1 GiB |
| 64 GiB | 8 GiB (cap) |

This leaves approximately 75% for the OS, other processes and allocations outside the codec budget. If every probe fails, the fallback is 256 MiB; a detected low value, including zero, is never raised to a minimum.

Explicit sizes such as `--memory 8GiB` bypass detection. Other codecs do not probe memory.

<details>
<summary>Platform memory detection and limitations</summary>

- **Linux:** physical `MemAvailable` and remaining cgroup v1/v2 memory limits, resolving process membership, mount paths and applicable visible ancestors. Swap is excluded.
- **Windows:** available physical RAM, available commit and queryable Job Object process/job limits, conservatively subtracting peak job-accounted usage.
- **macOS:** free and inactive VM pages, falling back to free pages under reported memory pressure, plus measurable address-space limits.

Inaccessible limits, cgroup ancestors hidden by a namespace/mount and unqueryable ancestor Windows jobs cannot be accounted for.

These are snapshots, not a hard process-memory limit or a guarantee against OOM. Concurrent allocations, changing limits and imperfect reclaimability estimates can affect available memory.

</details>

### Codec details

<details>
<summary>Worker limits, fixed budgets and archive metadata</summary>

| Codec / operation | Behavior |
| --- | --- |
| Gzip compression | Parallel, independent 1 MiB members in a standard concatenated stream; at most 16 in-flight workers |
| ZIP compression | At most 16 in-flight workers; ordinary entries up to 8 MiB use reusable libdeflate contexts, larger entries stream through zlib-ng |
| Zstandard / XZ encoding | Native worker counts reduced using a 2 GiB memory estimate |
| BZIP2 / LZ4 | Worker buffers and state bounded to approximately 256 MiB |
| XZ decoding | Up to 2 GiB; parallelism reduced above 512 MiB |
| Ordinary Zstandard decoding | Decoder windows limited to 1 GiB |
| Libarchive LZMA2 / Brotli encoding | Single-threaded |

ZIP extraction validates conflicting paths before dispatching independent files. Ordinary ZIP symlinks are created after file workers finish.

Packing retains one metadata record per entry for deterministic ordering, complete preflight checks and accurate progress totals. A single input tree does not need a separate duplicate-path set; multiple input roots retain that check. Collection remains proportional to entry count, without a second traversal or disk-backed index.

</details>

<details>
<summary>BSC format, memory requirements and validation</summary>

BSC reads and writes the official libbsc 3.4.0 `bsc1` file format, including its little-endian block count, original offsets, record sizes, sorting contexts and 28-byte native block headers. CUDA, OpenMP and build-machine-specific CPU optimization are disabled in the static library; libbsc retains its supported runtime CPU paths. Pakr schedules independent block operations in Go and preserves output order without nested native thread pools.

All BSC levels use BWT with LZP enabled. The mapping above was selected from measurements on text, source code, mixed data, random data and structured records. Fast QLFC is useful at levels 1-2, static QLFC is the balanced default and adaptive QLFC is used at higher levels. Larger blocks and preprocessing are workload-dependent, so they are not assumed to improve every input.

**Memory and workers**

Workers are reduced to fit the budget without reducing the requested compression level. Each BSC worker reserves `8*block + 16MiB + 4*2^hash` bytes, plus a shared 16 MiB allowance and 32 bytes per indexed block. This conservatively covers BWT/ST, LZP, preprocessing, QLFC and Go I/O buffers; actual use is lower for stored and small blocks. An impossible single-worker configuration is rejected before native worker allocation. Archive-declared block sizes and LZP hash settings receive the same preflight. Empty input has only the eight-byte BSC header and no native block work.

**Seeking and validation**

The block count is patched through pakr's existing seekable temporary output. Stdout therefore uses bounded-memory block processing and temporary-file staging rather than a nonstandard count-at-end container. Decoding indexes seekable input so physically reordered official blocks are restored by their original offsets; stdin is spooled only for BSC's required random access. The index is proportional to block count and included in the memory preflight.

Missing, duplicate, overlapping and noncontiguous output regions, impossible sizes or counts, invalid preprocessing metadata, trailing data, truncation, checksum failures and native decode errors are rejected. Pakr also bounds native QLFC, LZP and ST decoding reads and writes against validated block buffers. Cancellation waits for active native calls before releasing their state; blocking I/O still depends on the underlying file.

`--preset fast`, `balanced` and `best`, and `--max`, select the same public levels as other codecs. Explicit level and thread settings retain their usual override behavior.

</details>

<details>
<summary>BZIP3 blocks, memory requirements and validation</summary>

BZIP3 uses the official `BZ3v1` format: a 9-byte file header and ordered, CRC-checked independent blocks.

**Memory and workers**

Encoding and decoding reuse one native state and one in-place buffer per active worker, with bounded queues and deterministic output. Each worker reserves `bz3_min_memory_needed(block) + bz3_bound(block) + 2*block + 1MiB`, plus a shared 1 MiB allowance. This includes worst-case libsais scratch and queued results.

Workers are reduced to fit the budget; the requested block size is never silently reduced. If one worker cannot fit, initialization fails before native state allocation and reports required and available memory. The decoder applies the same preflight to the file's declared block size. Direct BZIP3 API calls with a zero memory argument use the automatic policy.

Level 9 or `--max` needs approximately **4.02 GiB even for one worker**. Where appropriate, override automatic selection:

```sh
pakr pack data -f bz3 --max --memory 8GiB
```

**Small inputs**

Empty output is just a header. Inputs of at most 65 KiB declare a 65 KiB block and use at most one worker, avoiding large decoder allocations; the requested level's budget is still checked first. Larger inputs retain the selected block size.

**Validation and cancellation**

Incomplete headers/blocks, invalid lengths and CRC failures are rejected. The format has no end marker or total-length field, so truncation exactly between complete blocks cannot be detected.

Native block operations finish before cancellation can reclaim their state. Blocking I/O also depends on the underlying stream.

</details>

<details>
<summary>Lzip encoding, dictionaries and validation</summary>

```sh
pakr pack file -o file.lz
pakr pack directory -o backup.tar.lz
pakr pack directory -f tlz -o - | pakr unpack - -f tar.lz -o restored
```

**Encoding**

Lzip uses fixed `lc=3`, `lp=0`, `pb=2`: fast mode with HC3 at level 1, fast mode with HC4 at level 2 and normal mode with BT4 at levels 3-9. Liblzma selects match-search depth automatically.

`--preset` selects the usual normalized level; `--max` selects level 9 without an additional extreme mode. Encoding produces one member and remains single-threaded.

Known raw input sizes cap dictionaries at the smallest representable size covering the input, at least 4 KiB, without limiting useful history. Unknown-size streams and TAR retain the selected dictionary.

**Memory**

Liblzma encoder/decoder estimates plus a 4 MiB streaming allowance must fit `--memory` before expensive native allocations. Settings are never reduced to fit the budget; failure reports required and available bytes.

Each concatenated member is checked against the same decoder budget. Legal dictionaries range from 4 KiB through 512 MiB.

**Validation and cancellation**

Headers must be version 1; payloads must terminate with the LZMA1 end marker. CRC32, uncompressed size and compressed member size must match. Truncation and trailing garbage are errors, including after TAR's end marker.

Cancellation is checked between bounded native steps. Blocking I/O still depends on the underlying stream.

</details>

<details>
<summary>Seekable Zstandard</summary>

`--seekable` writes independent 8 MiB Zstandard frames followed by the official seek table. Ordinary Zstandard tools can decode the result.

Encoder contexts and native workers are reused between frames. Only the seek index is spooled to a temporary file.

Pakr detects the seek table in files and uses reusable decoder contexts with up to eight workers, reducing concurrency to keep retained frame buffers within 256 MiB. Single-worker buffered decoding avoids worker goroutines. Piped input and external frames with compressed or decoded sizes above 65 MiB use sequential streaming.

Independent frames can reduce compression ratio and ordinary Zstandard can still be faster. Seekable output is opt-in.

</details>

## Status output

Status goes to stderr, keeping stdout clean for archive data. Packing previews up to three input paths; unpacking shows the archive and destination. Terminals also get a live progress line with a short phase label. Successful operations leave a summary:

```text
:: packed 1248 entries | 842.0 mib -> 219.0 mib (26.0%) | 3.2s | backup.tar.zst
:: unpacked 1248 entries | 842.0 mib | 1.8s | restored
```

Entries include directories and links; data sizes count regular file contents. The percentage is archive size relative to original data size, so values above 100% indicate expansion. Empty inputs show `(n/a)`.

Paths retain their case. On failure or cancellation, the live line is cleared before the error message. `--quiet` suppresses all status output, including errors.

## Filesystem behavior

- **No overwrites:** existing output archives and extracted files are never overwritten. Packing publishes through a no-clobber hard link, so the destination filesystem must support hard links.
- **Contained extraction:** Go's traversal-resistant `os.Root` rejects escaping paths and link targets. The current partial file is removed after failure; earlier entries may remain.
- **File types:** regular files, directories and internal relative symlinks are supported. Tar and supported RAR hardlinks are accepted during extraction. Symlinks on Windows require OS permission.
- **Metadata:** permission bits and modification times are preserved where supported. Ownership, ACLs and extended attributes are not supported.
- **Other limitations:** special files, encrypted archives and sparse-file optimization are not supported. Keep input files stable while packing.

## Building from source

Build from **Linux or WSL**.

| Requirements | Version / notes |
| --- | --- |
| Go | 1.27.2 or newer, on `PATH` |
| Zig | 0.17.0, on `PATH` |
| CMake | 3.24 or newer |
| Build tools | Bash, Make and a host C compiler |
| Utilities | curl, patch, tar, xz and standard Unix utilities including `sha256sum` |

```sh
git clone https://github.com/coalaura/pakr.git
cd pakr
bash scripts/build.sh linux amd64
```

The script downloads pinned, checksum-verified native sources, builds static libraries and compiles the binary with Go and Zig. NASM is built locally when needed; compression libraries do not need to be installed globally.

Outputs go to `dist/pakr-<os>-<arch>` with `.exe` on Windows. Sources and build caches stay in `.native/`; native build logs are under `.native/build/<os>-<arch>/`.

### Build targets

The first two arguments select `linux`, `windows` or `darwin` and `amd64` or `arm64`. Set `VERSION` to embed a release version:

```sh
VERSION=v1.2.3 bash scripts/build.sh windows amd64
```

- **Linux:** static musl linking.
- **Windows:** normal OS DLLs, including the Universal C Runtime in Windows 10 and newer.
- **macOS:** OS-provided system runtime. Cross-compilation requires an existing macOS SDK; set `SDKROOT` to its path.

Builds target baseline CPUs; native libraries can select accelerated routines at runtime.

### Tests and benchmarks

Tests must target the host OS and architecture. Additional arguments after `test` are passed to `go test`:

```sh
bash scripts/build.sh linux amd64 test
bash scripts/build.sh linux amd64 test -tags integration
bash scripts/build.sh linux amd64 test -run '^$' -bench . -benchmem
```

Integration tests require `gzip`, `zstd`, `bzip2`, `bzip3`, `xz`, `lzip`, `lz4`, `brotli` and `bsdtar` on `PATH`. Set `BZIP3_CLI` or `LZIP_CLI` to an official executable outside `PATH` if needed.

Tests cover native backends, independently generated fixtures, archive round trips, truncated streams, Unicode paths, overwrite refusal, path traversal and CLI pipelines. The integration-only `TestBzip3BlockLevels` exercises native allocations at every level and needs several GiB of free memory.

Benchmarks report throughput, Go allocations and output/input size. Use representative data on your deployment platform when choosing settings.

<details>
<summary>Process-level BZIP3 and Lzip benchmarks</summary>

Run on Linux with Python 3.9+ and a C compiler available as `cc`.

**BZIP3**

```sh
python3 scripts/bench-bzip3.py --pakr "$PWD/dist/pakr-linux-amd64" --bzip3 /path/to/bzip3
```

This compares one and four workers against the official CLI and pakr's BZIP2, Zstandard and XZ. It reports separate encode/decode throughput, compressed size and process peak RSS as CSV with three-run medians.

Inputs are 64 MiB synthetic text, repeated repository source, mixed source/random and incompressible corpora.

**Lzip**

```sh
python3 scripts/bench-lzip.py --pakr "$PWD/dist/pakr-linux-amd64" --lzip /path/to/lzip --plzip /path/to/plzip
```

This uses the same measurement helper with 4 KiB and 64 MiB text, source, mixed and random inputs at levels 1, 3 and 9. Reference dictionary sizes and match lengths are matched; encoder algorithms still differ.

Optional plzip comparisons use one and four workers to expose independent-member speed, memory and ratio tradeoffs. `BenchmarkLzip` separately measures in-memory raw encoding/decoding and Go allocations without process or filesystem overhead.

Temporary inputs and outputs are removed automatically. Process-level measurements include file I/O and startup; Go allocation counters do not measure native memory.

</details>

## TODOs

- Add [BSC](https://github.com/IlyaGrebnov/libbsc) support.
- Add [Kanzi](https://github.com/flanglet/kanzi-cpp) support (LZ, BWT, ROLZ).
- Add CAB extraction.
- Add ISO9660 extraction.
- Add WIM / ESD extraction.
- Support PPMd for packing and unpacking.
- Investigate [fast-lzma2](https://github.com/conor42/fast-lzma2) for XZ / 7z improvements.

## Third-party software

See [THIRD_PARTY.md](THIRD_PARTY.md) for dependency attribution and license locations. BZIP3 is statically linked under LGPLv3; the [licensing and relinking section](THIRD_PARTY.md#bzip3-licensing-and-relinking) grants the necessary application-code permissions and explains how to rebuild with a modified library.

Every binary release must provide the matching `pakr-source-<version>.tar.gz` with application and dependency sources, license texts and rebuild materials. The executable remains a single binary; the source bundle is a distribution obligation, not a runtime dependency.
