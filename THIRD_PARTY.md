# Third-party components

Native source versions, URLs and checksums are recorded in `scripts/native.lock`. Source archives and their license texts are downloaded to `.native/src/` by `scripts/fetch.sh`.

| Component | License text in downloaded source |
| --- | --- |
| zlib-ng | `zlib-ng/LICENSE.md` (zlib) |
| Zstandard | `zstd/LICENSE` (BSD option) |
| bzip2 | `bzip2/LICENSE` |
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
