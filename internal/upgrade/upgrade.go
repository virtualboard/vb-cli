package upgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/sirupsen/logrus"
)

const (
	// Repository owner and name for the vb-cli project
	repoOwner = "virtualboard"
	repoName  = "vb-cli"
)

// Upgrader handles the upgrade process for the vb binary
type Upgrader struct {
	client *github.Client
	logger *logrus.Logger
}

// NewUpgrader creates a new upgrader instance
func NewUpgrader(logger *logrus.Logger) *Upgrader {
	return &Upgrader{
		client: github.NewClient(nil),
		logger: logger,
	}
}

// CheckForUpdate checks if there's a newer version available
func (u *Upgrader) CheckForUpdate(currentVersion string) (*github.RepositoryRelease, bool, error) {
	u.logger.Debug("Checking for updates...")

	release, _, err := u.client.Repositories.GetLatestRelease(context.Background(), repoOwner, repoName)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get latest release: %w", err)
	}

	// Remove 'v' prefix for comparison
	latestVersion := strings.TrimPrefix(release.GetTagName(), "v")
	currentVersion = strings.TrimPrefix(currentVersion, "v")

	u.logger.Debugf("Current version: %s, Latest version: %s", currentVersion, latestVersion)

	// Simple string comparison - in a real implementation, you'd want semantic version comparison
	if latestVersion > currentVersion {
		return release, true, nil
	}

	return release, false, nil
}

// GetBinaryName returns the expected binary name for the current platform
func (u *Upgrader) GetBinaryName() string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go architecture names to common release naming conventions
	switch arch {
	case "amd64":
		arch = "amd64"
	case "386":
		arch = "386"
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "arm"
	default:
		arch = "amd64" // fallback
	}

	// Map Go OS names to GitHub Actions release naming conventions
	// This matches the platform names used in .github/workflows/release.yml and auto-release.yml
	switch os {
	case "darwin":
		// macOS uses "macos" in the release asset names
		return fmt.Sprintf("vb-macos-%s", arch)
	case "linux":
		return fmt.Sprintf("vb-linux-%s", arch)
	case "windows":
		return fmt.Sprintf("vb-windows-%s.exe", arch)
	default:
		// Fallback to the old format for unknown OS
		return fmt.Sprintf("vb_%s_%s", os, arch)
	}
}

// DownloadBinary downloads the binary for the current platform from a release
func (u *Upgrader) DownloadBinary(release *github.RepositoryRelease) (string, error) {
	binaryName := u.GetBinaryName()
	u.logger.Debugf("Looking for binary: %s", binaryName)

	// Find the asset with the matching binary name
	var asset *github.ReleaseAsset
	for _, a := range release.Assets {
		if a.GetName() == binaryName {
			asset = a
			break
		}
	}

	if asset == nil {
		return "", fmt.Errorf("binary %s not found in release %s", binaryName, release.GetTagName())
	}

	u.logger.Debugf("Found binary asset: %s", asset.GetName())

	// Download the binary
	resp, err := http.Get(asset.GetBrowserDownloadURL())
	if err != nil {
		return "", fmt.Errorf("failed to download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download binary: HTTP %d", resp.StatusCode)
	}

	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "vb-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tmpFile.Close()

	// Copy the binary content to the temporary file
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write binary to temporary file: %w", err)
	}

	// Make the temporary file executable
	// #nosec G302 -- executable binary requires 0755 permissions
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to make binary executable: %w", err)
	}

	u.logger.Debugf("Downloaded binary to: %s", tmpFile.Name())
	return tmpFile.Name(), nil
}

// ReplaceBinary replaces the current binary with the new one
func (u *Upgrader) ReplaceBinary(newBinaryPath string) error {
	// Get the path of the current executable
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	u.logger.Debugf("Current binary path: %s", currentPath)
	u.logger.Debugf("New binary path: %s", newBinaryPath)

	// Get the directory of the current executable
	currentDir := filepath.Dir(currentPath)
	currentName := filepath.Base(currentPath)

	// Create a backup of the current binary
	backupPath := filepath.Join(currentDir, currentName+".backup")
	if err := copyFile(currentPath, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	u.logger.Debugf("Created backup at: %s", backupPath)

	// Replace the current binary
	if err := copyFile(newBinaryPath, currentPath); err != nil {
		// Try to restore from backup
		_ = copyFile(backupPath, currentPath)
		_ = os.Remove(backupPath)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	// Make the new binary executable
	// #nosec G302 -- executable binary requires 0755 permissions
	if err := os.Chmod(currentPath, 0755); err != nil {
		// Try to restore from backup
		_ = copyFile(backupPath, currentPath)
		_ = os.Remove(backupPath)
		return fmt.Errorf("failed to make new binary executable: %w", err)
	}

	// Remove the backup and temporary file
	_ = os.Remove(backupPath)
	_ = os.Remove(newBinaryPath)

	u.logger.Debug("Binary replacement completed successfully")
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	// #nosec G304 -- file paths are controlled and validated in calling functions
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// #nosec G304 -- file paths are controlled and validated in calling functions
	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	return destFile.Sync()
}

// Upgrade performs the complete upgrade process
func (u *Upgrader) Upgrade(currentVersion string) error {
	u.logger.Info("Checking for updates...")

	release, hasUpdate, err := u.CheckForUpdate(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !hasUpdate {
		u.logger.Info("You are already running the latest version")
		return nil
	}

	u.logger.Infof("Found newer version: %s", release.GetTagName())
	u.logger.Info("Downloading new binary...")

	newBinaryPath, err := u.DownloadBinary(release)
	if err != nil {
		return fmt.Errorf("failed to download new binary: %w", err)
	}

	u.logger.Info("Replacing current binary...")
	if err := u.ReplaceBinary(newBinaryPath); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	u.logger.Infof("Successfully upgraded to version %s", release.GetTagName())
	return nil
}
