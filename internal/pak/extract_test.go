package pak

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/coalaura/pakr/internal/native"
)

func TestExtractionDirectoryMetadata(t *testing.T) {
	formats := []string{"tar", "zip"}
	modified := time.Unix(1700000000, 0)
	original := modified.Add(-time.Hour)

	for _, name := range formats {
		t.Run(name, func(t *testing.T) {
			output := t.TempDir()
			existing := filepath.Join(output, "existing")

			err := os.Mkdir(existing, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			err = os.Chtimes(existing, original, original)
			if err != nil {
				t.Fatal(err)
			}

			before, err := os.Stat(existing)
			if err != nil {
				t.Fatal(err)
			}

			headers := []tar.Header{
				{Name: "existing/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: modified},
				{Name: "parent/child/file", Typeflag: tar.TypeReg, Mode: 0o600},
				{Name: "parent/child/", Typeflag: tar.TypeDir, Mode: 0o700, ModTime: modified},
				{Name: "parent/", Typeflag: tar.TypeDir, Mode: 0o750, ModTime: modified},
				{Name: "locked/", Typeflag: tar.TypeDir, Mode: 0, ModTime: modified},
			}

			err = unpackDirectoryHeaders(t, output, name, headers)
			if err != nil {
				t.Fatal(err)
			}

			after, err := os.Stat(existing)
			if err != nil {
				t.Fatal(err)
			}

			if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("pre-existing directory metadata changed: %v, %v", after.Mode(), after.ModTime())
			}

			for _, header := range headers[2:] {
				info, statErr := os.Stat(filepath.Join(output, header.Name))
				if statErr != nil {
					t.Fatal(statErr)
				}

				if !info.ModTime().Equal(modified) || runtime.GOOS != "windows" && info.Mode().Perm() != os.FileMode(header.Mode) {
					t.Fatalf("created directory metadata not restored for %s: %v, %v", header.Name, info.Mode(), info.ModTime())
				}
			}
		})
	}
}

func TestExtractionDirectoryConflicts(t *testing.T) {
	sequences := [][]tar.Header{
		{{Name: "entry", Typeflag: tar.TypeSymlink, Linkname: "."}, {Name: "entry/", Typeflag: tar.TypeDir}},
		{{Name: "entry", Typeflag: tar.TypeReg}, {Name: "entry/", Typeflag: tar.TypeDir}},
		{{Name: "entry/", Typeflag: tar.TypeDir}, {Name: "entry", Typeflag: tar.TypeReg}},
		{{Name: "entry/", Typeflag: tar.TypeDir}, {Name: "entry", Typeflag: tar.TypeSymlink, Linkname: "."}},
		{{Name: "entry", Typeflag: tar.TypeSymlink, Linkname: "."}, {Name: "entry/child/", Typeflag: tar.TypeDir}},
	}

	for index, headers := range sequences {
		output := t.TempDir()

		before, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}

		err = unpackDirectoryHeaders(t, output, "tar", headers)
		if err == nil {
			t.Fatalf("conflicting sequence %d accepted", index)
		}

		after, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}

		if after.Mode() != before.Mode() {
			t.Fatalf("sequence %d changed destination root permissions: %v", index, after.Mode())
		}
	}
}

func TestExtractionPreexistingDirectoryConflicts(t *testing.T) {
	kinds := []string{"file", "symlink"}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			output := t.TempDir()
			filename := filepath.Join(output, "entry")

			if kind == "symlink" {
				mustDirectorySymlink(t, ".", filename)
			} else {
				err := os.WriteFile(filename, nil, 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			headers := []tar.Header{{Name: "entry/", Typeflag: tar.TypeDir, Mode: 0o777}}

			err := unpackDirectoryHeaders(t, output, "tar", headers)
			if err == nil {
				t.Fatal("pre-existing conflicting entry accepted")
			}
		})
	}
}

func TestExtractionDirectoryReplacement(t *testing.T) {
	kinds := []string{"directory", "symlink"}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			output := t.TempDir()

			root, err := os.OpenRoot(output)
			if err != nil {
				t.Fatal(err)
			}

			defer root.Close()

			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			extract := extractor{root: root, copying: &copier{ctx: ctx}, budget: newExtractionBudget(ctx, nil, cancel)}
			defer extract.close()

			header := native.Header{Name: "entry", Kind: native.Directory, Mode: 0o777}

			err = extract.entry(header, nil)
			if err != nil {
				t.Fatal(err)
			}

			err = root.Rename("entry", "original")
			if err != nil {
				t.Fatal(err)
			}

			if kind == "symlink" {
				mustDirectorySymlink(t, ".", filepath.Join(output, "entry"))
			} else {
				err = root.Mkdir("entry", 0o755)
				if err != nil {
					t.Fatal(err)
				}
			}

			before, err := root.Stat("entry")
			if err != nil {
				t.Fatal(err)
			}

			err = extract.finish()
			if err == nil {
				t.Fatal("replaced directory accepted")
			}

			after, err := root.Stat("entry")
			if err != nil {
				t.Fatal(err)
			}

			if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("replacement metadata changed")
			}
		})
	}
}

func TestExtractionAncestorReplacement(t *testing.T) {
	kinds := []string{"directory", "symlink", "removed"}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			output := t.TempDir()

			root, err := os.OpenRoot(output)
			if err != nil {
				t.Fatal(err)
			}

			defer root.Close()

			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			extract := extractor{root: root, copying: &copier{ctx: ctx}, budget: newExtractionBudget(ctx, nil, cancel)}
			defer extract.close()

			err = extract.entry(native.Header{Name: "parent/child/first", Kind: native.Regular, Mode: 0o600}, bytes.NewReader(nil))
			if err != nil {
				t.Fatal(err)
			}

			err = root.Rename("parent", "original")
			if err != nil {
				t.Fatal(err)
			}

			switch kind {
			case "directory":
				err = root.Mkdir("parent", 0o755)
				if err != nil {
					t.Fatal(err)
				}

				// Keep the original leaf identity: checking only the leaf would
				// miss the replacement of its ancestor.
				err = root.Rename("original/child", "parent/child")
				if err != nil {
					t.Fatal(err)
				}
			case "symlink":
				mustDirectorySymlink(t, "original", filepath.Join(output, "parent"))
			}

			err = extract.entry(native.Header{Name: "parent/child/second", Kind: native.Regular, Mode: 0o600}, bytes.NewReader(nil))
			if err == nil {
				t.Fatal("replaced implicit ancestor accepted")
			}

			_, err = root.Stat("parent/child/second")
			if !os.IsNotExist(err) {
				t.Fatalf("file published through replaced ancestor: %v", err)
			}
		})
	}
}

func unpackDirectoryHeaders(t *testing.T, output, name string, headers []tar.Header) error {
	t.Helper()

	var encoded bytes.Buffer

	if name == "zip" {
		writer := zip.NewWriter(&encoded)

		for _, header := range headers {
			item := &zip.FileHeader{Name: header.Name, Modified: header.ModTime}
			mode := os.FileMode(header.Mode)

			if header.Typeflag == tar.TypeDir {
				mode |= os.ModeDir
			}

			item.SetMode(mode)

			_, err := writer.CreateHeader(item)
			if err != nil {
				t.Fatal(err)
			}
		}

		err := writer.Close()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		writer := tar.NewWriter(&encoded)

		for index := range headers {
			err := writer.WriteHeader(&headers[index])
			if err != nil {
				t.Fatal(err)
			}
		}

		err := writer.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	format, err := ParseFormat(name)
	if err != nil {
		t.Fatal(err)
	}

	return Unpack(t.Context(), "-", Options{Format: format, Output: output, Threads: 4}, &encoded)
}

func mustDirectorySymlink(t *testing.T, target, name string) {
	t.Helper()

	err := os.Symlink(target, name)
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}

		t.Fatal(err)
	}
}
