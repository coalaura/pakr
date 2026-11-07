package main

import "io"

const formatsOverview = `Format  Compress   Extract    Savings
------  ---------  ---------  ---------------
tar     Very fast  Very fast  None
lz4     Very fast  Very fast  Modest
zst     Fast       Very fast  High
gz      Fast       Fast       Moderate
zip     Fast       Fast       Moderate
zipx    -          Varies     Varies
rar     -          Varies     Varies
cab     -          Varies     Varies
iso     Fast       Fast       None
cpio    Very fast  Fast       None
ar      Very fast  Fast       None
deb     -          Fast       None
lha     -          Varies     Varies
rpm     -          Varies     Varies
Z       Fast       Fast       Modest
lzma    Slow       Moderate   Very high
bz2     Slow       Moderate   Moderate-high
bz3     Moderate   Moderate   Very high
bsc     Moderate   Moderate   Very high
knz     Fast       Fast       High-very high
xz      Slow       Moderate   Very high
lz      Slow       Moderate   Very high
7z      Slow       Moderate   Very high
wim     Moderate   Fast       High
esd     Slow       Moderate   Very high
br      Fast       Fast       High

Choosing a format
  - Default: tar.zst
  - Fastest compression: tar.lz4
  - Broad desktop compatibility: zip
  - Raw codecs take one file; use an archive for multiple files or folders.

Format notes
  - A dash under Compress means extraction-only.
  - Encrypted archives are unsupported.
  - ZIP/ZIPX: Deflate64 and MP3/JPEG/WavPack methods are unsupported.
  - RAR: no multipart or RAR4 solid archives; RAR5 solid support is limited.
  - CAB: no multipart or Quantum-compressed cabinets.
  - WIM: XPRESS at levels 1-2, LZX at 3-9; ESD: solid LZMS, 64 MiB chunks.
  - WIM/ESD: file archives, not Windows deployment; multi-image input needs --image N.
  - ISO creation: level 2 with Rock Ridge and Joliet; no UDF or boot images.
  - CPIO creation: SVR4 newc; also cpio.gz, cpio.bz2, cpio.xz and cpio.zst.
  - AR/.a creation: flat regular files, BSD long names, no library symbol index.
  - DEB: outer AR members only. RPM: CPIO payload only.
  - LHA/LZH: legacy filename encodings may be unsupported on static Unix builds.
  - .Z and LZMA-Alone: no payload checksum; .Z truncation may be undetectable.
  - Piped or extensionless Brotli, LHA, LZMA and compressed tar require --format.

Compression settings
  - Ratings assume level 3 and vary with input, threads and hardware. Higher levels can be much slower.
  - Level 9 selects each backend's strongest practical settings; --max also enables XZ extreme within resource budgets.
  - --seekable enables independent frames and parallel file extraction for zst and tar.zst.
`

func showFormats(writer io.Writer) error {
	_, err := io.WriteString(writer, formatsOverview)

	return err
}
