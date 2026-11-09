package pak

/*
#include <sys/stat.h>
*/
import "C"

import (
	"os"
	"runtime"
	"time"
)

func setFileTime(file *os.File, modified time.Time) error {
	value := C.struct_timespec{tv_sec: C.time_t(modified.Unix()), tv_nsec: C.long(modified.Nanosecond())}
	times := [2]C.struct_timespec{value, value}

	// libc uses utimensat with a NULL pathname, supported since Linux 2.6.22,
	// rather than AT_EMPTY_PATH (Linux 5.8) or a procfs pathname fallback.
	result, err := C.futimens(C.int(file.Fd()), &times[0])

	runtime.KeepAlive(file)

	if result != 0 {
		return err
	}

	return nil
}
