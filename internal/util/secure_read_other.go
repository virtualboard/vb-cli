//go:build !unix

package util

import "os"

func openRootReadOnlyNoFollow(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
