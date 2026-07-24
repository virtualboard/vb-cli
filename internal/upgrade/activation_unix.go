//go:build !windows

package upgrade

import (
	"os"

	"golang.org/x/sys/unix"
)

func unlinkOpenDownload(path string) error {
	return os.Remove(path)
}

func openUpgradeGuard(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0o600) // #nosec G304 -- deterministic sibling of the executable
}

func lockUpgradeGuard(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX) // #nosec G115 -- supported descriptors fit int
}

func unlockUpgradeGuard(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN) // #nosec G115 -- supported descriptors fit int
}

func syncUpgradeDirectory(path string) error {
	directory, err := os.Open(path) // #nosec G304 -- executable parent selected by os.Executable
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
