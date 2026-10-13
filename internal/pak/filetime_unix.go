//go:build linux || darwin

package pak

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func setFileTime(file *os.File, modified time.Time) error {
	value := unix.NsecToTimeval(modified.UnixNano())

	times := []unix.Timeval{value, value}

	return unix.Futimes(int(file.Fd()), times)
}
