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
| liblzma | `xz/COPYING` and `xz/COPYING.0BSD` |
| liblz4 | `lz4/lib/LICENSE` (BSD-2-Clause) |
| Brotli | `brotli/LICENSE` (MIT) |
| libarchive | `libarchive/COPYING`; individual source notices also apply |
| libdeflate | `libdeflate/COPYING` (MIT) |
| ISA-L fork | `isal/LICENSE` (BSD-3-Clause) |
| rapidgzip and indexed_bzip2 | `rapidgzip/LICENSE-MIT` and `rapidgzip/LICENSE-APACHE` |
| NASM, build-time only | `nasm/LICENSE` (BSD-2-Clause) |

Go dependency versions and checksums are recorded in `go.mod`, `go.sum` and the separate `tools/toolchain` module. These include coalaura/plain, urfave/cli/v3, golang.org/x/sys, golang.org/x/term and the build-time coalaura/builder library. Go, Zig's runtime libraries, musl on Linux and the Windows runtime import libraries also contribute to the built executable.

Redistributed binaries must retain the applicable third-party copyright notices and license texts. Native source archives are unmodified except for the committed patches under `scripts/patches/`; the ISA-L dependency is explicitly pinned to the rapidgzip author's fork.

`internal/pak/testdata/archives.json` contains base64-encoded ZIPX and RAR fixtures from [libarchive 3.8.11's tests](https://github.com/libarchive/libarchive/tree/v3.8.11/libarchive/test), under the libarchive license above. Keys preserve upstream filenames; decoded bytes match `libarchive/test/<name>.uu` in the source pinned by `scripts/native.lock`. RAR utility fixtures are independently generated, not produced by pakr; upstream does not record every producer version or command. Expected contents follow the upstream tests. Encrypted, multipart and malformed regression fixtures exercise rejection; security tests mutate copies without changing the originals.

## BZIP3 licensing and relinking

BZIP3 copyright (C) 2022-2024 Kamila Szewczyk. Pakr uses its low-level block API and statically links the library. Pakr's modification dated 2026-11-05, `scripts/patches/bzip3-allocation.patch`, checks native allocation failure before zeroing memory. The upstream 1.5.5 headers grant LGPLv3-or-later terms; pakr satisfies the LGPLv3 option, including the incorporated GPLv3 terms.

### Application-code permission

The pakr copyright holders grant recipients of pakr permission to use, reproduce and modify the supplied pakr application source and/or object code as needed to rebuild or relink pakr with a modified version of BZIP3 and to install, execute and redistribute the resulting executable. Reverse engineering for debugging modifications to BZIP3 is permitted. This permission applies to the application code owned by the pakr copyright holders; third-party components remain subject to their respective licenses. No other application license is granted by this notice.

This permission and the rebuilding instructions below provide for relinking under LGPLv3 section 4(d)(0). Preserve these permissions, notices and materials when redistributing.

### Release materials

Each release containing BZIP3 must accompany its binaries with the matching `pakr-source-<version>.tar.gz` archive, not merely this notice or an upstream link. `scripts/source.sh <version>` includes pakr's application source, vendored Go dependencies, checksum-pinned native dependency source archives, patches, build scripts, license texts and the toolchain versions used. Releases publish this bundle alongside the executable. Keep this archive available alongside the corresponding binaries for as long as those binaries are offered.

The release source bundle includes both the GPLv3 and LGPLv3 texts and BZIP3's copyright notices. Library modifications must retain the applicable notices and comply with those terms.

### Rebuild with a modified BZIP3

Extract the source archive on Linux or WSL. Install the Go and Zig versions recorded in `TOOLCHAIN.txt`, together with the build prerequisites in README.md. For macOS targets, an appropriate macOS SDK is also required. Run `bash scripts/fetch.sh` to unpack and verify the included original native archives, then modify `.native/src/bzip3/`. Leave its `.pakr-*` marker in place so subsequent fetches preserve your changes. Remove `.native/<os>-<arch>/.complete-*` if rebuilding an already-built tree and run `VERSION=<version> bash scripts/build.sh <os> <arch>`. Go uses the vendored dependencies automatically; the toolchain helper also includes its own vendor directory.

The output is `dist/pakr-<os>-<arch>` (with `.exe` for Windows). It contains your modified library statically and can replace the original executable. No pakr signing key, activation service or signature check inside pakr is required. Rebuilt executables do not retain the publisher's signature. Any distributor imposing additional installation restrictions must provide the installation information required by LGPLv3 section 4(e), where applicable.
