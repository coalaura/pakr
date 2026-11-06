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
bz2     Slow       Moderate   Moderate-high
bz3     Moderate   Moderate   Very high
bsc     Moderate   Moderate   Very high
xz      Slow       Moderate   Very high
lz      Slow       Moderate   Very high
7z      Slow       Moderate   Very high
br      Fast       Fast       High

Choosing a format
  - Default: tar.zst
  - Fastest compression: tar.lz4
  - Broad desktop compatibility: zip
  - Raw codecs take one file; use tar.<codec> for multiple files or folders.

Extraction-only formats
  - ZIPX (also advanced ZIP): Store, Deflate, BZIP2, LZMA, ZSTD, XZ and PPMd.
  - RAR4/RAR5: single-volume archives supported by libarchive; RAR4 solid and unsupported RAR5 solid variants are rejected.
  - Encryption, multipart RAR, MP3/JPEG/WavPack ZIP and Deflate64 are unsupported.

Compression settings
  - Ratings assume level 3 and vary with input, threads and hardware. Higher levels can be much slower.
  - Level 9 selects each backend's strongest practical settings; --max also enables XZ extreme within resource budgets.
  - --seekable enables independent frames and parallel file extraction for zst and tar.zst.
`

func showFormats(writer io.Writer) error {
	_, err := io.WriteString(writer, formatsOverview)

	return err
}
