package main

import "io"

const formatsOverview = `Format  Compress   Extract    Savings
------  ---------  ---------  ---------------
tar     Very fast  Very fast  None
lz4     Very fast  Very fast  Modest
zst     Fast       Very fast  High
gz      Fast       Fast       Moderate
zip     Fast       Fast       Moderate
bz2     Slow       Moderate   Moderate-high
bz3     Moderate   Moderate   Very high
xz      Slow       Moderate   Very high
7z      Slow       Moderate   Very high
br      Fast       Fast       High

Choosing a format
  - Default: tar.zst
  - Fastest compression: tar.lz4
  - Broad desktop compatibility: zip
  - Raw codecs take one file; use tar.<codec> for multiple files or folders.

Compression settings
  - Ratings assume level 3 and vary with input, threads and hardware. Higher levels can be much slower.
  - Level 9 selects each backend's standard maximum; --max also enables XZ extreme within resource budgets.
  - --seekable enables independent frames and parallel file extraction for zst and tar.zst.
`

func showFormats(writer io.Writer) error {
	_, err := io.WriteString(writer, formatsOverview)

	return err
}
