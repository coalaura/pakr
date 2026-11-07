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
iso     -          Fast       None
cpio    -          Fast       None
ar/deb  -          Fast       None
lha     -          Varies     Varies
rpm     -          Varies     Varies
Z       -          Fast       Modest
lzma    -          Moderate   Very high
bz2     Slow       Moderate   Moderate-high
bz3     Moderate   Moderate   Very high
bsc     Moderate   Moderate   Very high
knz     Fast       Fast       High-very high
xz      Slow       Moderate   Very high
lz      Slow       Moderate   Very high
7z      Slow       Moderate   Very high
br      Fast       Fast       High

Choosing a format
  - Default: tar.zst
  - Fastest compression: tar.lz4
  - Broad desktop compatibility: zip
  - Raw codecs take one file; use tar.<codec> for multiple files or folders.

Format notes
  - A dash under Compress means extraction-only.
  - Encrypted archives are unsupported.
  - ZIP/ZIPX: Deflate64 and MP3/JPEG/WavPack methods are unsupported.
  - RAR: no multipart or RAR4 solid archives; RAR5 solid support is limited.
  - CAB: no multipart or Quantum-compressed cabinets.
  - ISO: no UDF images.
  - Compressed CPIO: cpio.gz, cpio.bz2, cpio.xz and cpio.zst.
  - DEB: outer AR members only. RPM: CPIO payload only.
  - LHA/LZH: legacy filename encodings may be unsupported on static Unix builds.
  - .Z: truncation cannot always be detected.
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
