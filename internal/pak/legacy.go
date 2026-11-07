package pak

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"

	"github.com/coalaura/pakr/internal/native"
)

type cpioLinkGroup struct {
	target  string
	pending []native.Header
}

type cpioLinks map[string]*cpioLinkGroup

func (links *cpioLinks) entry(header native.Header, archive io.Reader, extract *extractor) error {
	_, err := cleanName(header.Name)
	if err != nil {
		return err
	}

	if header.Kind != native.Hardlink && (header.Kind != native.Regular || header.Links <= 1) {
		err = extract.entry(header, archive)
		if err != nil || header.Kind == native.Regular {
			return err
		}

		return drainCPIOEntry(header, archive, extract)
	}

	if *links == nil {
		*links = make(cpioLinks)
	}

	key := header.Name

	if header.Kind == native.Hardlink {
		key = header.Link
	}

	group := (*links)[key]
	if group == nil {
		if header.Kind == native.Hardlink {
			return errors.New("unresolved CPIO hardlink")
		}

		group = &cpioLinkGroup{}
		(*links)[key] = group
	}

	if group.target != "" {
		err = drainCPIOEntry(header, archive, extract)
		if err != nil {
			return err
		}

		header.Kind = native.Hardlink
		header.Link = group.target
		header.Size = 0

		return extract.entry(header, archive)
	}

	if header.Size == 0 {
		err = drainCPIOEntry(header, archive, extract)
		if err != nil {
			return err
		}

		group.pending = append(group.pending, header)

		return nil
	}

	// Newc commonly stores the payload in the last member of a hardlink group.
	// Create that file once, then link the previously deferred names to it.
	header.Kind = native.Regular
	header.Link = ""

	err = extract.entry(header, archive)
	if err != nil {
		return err
	}

	group.target = header.Name

	return group.finish(extract)
}

func (group *cpioLinkGroup) finish(extract *extractor) error {
	for _, header := range group.pending {
		header.Kind = native.Hardlink
		header.Link = group.target
		header.Size = 0

		if group.target == "" {
			header.Kind = native.Regular
		}

		err := extract.entry(header, strings.NewReader(""))
		if err != nil {
			return err
		}

		if group.target == "" {
			group.target = header.Name
		}
	}

	group.pending = nil

	return nil
}

func unpackLegacyStream(input, inputName, format string, extract *extractor) (result error) {
	archive, err := native.OpenArchiveReader(input, format)
	if err != nil {
		return err
	}

	defer func() {
		result = errors.Join(result, archive.Close())
	}()

	_, err = archive.Next()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	header := native.Header{Name: rawOutputName(inputName), Size: -1, Mode: 0o644}
	source := io.Reader(archive)

	if errors.Is(err, io.EOF) {
		source = strings.NewReader("")
	}

	return extract.entry(header, source)
}

func validateISO(source io.ReaderAt, size int64) error {
	var descriptor [2048]byte

	// ISO volume descriptors start at sector 16; a boot record can precede the PVD.
	for sector := int64(16); sector < 256; sector++ {
		_, err := source.ReadAt(descriptor[:], sector*2048)
		if err != nil {
			return err
		}

		if string(descriptor[1:7]) != "CD001\x01" {
			return errors.New("invalid ISO9660 volume descriptor")
		}

		if descriptor[0] == 255 {
			break
		}

		if descriptor[0] != 1 {
			continue
		}

		blocks := binary.LittleEndian.Uint32(descriptor[80:84])
		blockSize := binary.LittleEndian.Uint16(descriptor[128:130])

		if blocks != binary.BigEndian.Uint32(descriptor[84:88]) || blockSize != binary.BigEndian.Uint16(descriptor[130:132]) || blockSize == 0 {
			return errors.New("invalid ISO9660 volume size")
		}

		if int64(blocks)*int64(blockSize) > size {
			return errors.New("truncated ISO9660 volume")
		}

		return nil
	}

	return errors.New("missing ISO9660 primary volume descriptor")
}

func finishCPIO(archive *native.Archive, links *cpioLinks, extract *extractor) error {
	for {
		err := extract.copying.ctx.Err()
		if err != nil {
			return err
		}

		err = archive.ReadTail()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}
	}

	for _, group := range *links {
		err := group.finish(extract)
		if err != nil {
			return err
		}
	}

	return nil
}

func drainCPIOEntry(header native.Header, archive io.Reader, extract *extractor) error {
	if header.Kind == native.Symlink {
		// libarchive already consumed and validated the link payload in Next.
		// Its declared bytes were reserved when the header arrived.
		header.Size = 0
	}

	return extract.payload(header, io.Discard, archive)
}
