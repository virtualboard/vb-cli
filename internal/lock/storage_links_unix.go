//go:build !windows

package lock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func requireSingleLink(file *os.File) error {
	var stat unix.Stat_t
	// #nosec G115 -- os.File descriptors originate from platform int values.
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("expected one hard link, found %d", stat.Nlink)
	}
	return nil
}
