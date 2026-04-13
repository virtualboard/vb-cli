package upgrade

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/version"
)

// httpGetter abstracts HTTP GET requests for testability.
type httpGetter interface {
	Get(url string) (*http.Response, error)
}

const (
	// Repository owner and name for the vb-cli project
	repoOwner = "virtualboard"
	repoName  = "vb-cli"
)

// Upgrader handles the upgrade process for the vb binary
type Upgrader struct {
	client     *github.Client
	logger     *logrus.Logger
	httpClient httpGetter
}

// NewUpgrader creates a new upgrader instance
func NewUpgrader(logger *logrus.Logger) *Upgrader {
	return &Upgrader{
		client:     github.NewClient(nil),
		logger:     logger,
		httpClient: http.DefaultClient,
	}
}

// CheckForUpdate checks if there's a newer version available
func (u *Upgrader) CheckForUpdate(currentVersion string) (*github.RepositoryRelease, bool, error) {
	u.logger.Debug("Checking for updates...")

	release, _, err := u.client.Repositories.GetLatestRelease(context.Background(), repoOwner, repoName)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get latest release: %w", err)
	}

	latestVersion := release.GetTagName()

	u.logger.Debugf("Current version: %s, Latest version: %s", currentVersion, latestVersion)

	newer, err := version.IsNewer(latestVersion, currentVersion)
	if err != nil {
		return nil, false, fmt.Errorf("failed to compare versions: %w", err)
	}
	if newer {
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
	// #nosec G107 -- URL comes from trusted GitHub API release asset
	resp, err := u.httpClient.Get(asset.GetBrowserDownloadURL())
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
		// #nosec G703 - tmpFile.Name() is safe, comes from os.CreateTemp
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write binary to temporary file: %w", err)
	}

	// Verify checksum against release checksums.txt
	u.logger.Debug("Verifying binary checksum...")
	checksumData, err := u.downloadChecksumsFile(release)
	if err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to download checksums: %w", err)
	}

	checksums, err := parseChecksums(checksumData)
	if err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to parse checksums: %w", err)
	}

	expectedHash, ok := checksums[binaryName]
	if !ok {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("no checksum found for %s in checksums.txt", binaryName)
	}

	if err := verifyChecksum(tmpFile.Name(), expectedHash); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}
	u.logger.Debug("Checksum verified successfully")

	// Make the temporary file executable
	// #nosec G302 G703 - executable binary requires 0755 permissions, tmpFile.Name() is safe from os.CreateTemp
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		// #nosec G703 - tmpFile.Name() is safe, comes from os.CreateTemp
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

// downloadChecksumsFile downloads the checksums.txt file from a release.
func (u *Upgrader) downloadChecksumsFile(release *github.RepositoryRelease) ([]byte, error) {
	var checksumAsset *github.ReleaseAsset
	for _, a := range release.Assets {
		if a.GetName() == "checksums.txt" {
			checksumAsset = a
			break
		}
	}
	if checksumAsset == nil {
		return nil, fmt.Errorf("checksums.txt not found in release %s", release.GetTagName())
	}

	// #nosec G107 -- URL comes from trusted GitHub API release asset
	resp, err := u.httpClient.Get(checksumAsset.GetBrowserDownloadURL())
	if err != nil {
		return nil, fmt.Errorf("failed to download checksums.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download checksums.txt: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read checksums.txt: %w", err)
	}
	return data, nil
}

// parseChecksums parses sha256sum output into a map of filename -> hash.
// Expected format: "<hash>  ./<filename>" (two spaces, with optional ./ prefix).
func parseChecksums(data []byte) (map[string]string, error) {
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// sha256sum format: "<hash>  <filename>" (two spaces)
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			continue
		}
		hash := parts[0]
		name := strings.TrimPrefix(parts[1], "./")
		checksums[name] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("no valid checksum entries found")
	}
	return checksums, nil
}

// verifyChecksum computes SHA-256 of a file and compares to the expected hash.
func verifyChecksum(filePath, expectedHash string) error {
	// #nosec G304 -- filePath comes from os.CreateTemp in DownloadBinary
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expectedHash {
		return fmt.Errorf("expected %s, got %s", expectedHash, actual)
	}
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

// UpgradeResult contains the result of an upgrade operation
type UpgradeResult struct {
	Message        string
	CurrentVersion string
	LatestVersion  string
	Upgraded       bool
}

// Upgrade performs the complete upgrade process
func (u *Upgrader) Upgrade(currentVersion string) (*UpgradeResult, error) {
	u.logger.Info("Checking for updates...")

	release, hasUpdate, err := u.CheckForUpdate(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}

	if !hasUpdate {
		u.logger.Info("You are already running the latest version")
		return &UpgradeResult{
			Message:        fmt.Sprintf("You are already running the latest version (%s)", currentVersion),
			CurrentVersion: currentVersion,
			LatestVersion:  release.GetTagName(),
			Upgraded:       false,
		}, nil
	}

	u.logger.Infof("Found newer version: %s", release.GetTagName())
	u.logger.Info("Downloading new binary...")

	newBinaryPath, err := u.DownloadBinary(release)
	if err != nil {
		return nil, fmt.Errorf("failed to download new binary: %w", err)
	}

	u.logger.Info("Replacing current binary...")
	if err := u.ReplaceBinary(newBinaryPath); err != nil {
		return nil, fmt.Errorf("failed to replace binary: %w", err)
	}

	u.logger.Infof("Successfully upgraded to version %s", release.GetTagName())
	return &UpgradeResult{
		Message:        fmt.Sprintf("Successfully upgraded from %s to %s", currentVersion, release.GetTagName()),
		CurrentVersion: currentVersion,
		LatestVersion:  release.GetTagName(),
		Upgraded:       true,
	}, nil
}
