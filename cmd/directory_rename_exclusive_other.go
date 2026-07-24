//go:build !darwin && !linux && !windows

package cmd

import (
	"errors"
	"os"
)

func renameDirectoryExclusiveOS(_ *os.File, _, _ string) error {
	return errors.New("exclusive directory activation is unsupported on this platform")
}
