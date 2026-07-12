//go:build windows

package feature

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func renameFeaturePathExclusive(rootPath string, _ *os.File, oldRelative, newRelative string) error {
	oldPath, err := windows.UTF16PtrFromString(filepath.Join(rootPath, oldRelative))
	if err != nil {
		return err
	}
	newPath, err := windows.UTF16PtrFromString(filepath.Join(rootPath, newRelative))
	if err != nil {
		return err
	}
	// Zero flags deliberately omit MOVEFILE_REPLACE_EXISTING.
	return windows.MoveFileEx(oldPath, newPath, 0)
}
