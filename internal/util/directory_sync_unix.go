//go:build !windows

package util

import "os"

func syncDirectoryPath(path string) error {
	directory, err := os.Open(path) // #nosec G304 -- caller supplies the parent of an authorized atomic-write target
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

func syncDirectoryFile(directory *os.File) error {
	return directory.Sync()
}
