package pak

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/coalaura/pakr/internal/native"
)

type extractor struct {
	root        *os.Root
	copying     *copier
	directories *extractionDirectories
	budget      *extractionBudget
	prechecked  bool
}

type extractionDirectory struct {
	info   os.FileInfo
	header native.Header
}

type extractionDirectories struct {
	mutex   sync.RWMutex
	created map[string]int
	entries []extractionDirectory
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

	if name == "." && header.Kind != native.Directory || header.Size < -1 {
		return fmt.Errorf("invalid entry: %q", header.Name)
	}

	if !extract.prechecked {
		err = extract.budget.entry(header.Size)
		if err != nil {
			return err
		}
	}

	if name == "." {
		return nil
	}

	err = extract.directory(path.Dir(name), nil, false)
	if err != nil {
		return err
	}

	switch header.Kind {
	case native.Directory:
		header.Name = name
		err = extract.directory(name, &header, false)
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

	err = extract.payload(header, file, source)

	if err == nil {
		err = applyMetadata(file, header)
	}

	err = closeWithError(file, err)
	if err != nil {
		return errors.Join(err, extract.root.Remove(name))
	}

	return nil
}

func (extract *extractor) payload(header native.Header, target io.Writer, source io.Reader) error {
	if header.Size < -1 {
		return fmt.Errorf("invalid entry size: %d", header.Size)
	}

	if header.Size >= 0 && header.Size < math.MaxInt64 {
		source = io.LimitReader(source, header.Size+1)
	}

	writer := extractionWriter{target: target, budget: extract.budget, declared: header.Size}

	count, err := extract.copying.copy(&writer, source)
	if err == nil && header.Size >= 0 && count != header.Size {
		err = fmt.Errorf("size mismatch: expected %d bytes, got %d", header.Size, count)
	}

	return err
}

func (extract *extractor) finish() error {
	if extract.directories == nil {
		return nil
	}

	// Creation order puts parents before children, even with late directory headers.
	for _, entry := range slices.Backward(extract.directories.entries) {
		if entry.header.Name == "" {
			continue
		}

		err := extract.finishDirectory(entry)
		if err != nil {
			return err
		}
	}

	return nil
}

func (extract *extractor) directory(name string, header *native.Header, exclusive bool) error {
	if name == "." {
		return nil
	}

	directories := extract.directoryState()
	parent := extract.root
	end := 0

	defer func() {
		if parent != extract.root {
			parent.Close()
		}
	}()

	// Walk relative to pinned parents instead of resolving every prefix from
	// the extraction root. This keeps deep paths linear without caching handles.
	for component := range strings.SplitSeq(name, "/") {
		end += len(component) + 1
		prefix := name[:end-1]

		info, err := directories.directoryInfo(parent, component, prefix, exclusive && prefix == name)
		if err != nil {
			return err
		}

		if prefix == name {
			if header != nil {
				directories.mutex.Lock()

				if index, created := directories.created[prefix]; created {
					directories.entries[index].header = *header
				}

				directories.mutex.Unlock()
			}

			return nil
		}

		next, err := parent.OpenRoot(component)
		if err != nil {
			return err
		}

		pinned, err := next.Stat(".")
		if err != nil || !os.SameFile(info, pinned) {
			next.Close()

			return fmt.Errorf("directory changed during extraction: %s", prefix)
		}

		if parent != extract.root {
			parent.Close()
		}

		parent = next
	}

	return nil
}

func (directories *extractionDirectories) directoryInfo(parent *os.Root, component, name string, exclusive bool) (os.FileInfo, error) {
	info, err := parent.Lstat(component)
	if errors.Is(err, os.ErrNotExist) {
		// Only creation is serialized. Recheck after locking because another ZIP
		// worker may have created and registered this directory in the meantime.
		directories.mutex.Lock()
		defer directories.mutex.Unlock()

		info, err = parent.Lstat(component)
		if errors.Is(err, os.ErrNotExist) {
			err = parent.Mkdir(component, 0o755)
			if err != nil {
				return nil, err
			}

			info, err = parent.Lstat(component)
			if err == nil && info.IsDir() {
				// A removed directory must not be re-registered with a new identity.
				if _, created := directories.created[name]; !created {
					directories.created[name] = len(directories.entries)
					directories.entries = append(directories.entries, extractionDirectory{info: info})
				}
			}
		} else if err == nil && exclusive {
			return nil, fmt.Errorf("output already exists: %s", name)
		}
	} else {
		if err == nil && exclusive {
			return nil, fmt.Errorf("output already exists: %s", name)
		}

		directories.mutex.RLock()
		defer directories.mutex.RUnlock()
	}

	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("conflicting directory entry: %s", name)
	}

	if index, created := directories.created[name]; created {
		if !os.SameFile(directories.entries[index].info, info) {
			return nil, fmt.Errorf("directory changed during extraction: %s", name)
		}
	}

	return info, nil
}

func (extract *extractor) directoryState() *extractionDirectories {
	if extract.directories == nil {
		extract.directories = &extractionDirectories{created: make(map[string]int)}
	}

	return extract.directories
}

func (extract *extractor) finishDirectory(entry extractionDirectory) error {
	header := entry.header

	info, err := extract.root.Lstat(header.Name)
	if err != nil {
		return err
	}

	if !info.IsDir() || !os.SameFile(entry.info, info) {
		return fmt.Errorf("directory changed during extraction: %s", header.Name)
	}

	directory, err := extract.root.OpenRoot(header.Name)
	if err != nil {
		return err
	}

	defer directory.Close()

	// Pin and verify the created directory before touching metadata, so path
	// replacement cannot redirect restoration to a pre-existing directory.
	info, err = directory.Stat(".")
	if err != nil {
		return err
	}

	if !os.SameFile(entry.info, info) {
		return fmt.Errorf("directory changed during extraction: %s", header.Name)
	}

	if !header.Modified.IsZero() {
		err = directory.Chtimes(".", header.Modified, header.Modified)
		if err != nil {
			return err
		}
	}

	return directory.Chmod(".", os.FileMode(header.Mode&0o777))
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
