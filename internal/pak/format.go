package pak

import (
	"fmt"
	"strings"

	"github.com/coalaura/pakr/internal/native"
)

type Format struct {
	Name      string
	Container string
	Codec     int
}

var formats = map[string]Format{
	"tar":    {Name: "tar", Container: "tar"},
	"zip":    {Name: "zip", Container: "zip"},
	"7z":     {Name: "7z", Container: "7z"},
	"gz":     {Name: "gz", Codec: native.Gzip},
	"gzip":   {Name: "gz", Codec: native.Gzip},
	"zst":    {Name: "zst", Codec: native.Zstd},
	"zstd":   {Name: "zst", Codec: native.Zstd},
	"bz2":    {Name: "bz2", Codec: native.Bzip2},
	"bzip2":  {Name: "bz2", Codec: native.Bzip2},
	"xz":     {Name: "xz", Codec: native.XZ},
	"lzma2":  {Name: "xz", Codec: native.XZ},
	"lz4":    {Name: "lz4", Codec: native.LZ4},
	"br":     {Name: "br", Codec: native.Brotli},
	"brotli": {Name: "br", Codec: native.Brotli},
}

var aliases = map[string]string{
	"tgz":  "tar.gz",
	"tbz":  "tar.bz2",
	"tbz2": "tar.bz2",
	"txz":  "tar.xz",
	"tzst": "tar.zst",
}

func ParseFormat(name string) (Format, error) {
	name = strings.TrimPrefix(strings.ToLower(name), ".")
	if canonical, ok := aliases[name]; ok {
		name = canonical
	}

	suffix, compound := strings.CutPrefix(name, "tar.")
	if compound {
		codec, ok := formats[suffix]
		if ok && codec.Codec != 0 {
			return Format{Name: "tar." + codec.Name, Container: "tar", Codec: codec.Codec}, nil
		}
	}

	if format, ok := formats[name]; ok {
		return format, nil
	}

	return Format{}, fmt.Errorf("unknown format %q (use tar, zip, 7z, gz, zst, bz2, xz, lz4, br, or tar.<codec>)", name)
}

func InferFormat(name string) (Format, error) {
	name = strings.ToLower(name)

	for index := range len(name) {
		if name[index] != '.' {
			continue
		}

		format, err := ParseFormat(name[index+1:])
		if err == nil {
			return format, nil
		}
	}

	return Format{}, fmt.Errorf("cannot infer format from %q; specify --format", name)
}
