package feature

import (
	"fmt"
	"os"
	"path/filepath"
)

func (m *Manager) renameWithinFeatureRootExclusive(oldRelative, newRelative string) error {
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open feature transaction root handle: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := renameFeaturePathExclusive(filepath.Clean(m.opts.RootDir), directory, oldRelative, newRelative); err != nil {
		return fmt.Errorf("exclusive feature path rename: %w", err)
	}
	return nil
}

// Compile-time assertion for the platform helper signature.
var _ func(string, *os.File, string, string) error = renameFeaturePathExclusive
