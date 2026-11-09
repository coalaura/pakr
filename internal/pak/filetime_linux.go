package pak

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func setFileTime(file *os.File, modified time.Time) error {
	value := unix.Timespec{Sec: modified.Unix(), Nsec: int64(modified.Nanosecond())}
	times := []unix.Timespec{value, value}

	return unix.UtimesNanoAt(int(file.Fd()), "", times, unix.AT_EMPTY_PATH)
}
