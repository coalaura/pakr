package pak

import (
	"os"

	"golang.org/x/sys/unix"
)

// Regular files ignore O_NONBLOCK. Supplying it avoids the runtime's redundant
// fcntl toggles around a failed epoll registration, and lets Stat reject a FIFO
// substituted after collection without blocking in open.
const inputOpenFlags = os.O_RDONLY | unix.O_NONBLOCK
