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

Kanzi also accepts a raw byte stream from stdin: `pakr pack - -f knz -o -`. Use `tar.knz` (alias `tknz`) when packing files or directories into a TAR archive.

### Unpacking

`pakr unpack <archive | ->` extracts into the current directory unless `-o` specifies a destination. It detects formats from the extension, then recognizable signatures, including ISO9660 volume descriptors and CPIO inside gzip/BZIP2/XZ/ZSTD. Specify `-f` for Brotli, LHA/LZH or legacy LZMA without an extension, or compressed tar from stdin or a file without a recognized extension.

Explicit and automatically detected sequential codec/tar formats stream directly from stdin; automatic detection probes compressed input for CPIO without spooling ordinary streams. ZIP/ZIPX, RAR, 7z, BSC and the legacy libarchive containers/streams require temporary-file spooling for extraction. Raw streams extract to the input filename with its final extension removed or to `stdin` for piped input. Raw Kanzi also supports decompression to stdout with `pakr unpack input.knz -o -`; extraction limits and stream validation still apply.

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
| `-o`, `--output` | Output archive or `-` for stdout when packing; destination directory or `-` for raw Kanzi stdout when unpacking |
| `-f`, `--format` | Explicit format or alias |
| `-t`, `--threads` | Worker budget: `auto` (default), `all` or 1-256 |
| `--memory` | BSC / BZIP3 / Kanzi / Lzip / WIM / ESD allocation budget, also LZMA-Alone packing: `auto` (default) or a positive byte count / `KiB`, `MiB`, `GiB`, `TiB` size |
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

Unpacking-specific flags are listed under [Extraction limits](#extraction-limits). WIM and ESD also accept `--image N` to select a 1-based image index; selection is required for multi-image archives.

## Formats

| Format | Compression | Decompression |
| --- | --- | --- |
| `tar` | Go [`archive/tar`](https://github.com/golang/go/tree/master/src/archive/tar) | Go [`archive/tar`](https://github.com/golang/go/tree/master/src/archive/tar) |
| `gz`, `gzip` | [ISA-L](https://github.com/intel/isa-l) at levels 1-4; [zlib-ng](https://github.com/zlib-ng/zlib-ng) at levels 5-9 | [zlib-ng](https://github.com/zlib-ng/zlib-ng) for small/piped streams; [rapidgzip](https://github.com/mxmlnkn/rapidgzip) for large files |
| `zst`, `zstd` | [libzstd](https://github.com/facebook/zstd) | [libzstd](https://github.com/facebook/zstd) |
| `bz2`, `bzip2` | [libbz2](https://sourceware.org/git/?p=bzip2.git), parallel concatenated streams | [libbz2](https://sourceware.org/git/?p=bzip2.git) for small/piped streams; [indexed_bzip2](https://github.com/mxmlnkn/rapidgzip) for large files |
| `bz3`, `bzip3` | [libbzip3](https://github.com/iczelia/bzip3), parallel independent blocks | [libbzip3](https://github.com/iczelia/bzip3), parallel independent blocks |
| `bsc` | [libbsc](https://github.com/IlyaGrebnov/libbsc), parallel independent blocks | [libbsc](https://github.com/IlyaGrebnov/libbsc), parallel indexed blocks |
| `knz` | [Kanzi C++ 2.6.0](https://github.com/flanglet/kanzi-cpp), native parallel blocks | [Kanzi C++ 2.6.0](https://github.com/flanglet/kanzi-cpp), native parallel blocks |
| `xz`, `lzma2` | [liblzma](https://github.com/tukaani-project/xz), XZ container | [liblzma](https://github.com/tukaani-project/xz) |
| `lz`, `lzip` | [liblzma](https://github.com/tukaani-project/xz) raw LZMA1, Lzip v1 container | [liblzma](https://github.com/tukaani-project/xz), including concatenated members |
| `zip` | [Go ZIP container](https://github.com/golang/go/tree/master/src/archive/zip); [libdeflate](https://github.com/ebiggers/libdeflate) or streaming [zlib-ng](https://github.com/zlib-ng/zlib-ng) | [Go ZIP container](https://github.com/golang/go/tree/master/src/archive/zip); [libdeflate](https://github.com/ebiggers/libdeflate) or streaming [zlib-ng](https://github.com/zlib-ng/zlib-ng); [libarchive](https://github.com/libarchive/libarchive) for advanced methods |
| `zipx` | Extraction only | ZIP container with advanced methods through [libarchive](https://github.com/libarchive/libarchive) |
| `rar` | Extraction only | RAR4/RAR5 through [libarchive](https://github.com/libarchive/libarchive) |
| `cab` | Extraction only | CAB Store, MSZIP and LZX through [libarchive](https://github.com/libarchive/libarchive) |
| `iso` | [libarchive](https://github.com/libarchive/libarchive), ISO9660 level 2 with Rock Ridge and Joliet | ISO9660, Rock Ridge and Joliet through [libarchive](https://github.com/libarchive/libarchive) |
| `cpio`, `cpio.gz`, `cpio.bz2`, `cpio.xz`, `cpio.zst` | [libarchive](https://github.com/libarchive/libarchive), SVR4 newc with existing compression backends | CPIO newc, crc, odc and binary through [libarchive](https://github.com/libarchive/libarchive) |
| `ar`, `a` | [libarchive](https://github.com/libarchive/libarchive), BSD AR with extended names | GNU/BSD AR through [libarchive](https://github.com/libarchive/libarchive) |
| `deb` | Extraction only | DEB outer AR members through [libarchive](https://github.com/libarchive/libarchive) |
| `lha`, `lzh` | Extraction only | Supported LHA methods through [libarchive](https://github.com/libarchive/libarchive) |
| `rpm` | Extraction only | RPM's compressed CPIO payload through [libarchive](https://github.com/libarchive/libarchive) |
| `Z` | [libarchive](https://github.com/libarchive/libarchive), Unix compress LZW | Unix compress through [libarchive](https://github.com/libarchive/libarchive) |
| `lzma` | [liblzma](https://github.com/tukaani-project/xz), LZMA-Alone | LZMA-Alone through [libarchive](https://github.com/libarchive/libarchive) |
| `lz4` | [liblz4](https://github.com/lz4/lz4) multithreaded frame API | [liblz4](https://github.com/lz4/lz4) frame API |
| `7z` | [libarchive](https://github.com/libarchive/libarchive) with LZMA2 | [libarchive](https://github.com/libarchive/libarchive) |
| `wim` | wimlib 1.14.5, non-solid XPRESS (levels 1-2) or LZX (levels 3-9), 32 KiB chunks | wimlib 1.14.5, verified resources and contained publication |
| `esd` | wimlib 1.14.5, solid LZMS, 64 MiB chunks | Same WIM reader; the two formats share a signature |
| `br`, `brotli` | [libbrotlienc](https://github.com/google/brotli) | [libbrotlidec](https://github.com/google/brotli) |

### Tar combinations and aliases

The following raw codecs can be combined with tar (`tar.Z` and `tar.lzma` are not supported):

| Format | Short aliases |
| --- | --- |
| `tar.gz` | `tgz` |
| `tar.zst` | `tzst` |
| `tar.bz2` | `tbz`, `tbz2` |
| `tar.bz3` | `tbz3` |
| `tar.bsc` | `tbsc` |
| `tar.knz` | `tknz` |
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

### WIM and ESD

`pakr pack documents photos -o backup.wim` creates one image with the usual input-path semantics. Use `.esd` for solid LZMS compression, or `-f wim` / `-f esd` explicitly. `pakr unpack backup.esd -o restored --image 2` selects an image; single-image archives select it automatically. Pakr does not append, update, mount or deploy images.

Standard WIM and solid ESD images containing ordinary Windows files, directories, hardlinks and ACL/security descriptors can be extracted for their file contents. This includes Windows captures with hidden/system files and empty DACLs, provided the selected image has no unsupported content-bearing entries. Full Windows installation/recovery images are not universally supported: junctions, alternate streams or other unsupported entries cause the entire extraction to fail.

Files, directories, hardlinks, safe relative symlinks, modification times and available ordinary Unix permissions are supported. Without Unix permissions, files default to `0644` and directories to `0755`, with write bits removed for read-only entries; the host OS determines which permission bits can be restored. As with other formats, ownership and ACLs are not restored. Capture excludes ACLs; extraction accepts stored security descriptors but does not apply them. Creation/access times, DOS 8.3 aliases, object IDs and hidden/system/temporary attributes are also omitted. Compressed and sparse file contents are decoded normally, without preserving their storage layout.

Windows symlink extraction requires a direct, existing target in the selected image; dangling links and symlink chains are rejected so directory-link types are not guessed. Absolute or escaping symlinks, junctions and unknown reparse tags are rejected. Junctions lack the explicit relative-target flag used by supported symlinks, so NT-namespace targets are not translated into filesystem paths. Named alternate streams (including empty ones), extended attributes, EFS encryption, offline files and devices are rejected rather than silently losing data or creating empty substitutes. Split, encrypted and nonstandard pipable WIMs are unsupported. Paths must be portable, without case collisions or reserved Windows names; directory depth is limited to 256.

Extraction preflights the selected tree and limits, verifies resource hashes (and integrity tables when present), then publishes through `os.Root` without overwriting. Native decoding writes only generated blob names in a private staging directory, never archive paths or links. Failed publication removes entries created by that job. Staging needs disk space for unique uncompressed resources in addition to final output. Output directories must not already contain the archive's paths, including directories. Capture checks the scanned manifest again and fails if entries change type, size or membership; callers should keep inputs stable during capture.

Standard WIMs require seeking. Stdout packing completes a temporary archive before copying it; stdin extraction uses bounded disk spooling. The stdin bound is the smaller of `--max-output` and 16 TiB (16 TiB when the limit is disabled), plus 12.5% and 256 MiB for framing. This bound applies to the whole input, including unselected images.

WIM/ESD use native compression workers, reducing their count to fit `--memory` without lowering compression effort. Budgets include native compressor estimates, chunk queues and conservative metadata allowances; a budget that cannot fit one worker fails. ESD's 64 MiB chunks can require substantial memory. Actual worker counts are reported when writing; small inputs may use fewer workers. Extraction has one native decoder. WIM jobs within one process are serialized because native compression defaults are global. Allocation estimates are not a hard RSS cap.

### Legacy archives and packages

CAB, DEB, LHA/LZH and RPM are extraction-only, including when inferred from the output filename. ISO9660, CPIO, AR, `.Z` and `.lzma` also support creation. These formats use the existing statically linked libarchive 3.8.11 and liblzma without runtime executables. Files, directories, safe links and available permission/modification-time metadata use the same contained, no-overwrite extractor and limits as other archives.

- CAB supports Store, MSZIP and LZX. Multipart cabinets, Quantum and encrypted entries fail explicitly.
- ISO9660 creation writes level 2 data images with Rock Ridge (UTF-8 names, Unix modes/ownership and safe symlinks) and Joliet. Rock Ridge preserves original names; Joliet has a separate name view limited to 64 BMP characters per component, with invalid characters replaced and truncated collisions disambiguated. Readers without Rock Ridge see symlinks as empty regular files and cannot recover Unix modes/ownership. Names must use Unicode BMP characters for standard Joliet compatibility; its generated paths must also fit libarchive's 240-byte limit. Files must be smaller than 4 GiB minus 2 KiB; directory depth is limited to seven levels below the root, with at most 65,534 directories. Rock Ridge components are limited to 255 bytes and paths to 4095 bytes; timestamps must be in 1900-2155 and have second precision. Libarchive stages contents in a temporary file, requiring disk space proportional to the input in addition to the output image. Extraction supports Rock Ridge and Joliet, with volume-size validation. UDF, boot-image creation, mounting and special device entries are unsupported.
- CPIO creation writes SVR4 newc with regular files, directories, safe symlinks, Unix modes/ownership and second-resolution timestamps. File sizes and nonnegative Unix timestamps must fit unsigned 32-bit fields; the root name `TRAILER!!!` is reserved. Extraction supports newc, checksum-bearing crc, odc and both binary byte orders, including payload-last hardlinks. Compressed CPIO is detected even with only `.gz`, `.bz2`, `.xz` or `.zst` extensions; an explicit raw codec format overrides that detection. Trailers are required; only up to 1 MiB of zero padding after the trailer is accepted, not concatenated CPIO archives. CPIO and ISO creation store hardlinked input files as independent files.
- AR creation accepts only flat regular files, preserving Unix modes/ownership and second-resolution modification times, using BSD extended names for long filenames. Numeric fields must fit AR's decimal widths (10 digits for stored size, 12 for nonnegative timestamps, six for owner/group IDs). `.a` is a generic archive, without a linker symbol index. Extraction supports GNU string tables and BSD extended names. DEB extracts the actual `debian-binary`, `control.tar.*` and `data.tar.*` members without recursively extracting or installing them; DEB creation remains unsupported.
- RPM extracts the CPIO payload, using gzip, BZIP2, XZ, ZSTD or legacy LZMA filters. It does not install packages, verify signatures or access an RPM database.
- LHA/LZH compatibility follows libarchive's methods. Unicode names are supported; legacy codepages depend on the platform. Static Unix builds have no iconv dependency and reject unsupported encodings instead of silently changing names.
- `.Z` and LZMA-Alone compress one regular file and extract one raw file, including empty streams. Both encoders are single-threaded; `.Z` has no level setting, while LZMA-Alone uses the XZ preset mapping and respects `--memory`. Invalid headers and decoder failures are rejected; these formats have no payload checksum, so not every corruption is detectable. In particular, `.Z` has no uncompressed-size or end marker and cannot reliably detect truncation. LZMA trailing data is rejected. Use `-f lzma` or `-f lha` for piped/extensionless input.

## Performance and memory

Levels represent effort rather than equivalent settings across codecs; higher settings may need substantially more memory. The thread budget is an upper bound for eligible work and some codecs remain single-threaded.

`--memory` applies to BSC, BZIP3, Kanzi, Lzip, WIM and ESD for both packing and unpacking, and to LZMA-Alone packing. Other codecs use fixed budgets. These are allocation budgets, not whole-process RSS guarantees; other parallel decoder indexes and libarchive decoders are outside them. WIM/ESD include conservative metadata allowances. Libarchive's XZ/LZMA stream filters have a fixed 1 GiB decoder-memory ceiling. BSC's block index is included in its budget.

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
| Kanzi | Native levels 1-9, unchanged; 64-bit block checksums |
| WIM | XPRESS 25, 50; then LZX 25, 50, 75, 100, 125, 150, 175 |
| ESD | LZMS 20, 40, 60, 80, 100, 120, 140, 160, 180 |
| Kanzi block size (MiB) | 4, 4, 4, 4, 4, 8, 16, 16, 32 |
| XZ / 7z LZMA2 / LZMA-Alone | 0, 1, 3, 4, 5, 6, 7, 8, 9 |
| Lzip dictionary (MiB) | 1, 1.5, 2, 3, 4, 8, 16, 24, 32 |
| Lzip nice match length | 32, 32, 32, 32, 32, 64, 128, 163, 273 |

Zero and negative settings still compress; they do not select store mode. Zstandard -3 and LZ4 acceleration 5 (frame level -4) are practical fast endpoints, rather than their much broader acceleration limits.

</details>

### Automatic memory budgets

For BSC, BZIP3, Kanzi, Lzip, WIM, ESD and LZMA-Alone packing, `--memory auto` uses **one quarter of the smallest reliable available-memory estimate, capped at 8 GiB**. The budget is refreshed whenever an encoder or decoder is initialized.

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

Kanzi writes the official headered version-7 bitstream and reads version-7 streams from both upstream C++ and Go tools, including their other transform/entropy combinations. Older bitstream versions and concatenated streams are rejected. Native levels retain LZ, BWT, ROLZ, CM, TPAQ and TPAQX strategies; level 3 remains the default. Checksums, header integrity, declared sizes and stream termination are checked before reporting successful extraction.

Kanzi admission reserves block buffers, transform tables, entropy state, growth copies and native worker overhead. Workers are capped at 64 and reduced to fit `--memory` and the known block count. The selected level and block size are never reduced; a budget too small for one worker fails before block/model allocation. Decompression performs the same preflight using the validated stream header. Estimates are conservative, particularly for small or easily compressed inputs. Level 9 uses 32 MiB blocks and requires substantially more memory per worker than the lower levels. Status output reports admitted jobs and reserved memory. Cancellation is checked between I/O operations and native transform/entropy phases; a running native phase must finish before cancellation can complete.

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

Direct external-tool integration tests require `gzip`, `uncompress`, `zstd`, `bzip2`, `bzip3`, `bsc`, `xz`, `lzip`, `lz4`, `brotli` and `bsdtar` on `PATH`. Set `BZIP3_CLI`, `BSC_CLI` or `LZIP_CLI` to an official executable outside `PATH` if needed. WIM/ESD interoperability tests use `PAKR_WIMLIB_IMAGEX`; after the Linux native build, `bash scripts/wimlib/reference.sh` builds the pristine pinned upstream CLI at `.native/build/wimlib-reference/wimlib-imagex`. CI runs bidirectional XPRESS, LZX and solid LZMS checks and image selection against it. Small reference fixtures, including upstream Windows-security and extended-attribute samples, test metadata mapping, safe links and rejection without partial output on all three platforms. Legacy creation checks also use GNU `cpio`, GNU `ar` and `xorriso`; these may skip locally when absent but are required with `CI=true`. BSD `cpio` and LLVM `ar` are additional optional readers. CI installs and checks the required tools on Linux, including independent Rock Ridge and Joliet extraction with `xorriso`.

Tests cover native backends, independently generated fixtures, archive round trips, truncated streams, Unicode paths, overwrite refusal, path traversal and CLI pipelines. The integration-only `TestBzip3BlockLevels` exercises native allocations at every level and needs several GiB of free memory.

The test workflow compiles every test package with `-tags=integration` and `GOEXPERIMENT=cgocheck2`, then runs the full suite on Linux, Windows and macOS amd64 runners. Linux reference tools generate fixtures that all three runners decode. Each runner exports its own compressed streams and archives; final Linux jobs independently verify every expected output from each OS, failing on missing artifacts. The same native tests exercise final-write cancellation, callback errors and short writes, ISO staging failures and cleanup, and LZMA finalization on all three platforms. Windows enables native symlinks; its archive checks use Windows permission bits and omit case-distinct sibling names. macOS uses a case-sensitive temporary volume for filename collision tests. arm64 targets have release cross-build coverage but no runtime CI job.

Fixture exchange is test-only: `PAKR_INTEROP_MODE=prepare` generates fixtures in `PAKR_INTEROP_REFERENCES`; `platform` reads those fixtures and writes archives to `PAKR_INTEROP_OUTPUTS`; `verify` reads both directories and uses `PAKR_INTEROP_OS` (`linux`, `windows` or `darwin`) for the producer's filesystem expectations. Use absolute directory paths. Without a mode, integration tests invoke reference tools directly as before.

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

- Support PPMd for packing and unpacking.
- Investigate [fast-lzma2](https://github.com/conor42/fast-lzma2) for XZ / 7z improvements.

## Third-party software

See [THIRD_PARTY.md](THIRD_PARTY.md) for dependency attribution and license locations. BZIP3 is statically linked under LGPLv3 and wimlib under LGPL-2.1-or-later; the [licensing and relinking section](THIRD_PARTY.md#bzip3-and-wimlib-licensing-and-relinking) grants the necessary application-code permissions and explains how to rebuild with a modified library.

Every binary release must provide the matching `pakr-source-<version>.tar.gz` with application and dependency sources, license texts and rebuild materials. The executable remains a single binary; the source bundle is a distribution obligation, not a runtime dependency.
