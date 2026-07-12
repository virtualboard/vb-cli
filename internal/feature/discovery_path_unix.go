//go:build !windows

package feature

import "os"

func featurePathIsLinked(info os.FileInfo) bool {
	return info != nil && info.Mode()&os.ModeSymlink != 0
}
