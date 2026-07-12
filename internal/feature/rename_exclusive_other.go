//go:build !linux && !darwin && !windows

package feature

import (
	"errors"
	"os"
)

func renameFeaturePathExclusive(_ string, _ *os.File, _, _ string) error {
	return errors.New("exclusive feature path rename is unsupported on this platform")
}
