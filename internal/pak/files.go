package pak

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/coalaura/pakr/internal/native"
)

type entry struct {
	path   string
	header native.Header
}

type copier struct {
	ctx      context.Context
	progress *Progress
	buffer   []byte
}

func (copying *copier) copy(target io.Writer, source io.Reader) (int64, error) {
	var total int64

	for {
		err := copying.ctx.Err()
		if err != nil {
			return total, err
		}

		count, readErr := source.Read(copying.buffer)
		if count > 0 {
			written, err := target.Write(copying.buffer[:count])
			total += int64(written)

			copying.progress.Add(written)

			if err != nil {
				return total, err
			}

			if written != count {
				return total, io.ErrShortWrite
			}
		}

		if errors.Is(readErr, io.EOF) {
			return total, nil
		}

		if readErr != nil {
			return total, readErr
		}
	}
}

func (copying *copier) file(target io.Writer, item *entry) error {
	file, err := os.Open(item.path)
	if err != nil {
		return err
	}

	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Size() != item.header.Size {
		return fmt.Errorf("input changed while packing: %s", item.path)
	}

	count, err := copying.copy(target, file)
	if err != nil {
		return err
	}

	if count != item.header.Size {
		return fmt.Errorf("input changed while packing: %s", item.path)
	}

	return nil
}

func collect(ctx context.Context, inputs []string, output string) ([]entry, int64, error) {
	entries := make([]entry, 0, len(inputs))

	// One lexical walk has unique paths; only overlapping input roots need a set.
	var names map[string]struct{}

	if len(inputs) > 1 {
		names = make(map[string]struct{}, len(inputs))
	}

	var total int64

	for _, input := range inputs {
		absolute, err := filepath.Abs(input)
		if err != nil {
			return nil, 0, err
		}

		base := filepath.Dir(absolute)

		if filepath.Clean(input) == "." {
			base = absolute
		}

		err = filepath.WalkDir(absolute, func(filename string, directory fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			contextErr := ctx.Err()
			if contextErr != nil {
				return contextErr
			}

			if filename == output {
				return nil
			}

			name, err := filepath.Rel(base, filename)
			if err != nil {
				return err
			}

			if name == "." {
				return nil
			}

			name = filepath.ToSlash(name)

			_, err = cleanName(name)
			if err != nil {
				return err
			}

			if _, exists := names[name]; exists {
				return fmt.Errorf("duplicate archive path: %s", name)
			}

			if names != nil {
				names[name] = struct{}{}
			}

			info, err := directory.Info()
			if err != nil {
				return err
			}

			header := native.Header{Name: name, Mode: uint32(info.Mode().Perm()), Modified: info.ModTime()}

			switch {
			case info.Mode().IsRegular():
				header.Size = info.Size()
				if header.Size < 0 || header.Size > math.MaxInt64-total {
					return errors.New("input sizes exceed supported total")
				}

				total += header.Size
			case info.IsDir():
				header.Kind = native.Directory
			case info.Mode()&os.ModeSymlink != 0:
				header.Kind = native.Symlink

				header.Link, err = os.Readlink(filename)
				if err != nil {
					return err
				}

				header.Link = filepath.ToSlash(header.Link)

				err = validateLink(name, header.Link)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported file type: %s", filename)
			}

			if len(entries) == cap(entries) {
				// Doubling avoids repeated copies of large metadata records on deep trees.
				grown := make([]entry, len(entries), max(1024, 2*cap(entries)))
				copy(grown, entries)

				entries = grown
			}

			entries = append(entries, entry{path: filename, header: header})

			return nil
		})

		if err != nil {
			return nil, 0, err
		}
	}

	return entries, total, nil
}

func cleanName(name string) (string, error) {
	if strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe archive path: %q", name)
	}

	cleaned := path.Clean(name)
	if !filepath.IsLocal(cleaned) {
		return "", fmt.Errorf("unsafe archive path: %q", name)
	}

	return cleaned, nil
}

func validateLink(name, target string) error {
	if target == "" || strings.ContainsAny(target, "\\:\x00") || strings.HasPrefix(target, "/") {
		return fmt.Errorf("unsafe symbolic link: %s -> %s", name, target)
	}

	_, err := cleanName(path.Join(path.Dir(name), target))
	return err
}

func publish(temporary, output string) error {
	err := os.Link(temporary, output)
	if err != nil {
		return fmt.Errorf("publish archive without overwriting existing files: %w", err)
	}

	return os.Remove(temporary)
}

func closeWithError(closer io.Closer, prior error) error {
	return errors.Join(prior, closer.Close())
}
