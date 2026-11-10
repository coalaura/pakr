package pak

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

func TestTarBufferedFlushFailure(t *testing.T) {
	failure := errors.New("flush failed")
	copying := copier{ctx: t.Context(), buffer: make([]byte, native.BufferSize)}

	err := packTar(zipInterruptWriter{err: failure}, nil, &copying)
	if !errors.Is(err, failure) {
		t.Fatalf("buffered final write error lost: %v", err)
	}
}

func TestTarBufferedFooterFailure(t *testing.T) {
	var encoded bytes.Buffer

	compressed := gzip.NewWriter(&encoded)
	archive := tar.NewWriter(compressed)
	header := tar.Header{Name: "file", Mode: 0o600, Size: 3}

	err := archive.WriteHeader(&header)
	if err != nil {
		t.Fatal(err)
	}

	_, err = archive.Write([]byte("tar"))
	if err != nil {
		t.Fatal(err)
	}

	err = archive.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = compressed.Close()
	if err != nil {
		t.Fatal(err)
	}

	encoded.Bytes()[encoded.Len()-8] ^= 1
	options := Options{Format: Format{Name: "tar.gz", Container: "tar", Codec: native.Gzip}, Output: t.TempDir(), Threads: 1}

	err = Unpack(t.Context(), "-", options, &encoded)
	if err == nil {
		t.Fatal("codec footer error lost after TAR EOF")
	}
}

func TestExtractionAncestorCacheBound(t *testing.T) {
	output := t.TempDir()

	root, err := os.OpenRoot(output)
	if err != nil {
		t.Fatal(err)
	}

	defer root.Close()

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)

	copying := copier{ctx: ctx, buffer: make([]byte, 16)}

	extract := extractor{root: root, copying: &copying, budget: newExtractionBudget(ctx, nil, cancel)}
	defer extract.close()

	name := strings.Repeat("d/", 40) + "first"

	err = extract.entry(native.Header{Name: name, Kind: native.Regular, Mode: 0o600}, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}

	if len(extract.ancestors) != 32 {
		t.Fatalf("cached handles: %d", len(extract.ancestors))
	}

	pinned := extract.ancestors[0].root

	err = extract.entry(native.Header{Name: "other/branch/second", Kind: native.Regular, Mode: 0o600}, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}

	_, err = pinned.Stat(".")
	if err == nil {
		t.Fatal("unused prefix handle retained")
	}

	err = extract.finish()
	if err != nil {
		t.Fatal(err)
	}

	if len(extract.ancestors) != 0 {
		t.Fatal("cached handles not closed")
	}

	_, err = os.Stat(filepath.Join(output, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
}
