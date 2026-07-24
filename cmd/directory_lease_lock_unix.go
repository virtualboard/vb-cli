//go:build !windows

package cmd

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockDirectoryReplaceLease(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) // #nosec G115 -- file descriptors fit int on supported Unix targets
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockDirectoryReplaceLease(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN) // #nosec G115 -- file descriptors fit int on supported Unix targets
}
