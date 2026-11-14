package native

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
)

const xzIndexProbeLimit = 4096

// XZDecoderThreads bounds decoder concurrency by the block count of a single
// stream. Unsupported layouts and failed probes retain the requested budget;
// this hint never replaces validation by the decoder.
func XZDecoderThreads(source io.ReaderAt, size int64, threads int) int {
	if threads <= 1 || size < 32 {
		return threads
	}

	var footer [12]byte

	_, err := source.ReadAt(footer[:], size-12)
	if err != nil || footer[10] != 'Y' || footer[11] != 'Z' || !xzChecksum(footer[4:10], footer[:4]) {
		return threads
	}

	indexSize := (int64(binary.LittleEndian.Uint32(footer[4:8])) + 1) * 4
	if indexSize < 8 || indexSize > xzIndexProbeLimit || indexSize > size-24 {
		return threads
	}

	var header [12]byte

	_, err = source.ReadAt(header[:], 0)
	if err != nil || !bytes.Equal(header[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0}) || !bytes.Equal(header[6:8], footer[8:10]) || !xzChecksum(header[6:8], header[8:12]) {
		return threads
	}

	var buffer [xzIndexProbeLimit]byte

	index := buffer[:indexSize]
	_, err = source.ReadAt(index, size-12-indexSize)

	if err != nil || index[0] != 0 || !xzChecksum(index[:len(index)-4], index[len(index)-4:]) {
		return threads
	}

	index = index[1 : len(index)-4]

	blocks, count := binary.Uvarint(index)
	if count <= 0 || blocks >= uint64(threads) {
		return threads
	}

	index = index[count:]
	remaining := uint64(size - indexSize - 24)

	for range blocks {
		unpadded, count := binary.Uvarint(index)
		if count <= 0 || unpadded < 5 || unpadded > remaining {
			return threads
		}

		index = index[count:]

		_, count = binary.Uvarint(index)
		if count <= 0 {
			return threads
		}

		index = index[count:]

		padded := (unpadded + 3) &^ uint64(3)
		if padded > remaining {
			return threads
		}

		remaining -= padded
	}

	// Accounting for the complete file excludes concatenated streams. They may
	// have more blocks in an earlier stream than the final index reports.
	if remaining != 0 || len(index) > 3 {
		return threads
	}

	for _, padding := range index {
		if padding != 0 {
			return threads
		}
	}

	return max(1, int(blocks))
}

func xzChecksum(data, checksum []byte) bool {
	return crc32.ChecksumIEEE(data) == binary.LittleEndian.Uint32(checksum)
}
