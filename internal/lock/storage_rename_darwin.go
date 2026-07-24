//go:build darwin

package lock

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameStoragePathExclusive(directory *os.File, source, destination string) error {
	// #nosec G115 -- os.File descriptors originate from platform int values.
	fd := int(directory.Fd())
	return unix.RenameatxNp(fd, source, fd, destination, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY)
}
