package pak

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"

	"github.com/coalaura/pakr/internal/native"
)

type extractor struct {
	root        *os.Root
	copying     *copier
	directories []native.Header
}

func (extract *extractor) entry(header native.Header, source io.Reader) error {
	err := extract.copying.ctx.Err()
	if err != nil {
		return err
	}

	name, err := cleanName(header.Name)
	if err != nil {
		return err
	}

	if name == "." && header.Kind == native.Directory {
		return nil
	}

	if name == "." || header.Size < -1 {
		return fmt.Errorf("invalid entry: %q", header.Name)
	}

	err = extract.root.MkdirAll(path.Dir(name), 0o755)
	if err != nil {
		return err
	}

	switch header.Kind {
	case native.Directory:
		err = extract.root.MkdirAll(name, 0o755)
		if err == nil {
			header.Name = name
			extract.directories = append(extract.directories, header)
		}
	case native.Symlink:
		err = validateLink(name, header.Link)
		if err == nil {
			err = extract.root.Symlink(header.Link, name)
		}
	case native.Hardlink:
		var target string

		target, err = cleanName(header.Link)
		if err == nil {
			err = extract.root.Link(target, name)
		}
	case native.Regular:
		err = extract.file(name, header, source)
	default:
		err = fmt.Errorf("unsupported entry type: %s", name)
	}

	if err != nil {
		return fmt.Errorf("extract %q: %w", name, err)
	}

	extract.copying.progress.File()

	return nil
}

func (extract *extractor) file(name string, header native.Header, source io.Reader) error {
	file, err := extract.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	count, err := extract.copying.copy(file, source)
	if err == nil && header.Size >= 0 && count != header.Size {
		err = fmt.Errorf("size mismatch: expected %d bytes, got %d", header.Size, count)
	}

	if err == nil {
		err = applyMetadata(file, header)
	}

	err = closeWithError(file, err)
	if err != nil {
		return errors.Join(err, extract.root.Remove(name))
	}

	return nil
}

func (extract *extractor) finish() error {
	for _, header := range slices.Backward(extract.directories) {
		err := extract.root.Chmod(header.Name, os.FileMode(header.Mode&0o777))
		if err != nil {
			return err
		}

		if !header.Modified.IsZero() {
			err = extract.root.Chtimes(header.Name, header.Modified, header.Modified)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func applyMetadata(file *os.File, header native.Header) error {
	err := file.Chmod(os.FileMode(header.Mode & 0o777))
	if err != nil {
		return err
	}

	if !header.Modified.IsZero() {
		return setFileTime(file, header.Modified)
	}

	return nil
}
