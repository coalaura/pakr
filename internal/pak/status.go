package pak

import (
	"io"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/coalaura/plain/minimal"
)

const previewPathLimit = 64

type countingWriter struct {
	output io.Writer
	bytes  int64
}

func (writer *countingWriter) Write(data []byte) (int, error) {
	count, err := writer.output.Write(data)
	writer.bytes += int64(count)

	return count, err
}

func packPreview(log *minimal.Minimal, inputs []string, destination string) {
	if log == nil {
		return
	}

	if destination == "-" {
		destination = "stdout"
	}

	if len(inputs) == 1 {
		_ = log.Infof("pack %s -> %s\n", previewPath(inputs[0]), previewPath(destination))

		return
	}

	_ = log.Infoln("pack")

	for _, input := range inputs[:min(len(inputs), 3)] {
		_ = log.Subln(previewPath(input))
	}

	if len(inputs) > 3 {
		_ = log.Subf("+%d more\n", len(inputs)-3)
	}

	_ = log.Stepf("into %s\n", previewPath(destination))
}

func unpackPreview(log *minimal.Minimal, input, destination string) {
	if input == "-" {
		input = "stdin"
	}

	if log != nil {
		_ = log.Infof("unpack %s -> %s\n", previewPath(input), previewPath(destination))
	}
}

func previewPath(name string) string {
	if len(name) <= previewPathLimit {
		return displayPath(name)
	}

	// Keep the filename end without splitting a UTF-8 character.
	offset := len(name) - previewPathLimit + 1

	for offset < len(name) && !utf8.RuneStart(name[offset]) {
		offset++
	}

	return displayPath("~" + name[offset:])
}

func displayPath(name string) string {
	for _, character := range name {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return strconv.Quote(name)
		}
	}

	return name
}
