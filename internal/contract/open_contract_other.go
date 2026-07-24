//go:build !unix

package contract

import "os"

func openWorkspaceRegularFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
