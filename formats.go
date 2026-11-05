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
xz      Slow       Moderate   Very high
7z      Slow       Moderate   Very high
br      Fast       Fast       High

Default: tar.zst. Fastest compression: tar.lz4. Broad desktop compatibility: zip.

Raw codecs take one file; use tar.<codec> for multiple files or folders.

Speeds assume level 3; higher levels can be much slower. Ratings are qualitative and vary with input, threads and hardware.
Level 9 reaches each backend's standard maximum; --max also enables XZ extreme within resource budgets.
Use --seekable with zst or tar.zst for independent frames and parallel file extraction.
`

func showFormats(writer io.Writer) error {
	_, err := io.WriteString(writer, formatsOverview)

	return err
}
