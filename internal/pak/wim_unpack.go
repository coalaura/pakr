package pak

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/coalaura/pakr/internal/membudget"
	"github.com/coalaura/pakr/internal/native"
)

type wimItem struct {
	header native.Header
	hash   [20]byte
	size   uint64
}

type wimStage struct {
	directory string
	sizes     map[[20]byte]uint64
	file      *os.File
	hash      [20]byte
	offset    uint64
}

func (stage *wimStage) receive(hash [20]byte, offset uint64, data []byte, event int) error {
	switch event {
	case 0:
		if stage.file != nil {
			return errors.New("overlapping WIM resources")
		}

		var err error

		stage.file, err = os.OpenFile(stage.filename(hash), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		stage.hash = hash
		stage.offset = 0

		return err
	case 1:
		if stage.file == nil || hash != stage.hash || offset != stage.offset || offset > stage.sizes[hash] || uint64(len(data)) > stage.sizes[hash]-offset {
			return errors.New("invalid WIM resource size or offset")
		}

		count, err := stage.file.Write(data)
		stage.offset += uint64(count)

		return err
	case 2:
		if stage.file == nil {
			return errors.New("missing WIM resource")
		}

		err := stage.file.Close()

		stage.file = nil
		if stage.offset != stage.sizes[hash] {
			return errors.Join(err, errors.New("truncated WIM resource"))
		}

		return err
	default:
		return errors.New("invalid WIM resource callback")
	}
}

func (stage *wimStage) filename(hash [20]byte) string {
	return filepath.Join(stage.directory, hex.EncodeToString(hash[:]))
}

func unpackWIM(input string, file *os.File, size int64, options Options, extract *extractor) error {
	memory := membudget.Resolve(options.Memory)

	metadataLimit, err := validateWIM(file, size, memory)
	if err != nil {
		return err
	}

	session, err := native.NewWIM(extract.copying.ctx)
	if err != nil {
		return err
	}

	defer session.Close()

	err = session.Open(input)
	if err != nil {
		return err
	}

	image := options.Image
	if image == 0 {
		if session.Images() != 1 {
			return fmt.Errorf("WIM contains %d images; select one with --image N", session.Images())
		}

		image = 1
	}

	if image < 1 || image > session.Images() {
		return fmt.Errorf("invalid WIM image %d; archive contains %d images", image, session.Images())
	}

	items := make([]wimItem, 0, 256)
	names := make(map[string]int, 256)
	groups := make(map[uint64]int, 256)
	sizes := make(map[[20]byte]uint64, 256)
	hashes := make([][20]byte, 0, 256)
	metadataUsed := uint64(0)

	err = session.Tree(image, metadataLimit, func(item native.WIMEntry) error {
		header, entryErr := wimHeader(item)
		if entryErr != nil {
			return entryErr
		}

		metadataUsed += 512 + uint64(len(header.Name))*4
		if metadataUsed > metadataLimit*4 {
			return errors.New("WIM directory tree exceeds metadata memory budget")
		}

		entryErr = extract.budget.entry(int64(item.Size))
		if entryErr != nil {
			return entryErr
		}

		key := strings.ToUpper(header.Name)
		if _, exists := names[key]; exists {
			return fmt.Errorf("duplicate or case-colliding WIM path: %s", header.Name)
		}

		if header.Name != "." {
			parent, exists := names[strings.ToUpper(path.Dir(header.Name))]
			if !exists || items[parent].header.Kind != native.Directory || items[parent].header.Name != path.Dir(header.Name) {
				return fmt.Errorf("invalid WIM parent directory: %s", header.Name)
			}

			_, entryErr = extract.root.Lstat(header.Name)
			if entryErr == nil {
				return fmt.Errorf("output already exists: %s", header.Name)
			}

			if !errors.Is(entryErr, os.ErrNotExist) {
				return entryErr
			}
		}

		if item.Group != 0 && header.Kind == native.Regular {
			if previous, exists := groups[item.Group]; exists {
				original := &items[previous]
				if original.hash != item.Hash || original.size != item.Size || original.header.Mode != header.Mode || !original.header.Modified.Equal(header.Modified) {
					return fmt.Errorf("inconsistent WIM hardlink: %s", header.Name)
				}

				header.Kind = native.Hardlink
				header.Link = original.header.Name
			} else {
				groups[item.Group] = len(items)
			}
		}

		if item.Size != 0 {
			if item.Hash == [20]byte{} {
				return fmt.Errorf("missing WIM resource hash: %s", header.Name)
			}

			previous, exists := sizes[item.Hash]
			if exists && previous != item.Size {
				return fmt.Errorf("inconsistent WIM resource size: %s", header.Name)
			}

			if !exists {
				sizes[item.Hash] = item.Size
				hashes = append(hashes, item.Hash)
			}
		}

		names[key] = len(items)
		items = append(items, wimItem{header: header, hash: item.Hash, size: item.Size})

		return nil
	})

	if err != nil {
		return err
	}

	directory, err := os.MkdirTemp("", "pakr-wim-*")
	if err != nil {
		return err
	}

	defer os.RemoveAll(directory)

	stage := wimStage{directory: directory, sizes: sizes}

	defer func() {
		if stage.file != nil {
			_ = stage.file.Close()
		}
	}()

	if options.Log != nil {
		_ = options.Log.Infof("WIM: image %d of %d, 1 native decoder, %d MiB allocation budget\n", image, session.Images(), memory>>20)
	}

	extract.copying.progress.Phase("verify", -1)

	err = session.Read(hashes, stage.receive)
	if err != nil {
		return err
	}

	for index := range items {
		item := &items[index]
		if item.header.Kind != native.Symlink {
			continue
		}

		data, readErr := os.ReadFile(stage.filename(item.hash))
		if readErr != nil {
			return readErr
		}

		item.header.Link, err = wimSymlink(data)
		if err == nil {
			err = validateLink(item.header.Name, item.header.Link)
		}

		if err != nil {
			return err
		}

		if runtime.GOOS == "windows" {
			target := path.Join(path.Dir(item.header.Name), item.header.Link)

			position, exists := names[strings.ToUpper(target)]
			if !exists || items[position].header.Kind == native.Symlink {
				return fmt.Errorf("WIM symlink on Windows requires a direct, existing archive target: %s", item.header.Name)
			}
		}
	}

	return publishWIM(items, &stage, extract)
}

func publishWIM(items []wimItem, stage *wimStage, extract *extractor) (result error) {
	created := make([]native.Header, 0, len(items))

	defer func() {
		if result == nil {
			return
		}

		// Only remove entries created by this job, and restore traversal first.
		for _, header := range created {
			if header.Kind == native.Directory {
				_ = extract.root.Chmod(header.Name, 0o700)
			}
		}

		for _, header := range slices.Backward(created) {
			result = errors.Join(result, extract.root.Remove(header.Name))
		}
	}()

	extract.prechecked = true

	defer func() {
		extract.prechecked = false
	}()

	extract.copying.progress.Phase("unpack", -1)

	// Publish symlinks after their targets so os.Root preserves directory links on Windows.
	order := make([]int, 0, len(items))

	for index := range items {
		if items[index].header.Kind != native.Symlink {
			order = append(order, index)
		}
	}

	for index := range items {
		if items[index].header.Kind == native.Symlink {
			order = append(order, index)
		}
	}

	for _, index := range order {
		item := &items[index]
		if item.header.Name == "." {
			continue
		}

		err := extract.copying.ctx.Err()
		if err != nil {
			return err
		}

		if item.header.Kind == native.Directory {
			err = extract.root.Mkdir(item.header.Name, 0o755)
			if err == nil {
				extract.directories = append(extract.directories, item.header)
			}
		} else {
			var (
				source io.Reader = bytes.NewReader(nil)
				file   *os.File
			)

			if item.header.Kind == native.Regular && item.size != 0 {
				file, err = os.Open(stage.filename(item.hash))
				if err != nil {
					return err
				}

				source = file
			}

			err = extract.entry(item.header, source)

			if file != nil {
				closeErr := file.Close()
				if err == nil && closeErr != nil {
					created = append(created, item.header)

					return closeErr
				}
			}
		}

		if err != nil {
			return err
		}

		created = append(created, item.header)
	}

	result = extract.finish()
	extract.directories = nil

	if result == nil {
		result = extract.copying.ctx.Err()
	}

	return result
}
