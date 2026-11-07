package pak

import (
	"bufio"
	"bytes"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/coalaura/pakr/internal/native"
)

type cpioHeaderMutation struct {
	name    string
	fixture string
	offset  int
	value   string
}

func TestCPIOProbeVariants(t *testing.T) {
	variants := []string{"newc.cpio", "crc.cpio", "odc.cpio", "cpio_bin_be.cpio", "cpio_bin_le.cpio", "empty-dir.cpio"}
	codecs := []string{"gz", "bz2", "xz", "zst"}

	for _, variant := range variants {
		for _, codec := range codecs {
			t.Run(variant+"/"+codec, func(t *testing.T) {
				payload := legacyFixture(t, variant)
				encoded := compressCPIOProbe(t, payload, codec)

				assertCPIOProbe(t, encoded, codec, "cpio."+codec)

				output := unpackFixture(t, encoded, "")

				if variant == "cpio_bin_be.cpio" || variant == "cpio_bin_le.cpio" {
					assertArchiveFile(t, output, "file1111222233334444", []byte("abcde"))
				} else if variant != "empty-dir.cpio" {
					assertArchiveFile(t, output, "dir/file", []byte("legacy archive payload\n"))
				}
			})
		}
	}
}

func TestCPIOProbeRejectsMalformedHeaders(t *testing.T) {
	cases := []cpioHeaderMutation{
		{name: "newc nonhex", fixture: "newc.cpio", offset: 6, value: "g"},
		{name: "newc signed", fixture: "newc.cpio", offset: 6, value: "+"},
		{name: "newc mode", fixture: "newc.cpio", offset: 14, value: "0000f000"},
		{name: "newc mode overflow", fixture: "newc.cpio", offset: 14, value: "000141ed"},
		{name: "newc links", fixture: "newc.cpio", offset: 38, value: "00000000"},
		{name: "newc name short", fixture: "newc.cpio", offset: 94, value: "00000001"},
		{name: "newc name long", fixture: "newc.cpio", offset: 94, value: "00100001"},
		{name: "newc checksum", fixture: "newc.cpio", offset: 102, value: "00000001"},
		{name: "crc nonhex", fixture: "crc.cpio", offset: 102, value: "z"},
		{name: "odc nonoctal", fixture: "odc.cpio", offset: 6, value: "8"},
		{name: "odc timestamp", fixture: "odc.cpio", offset: 48, value: " "},
		{name: "odc size", fixture: "odc.cpio", offset: 65, value: "-"},
		{name: "odc name", fixture: "odc.cpio", offset: 59, value: "000000"},
		{name: "binary be mode", fixture: "cpio_bin_be.cpio", offset: 6, value: "\xf0\x00"},
		{name: "binary le links", fixture: "cpio_bin_le.cpio", offset: 12, value: "\x00\x00"},
		{name: "binary be name", fixture: "cpio_bin_be.cpio", offset: 20, value: "\x00\x01"},
	}
	codecs := []string{"gz", "bz2", "xz", "zst"}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload := legacyFixture(t, test.fixture)
			copy(payload[test.offset:], test.value)

			if validCPIOHeader(payload) {
				t.Fatal("malformed header accepted")
			}

			for _, codec := range codecs {
				encoded := compressCPIOProbe(t, payload, codec)
				assertCPIOProbe(t, encoded, codec, codec)

				output := unpackFixture(t, encoded, "")
				assertArchiveFile(t, output, "stdin", payload)
			}
		})
	}
}

func TestCPIOProbeShortCandidates(t *testing.T) {
	variants := []string{"newc.cpio", "crc.cpio", "odc.cpio", "cpio_bin_be.cpio", "cpio_bin_le.cpio"}
	codecs := []string{"gz", "bz2", "xz", "zst"}

	for _, variant := range variants {
		data := legacyFixture(t, variant)
		headerSize := 110

		switch variant {
		case "odc.cpio":
			headerSize = 76
		case "cpio_bin_be.cpio", "cpio_bin_le.cpio":
			headerSize = 26
		}

		for length := range headerSize {
			if validCPIOHeader(data[:length]) {
				t.Fatalf("accepted %s header of length %d", variant, length)
			}
		}

		for _, codec := range codecs {
			encoded := compressCPIOProbe(t, data[:headerSize-1], codec)
			assertCPIOProbe(t, encoded, codec, codec)

			output := unpackFixture(t, encoded, "")
			assertArchiveFile(t, output, "stdin", data[:headerSize-1])
		}
	}
}

func TestCPIOProbeOrdinaryFiles(t *testing.T) {
	magics := []string{"070701", "070702", "070707", "\xc7\x71", "\x71\xc7"}
	codecs := []string{"gz", "bz2", "xz", "zst"}

	for _, magic := range magics {
		payload := bytes.Repeat([]byte("ordinary file content\n"), 32)
		copy(payload, magic)

		for _, codec := range codecs {
			encoded := compressCPIOProbe(t, payload, codec)
			assertCPIOProbe(t, encoded, codec, codec)

			output := unpackFixture(t, encoded, "")
			assertArchiveFile(t, output, "stdin", payload)

			input := filepath.Join(t.TempDir(), "input."+codec)

			err := os.WriteFile(input, encoded, 0o600)
			if err != nil {
				t.Fatal(err)
			}

			output = t.TempDir()

			err = Unpack(t.Context(), input, Options{Output: output, Threads: 1}, nil)
			if err != nil {
				t.Fatal(err)
			}

			assertArchiveFile(t, output, "input", payload)
		}
	}
}

func TestCPIOProbeInputLimit(t *testing.T) {
	codecs := []string{"gz", "bz2", "xz", "zst"}
	payload := legacyFixture(t, "newc.cpio")

	for _, codec := range codecs {
		t.Run(codec, func(t *testing.T) {
			empty := compressCPIOProbe(t, nil, codec)

			encoded := bytes.Repeat(empty, cpioProbeInputLimit/len(empty)+1)
			encoded = append(encoded, compressCPIOProbe(t, payload, codec)...)

			format, _ := ParseFormat(codec)

			original := bytes.NewReader(encoded)
			input := bufio.NewReaderSize(original, 512)

			replay, detected, err := probeCPIOStream(input, format)
			consumed := len(encoded) - original.Len() - input.Buffered()

			if err != nil || detected != format || consumed > cpioProbeInputLimit {
				t.Fatalf("probe: format=%s consumed=%d error=%v", detected.Name, consumed, err)
			}

			actual, err := io.ReadAll(replay)
			if err != nil || !bytes.Equal(actual, encoded) {
				t.Fatalf("probe replay lost bytes: %v", err)
			}

			assertCPIOProbe(t, encoded, codec, codec)

			output := unpackFixture(t, encoded, "")
			assertArchiveFile(t, output, "stdin", payload)
		})
	}
}

func TestCPIOExplicitFormatBeyondProbe(t *testing.T) {
	payload := legacyFixture(t, "newc.cpio")
	compressed := compressCPIOProbe(t, payload, "gz")

	encoded := make([]byte, 0, len(compressed)+cpioProbeInputLimit+2)
	encoded = append(encoded, compressed[:10]...)
	encoded[3] |= 8 // GZIP FNAME can span more input than an automatic probe allows.
	encoded = append(encoded, bytes.Repeat([]byte{'x'}, cpioProbeInputLimit+1)...)
	encoded = append(encoded, 0)
	encoded = append(encoded, compressed[10:]...)

	assertCPIOProbe(t, encoded, "gz", "gz")

	output := unpackFixture(t, encoded, "")
	assertArchiveFile(t, output, "stdin", payload)

	empty := compressCPIOProbe(t, nil, "gz")

	encoded = bytes.Repeat(empty, cpioProbeInputLimit/len(empty)+1)
	encoded = append(encoded, compressed...)

	output = unpackFixture(t, encoded, "cpio.gz")
	assertArchiveFile(t, output, "dir/file", []byte("legacy archive payload\n"))
}

func TestCPIOProbeTruncatedStreams(t *testing.T) {
	codecs := []string{"gz", "bz2", "xz", "zst"}

	for _, codec := range codecs {
		t.Run(codec, func(t *testing.T) {
			payload := legacyFixture(t, "newc.cpio")
			encoded := compressCPIOProbe(t, payload, codec)
			assertCPIOAutoRejected(t, encoded[:len(encoded)-1])

			encoded = compressCPIOProbe(t, []byte("070701ordinary compressed data"), codec)
			assertCPIOAutoRejected(t, encoded[:len(encoded)-1])
			assertCPIOAutoRejected(t, encoded[:8])
		})
	}
}

func TestCPIOProbeLargeBZIP2Block(t *testing.T) {
	var archive bytes.Buffer

	payload := make([]byte, 900000)
	random := rand.New(rand.NewPCG(1, 2))

	for index := range payload {
		payload[index] = byte(random.Uint32())
	}

	appendCPIOTestEntry(&archive, "large", string(payload), 0o100644, 1)
	appendCPIOTestEntry(&archive, "TRAILER!!!", "", 0, 1)

	encoded := compressCPIOProbe(t, archive.Bytes(), "bz2")
	assertCPIOProbe(t, encoded, "bz2", "cpio.bz2")

	output := unpackFixture(t, encoded, "")
	assertArchiveFile(t, output, "large", payload)
}

func compressCPIOProbe(t *testing.T, data []byte, codec string) []byte {
	t.Helper()

	var encoded bytes.Buffer

	format, err := ParseFormat(codec)
	if err != nil {
		t.Fatal(err)
	}

	writer, err := native.NewWriter(&encoded, format.Codec, 9, 1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write(data)
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}

func assertCPIOAutoRejected(t *testing.T, encoded []byte) {
	t.Helper()

	err := Unpack(t.Context(), "-", Options{Output: t.TempDir(), Threads: 1}, bytes.NewReader(encoded))
	if err == nil {
		t.Fatal("truncated compressed stream accepted")
	}
}

func assertCPIOProbe(t *testing.T, encoded []byte, codec, expected string) {
	t.Helper()

	format, _ := ParseFormat(codec)
	want, _ := ParseFormat(expected)

	input := bufio.NewReaderSize(bytes.NewReader(encoded), 512)
	_, _ = input.Peek(512)

	replay, detected, err := probeCPIOStream(input, format)
	if err != nil || detected != want {
		t.Fatalf("stdin probe: got %s, want %s: %v", detected.Name, want.Name, err)
	}

	actual, err := io.ReadAll(replay)
	if err != nil || !bytes.Equal(actual, encoded) {
		t.Fatalf("stdin replay: %v", err)
	}

	path := filepath.Join(t.TempDir(), "input."+codec)

	err = os.WriteFile(path, encoded, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	detected, err = detectFormat(file)
	if err != nil || detected != want {
		t.Fatalf("file probe: got %s, want %s: %v", detected.Name, want.Name, err)
	}
}
