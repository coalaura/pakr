//go:build !windows

package pak

import (
	"io/fs"
	"syscall"
)

func fileOwnership(info fs.FileInfo) (uint32, uint32) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid, stat.Gid
	}

	return 0, 0
}
