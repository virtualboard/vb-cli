package report

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/virtualboard/vb-cli/internal/util"
)

// WriteOutput publishes a fully rendered report. Without force, link creation
// atomically refuses any existing destination, including a racing writer.
func WriteOutput(path string, data []byte, force bool) error {
	if force {
		return util.WriteFileAtomic(path, data, 0o600)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".vb-report-*")
	if err != nil {
		return err
	}
	// #nosec G104 -- best-effort removal of the private temporary link after publication.
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		// #nosec G104 -- retain the original write error during cleanup.
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		return fmt.Errorf("publish report without replacing existing output: %w", err)
	}
	return nil
}
