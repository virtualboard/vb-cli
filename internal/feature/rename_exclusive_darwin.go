//go:build darwin

package feature

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameFeaturePathExclusive(_ string, root *os.File, oldRelative, newRelative string) error {
	// #nosec G115 -- os.File.Fd returns the platform's native integer file
	// descriptor represented as uintptr; RenameatxNp requires that same fd as int.
	directoryFD := int(root.Fd())
	return unix.RenameatxNp(directoryFD, oldRelative, directoryFD, newRelative, unix.RENAME_EXCL)
}
