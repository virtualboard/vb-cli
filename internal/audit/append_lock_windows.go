//go:build windows

package audit

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

func tryLockAuditAppend(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	runtime.KeepAlive(file)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockAuditAppend(file *os.File) error {
	var overlapped windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	runtime.KeepAlive(file)
	return err
}

func requireSingleAuditLink(file *os.File) error {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	runtime.KeepAlive(file)
	if err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("expected one hard link, found %d", info.NumberOfLinks)
	}
	return nil
}
