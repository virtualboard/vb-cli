package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/contract"
)

func writeSpecTestContract(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "virtualboard.json"), contract.CanonicalJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
}
