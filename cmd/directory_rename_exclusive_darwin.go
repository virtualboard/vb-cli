//go:build darwin

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameDirectoryExclusiveOS(parent *os.File, sourceName, destinationName string) error {
	// #nosec G115 -- os.File descriptors originate from platform int values.
	fd := int(parent.Fd())
	return unix.RenameatxNp(
		fd,
		sourceName,
		fd,
		destinationName,
		unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY,
	)
}
