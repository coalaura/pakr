# Third-party components

Native source versions, URLs and checksums are recorded in `scripts/native.lock`. Source archives and their license texts are downloaded to `.native/src/` by `scripts/fetch.sh`.

| Component | License text in downloaded source |
| --- | --- |
| zlib-ng | `zlib-ng/LICENSE.md` (zlib) |
| Zstandard | `zstd/LICENSE` (BSD option) |
| xxHash (Zstandard seek-table checksums) | `zstd/lib/common/xxhash.h` (BSD-2-Clause notice) |
| bzip2 | `bzip2/LICENSE` |
| BZIP3 1.5.5 | `bzip3/LICENSE` and source headers (LGPL-3.0-or-later; distributed using LGPLv3) |
| libsais (included in BZIP3) | `bzip3/3rdparty/libsais-LICENSE` (Apache-2.0) |
| libbsc 3.4.0 | `libbsc/LICENSE` (Apache-2.0); authorship and additional credits in `libbsc/AUTHORS` |
| Kanzi C++ 2.6.0 | `kanzi/LICENSE` (Apache-2.0); copyright Frederic Langlet |
| liblzma | `xz/COPYING` and `xz/COPYING.0BSD` |
| Fast LZMA2 1.0.1 | `fast-lzma2/LICENSE` (BSD-2-Clause option selected; copyright Conor McCarthy) |
| xxHash (included in Fast LZMA2) | `fast-lzma2/xxhash.h` (BSD-2-Clause; copyright Yann Collet) |
| liblz4 1.11.1 | `lz4/lib/LICENSE` (BSD-2-Clause) |
| Brotli | `brotli/LICENSE` (MIT) |
| libarchive | `libarchive/COPYING`; individual source notices also apply |
| wimlib 1.14.5 (library only) | `wimlib/COPYING` and `wimlib/COPYING.LGPL` (LGPL-2.1-or-later option, without NTFS-3G) |
| libdeflate | `libdeflate/COPYING` (MIT) |
| ISA-L fork | `isal/LICENSE` (BSD-3-Clause) |
| rapidgzip 0.16.2 | `rapidgzip/LICENSE-MIT` and `rapidgzip/LICENSE-APACHE` |
| librapidarchive (rapidgzip and indexed_bzip2 implementation) | `librapidarchive/LICENSE-MIT` and `librapidarchive/LICENSE-APACHE` |
| NASM, build-time only | `nasm/LICENSE` (BSD-2-Clause) |

Go dependency versions and checksums are recorded in `go.mod`, `go.sum` and the separate `tools/toolchain` module. These include coalaura/plain, urfave/cli/v3, golang.org/x/sys, golang.org/x/term and the build-time coalaura/builder library. Go, Zig's runtime libraries, musl on Linux and the Windows runtime import libraries also contribute to the built executable.

Redistributed binaries must retain the applicable third-party copyright notices and license texts. Native source archives are unmodified except for the committed patches under `scripts/patches/`; the ISA-L dependency is explicitly pinned to the rapidgzip author's fork. The rapidgzip 0.16.2 archive omits its librapidarchive submodule: pakr separately fetches revision `59eb3317063f14a9b1b5cbc50989bba99992b7b7` and that revision's ISA-L fork `63bb128a3254ba05ec8b7b5abf19615b14a0436a`. Both original archives are included in the release source bundle. The `librapidarchive-*` patches retain bzip2 workspace reuse, lightweight header probes with reduced read-ahead, and sparse-window decoder storage off worker stacks.

Additional librapidarchive patches reject a header-only truncated gzip stream and resolve a `BYTE_SIZE` macro collision with Darwin's Mach headers. Bzip2 stream-CRC verification uses the upstream implementation.

`internal/pak/testdata/archives.json` contains base64-encoded ZIPX and RAR fixtures from [libarchive 3.8.11's tests](https://github.com/libarchive/libarchive/tree/v3.8.11/libarchive/test), under the libarchive license above. Keys preserve upstream filenames; decoded bytes match `libarchive/test/<name>.uu` in the source pinned by `scripts/native.lock`. RAR utility fixtures are independently generated, not produced by pakr; upstream does not record every producer version or command. Expected contents follow the upstream tests. Encrypted, multipart and malformed regression fixtures exercise rejection; security tests mutate copies without changing the originals.

`internal/pak/testdata/legacy.json` stores base64-encoded gzip envelopes around independent archive fixtures. CAB (`makecab.exe`), GNU AR, LHA, binary/Unicode CPIO, RPM and ISO fixtures come from the same libarchive tests (`test_read_format_<key>.uu`; ISO `.Z` envelopes are decoded first). `payload.Z`, `payload.cpio` and `empty-dir.cpio` come from the byte arrays in `test_read_format_cpio_svr4c_Z.c` and `test_read_format_cpio_odc.c`. The remaining snapshots were produced with GNU cpio (`-o -H newc`, `crc`, `odc`), gzip/bzip2/xz/zstd, bsdtar (`--format arbsd`, without stdout block padding) and xz (`--format=lzma`). DEB's three opaque members deliberately test outer-container extraction, not package validity. Security tests mutate copies; no fixture was encoded by pakr.

`scripts/patches/libarchive-extraction.patch` adds CPIO crc and gzip trailer validation, strict malformed/empty-stream error propagation, AR truncation checks, bounded CPIO trailer draining and XZ/LZMA filter memory limits. It also validates Unix compress headers and rejects trailing LZMA-Alone data. These checks retain libarchive's existing decoding algorithms.

## libbsc

libbsc 3.4.0 is authored by Ilya Grebnov, with additional contributors and algorithm credits listed in its `AUTHORS` file. Pakr statically links the Apache-2.0 library. Pakr's modification dated 2026-11-06, `scripts/patches/libbsc-bounds.patch`, adds checked decoding-buffer boundaries and malformed-input rejection; it does not change the format or encoder. The unmodified source and its license are included in release source archives.

## Kanzi

Kanzi C++ 2.6.0 by Frederic Langlet is statically linked under Apache-2.0. Pakr's modification dated 2026-11-07, `scripts/patches/kanzi-memory.patch`, bounds binary entropy buffers: expanding single-chunk encodes use the existing transformed-copy fallback, and decoding rejects payload lengths exceeding the enclosing block before allocation. The official bitstream remains unchanged. The pinned original source, license and patch are included in release source archives.

## BZIP3 and wimlib licensing and relinking

BZIP3 copyright (C) 2022-2024 Kamila Szewczyk. Pakr uses its low-level block API and statically links the library. Pakr's modification dated 2026-11-05, `scripts/patches/bzip3-allocation.patch`, checks native allocation failure before zeroing memory. The upstream 1.5.5 headers grant LGPLv3-or-later terms; pakr satisfies the LGPLv3 option, including the incorporated GPLv3 terms.

wimlib 1.14.5 is copyright Eric Biggers and the contributors identified in its source headers. Pakr statically links libwim under LGPL-2.1-or-later, choosing LGPL 2.1, without NTFS-3G, FUSE or the GPL command-line programs. Pakr's modification dated 2026-11-07, `scripts/patches/wimlib-strict-metadata.patch`, rejects malformed directory entries instead of silently ignoring them and bounds recursive metadata expansion. The adapter uses pinned internal resource-reading interfaces; it does not use wimlib's filesystem extraction. The original source, modification, build configuration and license texts accompany release binaries.

### Application-code permission

The pakr copyright holders grant recipients of pakr permission to use, reproduce and modify the supplied pakr application source and/or object code as needed to rebuild or relink pakr with a modified version of BZIP3 or wimlib and to install, execute and redistribute the resulting executable. Modification for the recipient's own use and reverse engineering for debugging modifications to either library are permitted. This permission applies to the application code owned by the pakr copyright holders; third-party components remain subject to their respective licenses. No other application license is granted by this notice.

This permission and the complete source and rebuilding instructions below provide for relinking under LGPLv3 section 4(d)(0) and LGPL 2.1 section 6(a). Preserve these permissions, notices and materials when redistributing.

### Release materials

Each release containing BZIP3 or wimlib must accompany its binaries with the matching `pakr-source-<version>.tar.gz` archive, not merely this notice or an upstream link. `scripts/source.sh <version>` includes pakr's application source, vendored Go dependencies, checksum-pinned native dependency source archives, patches, build scripts, license texts and the toolchain versions used. Releases publish this bundle alongside the executable. Keep this archive available alongside the corresponding binaries for as long as those binaries are offered.

The release source bundle includes the GPLv3, LGPLv3 and LGPL 2.1 texts, BZIP3's notices and wimlib's license selection and copyright notices. Library modifications must retain the applicable notices and comply with those terms.

### Rebuild with a modified library

Extract the source archive on Linux or WSL. Install the Go and Zig versions recorded in `TOOLCHAIN.txt`, together with the build prerequisites in README.md. For macOS targets, an appropriate macOS SDK is also required. Run `bash scripts/fetch.sh` to unpack and verify the included original native archives, then modify `.native/src/bzip3/`. Leave its `.pakr-*` marker in place so subsequent fetches preserve your changes. Remove `.native/<os>-<arch>/.complete-*` if rebuilding an already-built tree and run `VERSION=<version> bash scripts/build.sh <os> <arch>`. Go uses the vendored dependencies automatically; the toolchain helper also includes its own vendor directory.

The output is `dist/pakr-<os>-<arch>` (with `.exe` for Windows). It contains your modified library statically and can replace the original executable. No pakr signing key, activation service or signature check inside pakr is required. Rebuilt executables do not retain the publisher's signature. Any distributor imposing additional installation restrictions must provide the installation information required by LGPLv3 section 4(e), where applicable.

For wimlib, follow the same procedure but modify `.native/src/wimlib/` and remove `.native/<os>-<arch>/.wim-*` before rebuilding. Keep its `.pakr-*` marker. The included pakr source, vendored dependencies and native sources are the relinkable application materials; no proprietary object file or runtime DLL is needed.
