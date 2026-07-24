//go:build darwin || linux

package contract

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLoadRejectsContractFIFOWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, fileName), 0o600); err != nil {
		t.Skipf("FIFOs unavailable: %v", err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("contract FIFO was not rejected explicitly: %v", err)
	}
}
