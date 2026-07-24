//go:build linux

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameDirectoryExclusiveOS(parent *os.File, sourceName, destinationName string) error {
	// #nosec G115 -- os.File descriptors originate from platform int values.
	fd := int(parent.Fd())
	return unix.Renameat2(fd, sourceName, fd, destinationName, unix.RENAME_NOREPLACE)
}
