//go:build !windows

package audit

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockAuditAppend(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) // #nosec G115 -- supported Unix file descriptors fit int
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockAuditAppend(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN) // #nosec G115 -- supported Unix file descriptors fit int
}

func requireSingleAuditLink(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil { // #nosec G115 -- supported Unix file descriptors fit int
		return err
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("expected one hard link, found %d", stat.Nlink)
	}
	return nil
}
