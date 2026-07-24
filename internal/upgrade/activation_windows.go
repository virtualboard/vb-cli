//go:build windows

package upgrade

import (
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

func unlinkOpenDownload(string) error {
	// Windows does not generally allow unlinking an open file. Close removes the
	// private path; self-replacement is rejected before activation.
	return nil
}

func openUpgradeGuard(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- deterministic sibling of the executable
}

func lockUpgradeGuard(file *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
	runtime.KeepAlive(file)
	return err
}

func unlockUpgradeGuard(file *os.File) error {
	var overlapped windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	runtime.KeepAlive(file)
	return err
}

func syncUpgradeDirectory(string) error { return nil }
