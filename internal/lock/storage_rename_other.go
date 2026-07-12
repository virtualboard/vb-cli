//go:build !darwin && !linux && !windows

package lock

import (
	"errors"
	"os"
)

func renameStoragePathExclusive(_ *os.File, _, _ string) error {
	return errors.New("identity-bound exclusive lock rename is unsupported on this platform")
}
