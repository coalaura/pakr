package pak

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func setFileTime(file *os.File, modified time.Time) error {
	value := windows.NsecToFiletime(modified.UnixNano())

	return windows.SetFileTime(windows.Handle(file.Fd()), nil, &value, &value)
}
