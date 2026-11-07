package pak

import "io/fs"

func fileOwnership(info fs.FileInfo) (uint32, uint32) {
	return 0, 0
}
