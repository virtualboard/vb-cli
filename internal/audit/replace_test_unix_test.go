//go:build !windows

package audit

import "os"

func replaceAuditTestPath(source, destination string) error {
	return os.Rename(source, destination)
}
