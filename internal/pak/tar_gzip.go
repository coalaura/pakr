package pak

import (
	"archive/tar"
	"context"
	"io"
	"os"

	"github.com/coalaura/pakr/internal/native"
)

func tarGzipParallel(ctx context.Context, file *os.File, size int64) (bool, error) {
	// Probe without changing the input offset. Bound metadata work even when an
	// archive has no payload; normal extraction still validates the entire stream.
	reader, err := native.NewReader(io.NewSectionReader(file, 0, size), native.Gzip, 1)
	if err != nil {
		return false, err
	}

	defer reader.Close()

	archive := tar.NewReader(io.LimitReader(reader, native.BufferSize))

	for {
		err = context.Cause(ctx)
		if err != nil {
			return false, err
		}

		header, readErr := archive.Next()
		if readErr != nil {
			return false, nil
		}

		if header.Typeflag == tar.TypeDir {
			continue
		}

		// A large first payload cannot use the TAR file workers. Spend the worker
		// budget on decoding instead; small-file archives retain filesystem overlap.
		return header.Typeflag == tar.TypeReg && header.Size > tarWorkerSize, nil
	}
}
