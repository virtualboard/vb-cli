//go:build !windows

package lock

import "os"

func replaceTestFile(source, destination string) error {
	return os.Rename(source, destination)
}
