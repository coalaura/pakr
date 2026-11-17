//go:build !linux

package pak

import "os"

const inputOpenFlags = os.O_RDONLY
