//go:build windows

package util

import "os"

// Go's portable directory handles do not expose a FlushFileBuffers-compatible
// handle on all supported Windows filesystems. Atomic activation itself uses
// the platform rename primitive; hosted Windows CI exercises that path.
func syncDirectoryPath(string) error { return nil }

func syncDirectoryFile(*os.File) error { return nil }
