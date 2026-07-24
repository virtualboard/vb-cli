//go:build !windows

package lock

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockGuardFile(file *os.File) error {
	// #nosec G115 -- os.File descriptors originate from platform int values.
	return unix.Flock(int(file.Fd()), unix.LOCK_EX)
}

func unlockGuardFile(file *os.File) error {
	// #nosec G115 -- os.File descriptors originate from platform int values.
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
