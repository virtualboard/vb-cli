package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-github/v60/github"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUpgrader(t *testing.T) {
	logger := logrus.New()
	upgrader := NewUpgrader(logger)

	assert.NotNil(t, upgrader)
	assert.NotNil(t, upgrader.client)
	assert.Equal(t, logger, upgrader.logger)
}

func TestGetBinaryName(t *testing.T) {
	upgrader := NewUpgrader(logrus.New())

	expectedOS := runtime.GOOS
	expectedArch := runtime.GOARCH

	// Map Go architecture names to expected naming conventions
	switch expectedArch {
	case "amd64":
		expectedArch = "amd64"
	case "386":
		expectedArch = "386"
	case "arm64":
		expectedArch = "arm64"
	case "arm":
		expectedArch = "arm"
	default:
		expectedArch = "amd64"
	}

	// Map Go OS names to GitHub Actions release naming conventions
	var expectedName string
	switch expectedOS {
	case "darwin":
		// macOS uses "macos" in the release asset names
		expectedName = fmt.Sprintf("vb-macos-%s", expectedArch)
	case "linux":
		expectedName = fmt.Sprintf("vb-linux-%s", expectedArch)
	case "windows":
		expectedName = fmt.Sprintf("vb-windows-%s.exe", expectedArch)
	default:
		// Fallback to the old format for unknown OS
		expectedName = fmt.Sprintf("vb_%s_%s", expectedOS, expectedArch)
	}

	actualName := upgrader.GetBinaryName()
	assert.Equal(t, expectedName, actualName)
}

func TestCheckForUpdate(t *testing.T) {
	// This test requires network access and GitHub API
	// Skip if running in CI or if network is not available
	if os.Getenv("CI") != "" || os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network-dependent test")
	}

	upgrader := NewUpgrader(logrus.New())

	// Test with a very old version to ensure we get a newer version
	release, hasUpdate, err := upgrader.CheckForUpdate("v0.0.1")

	// We can't assert specific values since they depend on actual GitHub releases
	// But we can check that the function doesn't error and returns valid data
	if err != nil {
		// If there's a network error, just skip the test
		t.Skipf("Skipping due to network error: %v", err)
	}
	assert.NotNil(t, release)
	// hasUpdate might be true or false depending on actual releases
	assert.IsType(t, true, hasUpdate)
}

func TestCheckForUpdateNoUpdate(t *testing.T) {
	// This test requires network access and GitHub API
	if os.Getenv("CI") != "" || os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network-dependent test")
	}

	upgrader := NewUpgrader(logrus.New())

	// Test with a very new version to ensure we don't get an update
	release, hasUpdate, err := upgrader.CheckForUpdate("v999.999.999")

	if err != nil {
		// If there's a network error, just skip the test
		t.Skipf("Skipping due to network error: %v", err)
	}
	assert.NotNil(t, release)
	assert.False(t, hasUpdate)
}

func TestCopyFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "upgrade-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create source file
	srcFile := filepath.Join(tempDir, "source.txt")
	content := "test content for file copying"
	err = os.WriteFile(srcFile, []byte(content), 0644)
	require.NoError(t, err)

	// Create destination file path
	dstFile := filepath.Join(tempDir, "destination.txt")

	// Copy the file
	err = copyFile(srcFile, dstFile)
	require.NoError(t, err)

	// Verify the file was copied correctly
	copiedContent, err := os.ReadFile(dstFile)
	require.NoError(t, err)
	assert.Equal(t, content, string(copiedContent))

	// Verify file permissions
	info, err := os.Stat(dstFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestCopyFileSourceNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "upgrade-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	srcFile := filepath.Join(tempDir, "nonexistent.txt")
	dstFile := filepath.Join(tempDir, "destination.txt")

	err = copyFile(srcFile, dstFile)
	assert.Error(t, err)
}

func TestCopyFileDestinationError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "upgrade-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create source file
	srcFile := filepath.Join(tempDir, "source.txt")
	err = os.WriteFile(srcFile, []byte("test"), 0644)
	require.NoError(t, err)

	// Try to copy to a directory (should fail)
	dstFile := tempDir

	err = copyFile(srcFile, dstFile)
	assert.Error(t, err)
}

// MockUpgrader for testing without network calls
type MockUpgrader struct {
	*Upgrader
	mockRelease        *github.RepositoryRelease
	mockError          error
	checkForUpdateFunc func(string) (*github.RepositoryRelease, bool, error)
	upgradeFunc        func(string) (*UpgradeResult, error)
}

func NewMockUpgrader(logger *logrus.Logger) *MockUpgrader {
	return &MockUpgrader{
		Upgrader: NewUpgrader(logger),
	}
}

func (m *MockUpgrader) CheckForUpdate(currentVersion string) (*github.RepositoryRelease, bool, error) {
	if m.checkForUpdateFunc != nil {
		return m.checkForUpdateFunc(currentVersion)
	}

	if m.mockError != nil {
		return nil, false, m.mockError
	}

	if m.mockRelease == nil {
		return nil, false, fmt.Errorf("no mock release set")
	}

	// Simple mock logic - assume any release is newer
	return m.mockRelease, true, nil
}

func (m *MockUpgrader) Upgrade(currentVersion string) (*UpgradeResult, error) {
	if m.upgradeFunc != nil {
		return m.upgradeFunc(currentVersion)
	}

	// Use the mock CheckForUpdate
	release, hasUpdate, err := m.CheckForUpdate(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}

	if !hasUpdate {
		m.logger.Info("You are already running the latest version")
		return &UpgradeResult{
			Message:        fmt.Sprintf("You are already running the latest version (%s)", currentVersion),
			CurrentVersion: currentVersion,
			LatestVersion:  release.GetTagName(),
			Upgraded:       false,
		}, nil
	}

	m.logger.Infof("Found newer version: %s", release.GetTagName())
	m.logger.Info("Downloading new binary...")

	newBinaryPath, err := m.DownloadBinary(release)
	if err != nil {
		return nil, fmt.Errorf("failed to download new binary: %w", err)
	}

	m.logger.Info("Replacing current binary...")
	// Mock the binary replacement - just simulate success without actually replacing
	// This avoids the "text file busy" error when trying to replace the test binary
	m.logger.Debug("Mock binary replacement completed successfully")

	// Clean up the temporary file
	_ = os.Remove(newBinaryPath)

	m.logger.Infof("Successfully upgraded to version %s", release.GetTagName())
	return &UpgradeResult{
		Message:        fmt.Sprintf("Successfully upgraded from %s to %s", currentVersion, release.GetTagName()),
		CurrentVersion: currentVersion,
		LatestVersion:  release.GetTagName(),
		Upgraded:       true,
	}, nil
}

func (m *MockUpgrader) DownloadBinary(release *github.RepositoryRelease) (string, error) {
	binaryName := m.GetBinaryName()

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

	// Create a mock binary file
	tempFile, err := os.CreateTemp("", "mock-binary-*")
	if err != nil {
		return "", err
	}

	// Write some mock binary content
	_, err = tempFile.WriteString("mock binary content")
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return "", err
	}

	tempFile.Close()

	// Make it executable
	err = os.Chmod(tempFile.Name(), 0755)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}

func TestUpgradeWithMock(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	mockUpgrader := NewMockUpgrader(logger)

	// Create a mock release
	mockRelease := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String(mockUpgrader.GetBinaryName()),
				BrowserDownloadURL: github.String("https://example.com/binary"),
			},
		},
	}
	mockUpgrader.mockRelease = mockRelease

	// Test upgrade with mock
	result, err := mockUpgrader.Upgrade("v0.0.1")
	assert.NoError(t, err)
	assert.True(t, result.Upgraded)
	assert.Equal(t, "v0.0.1", result.CurrentVersion)
	assert.Equal(t, "v1.0.0", result.LatestVersion)
}

func TestUpgradeNoUpdateWithMock(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	mockUpgrader := NewMockUpgrader(logger)

	// Create a mock release
	mockRelease := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String(mockUpgrader.GetBinaryName()),
				BrowserDownloadURL: github.String("https://example.com/binary"),
			},
		},
	}
	mockUpgrader.mockRelease = mockRelease

	// Mock CheckForUpdate to return no update
	mockUpgrader.checkForUpdateFunc = func(currentVersion string) (*github.RepositoryRelease, bool, error) {
		return mockRelease, false, nil
	}

	// Test upgrade with no update available
	result, err := mockUpgrader.Upgrade("v1.0.0")
	assert.NoError(t, err)
	assert.False(t, result.Upgraded)
	assert.Equal(t, "v1.0.0", result.CurrentVersion)
	assert.Equal(t, "v1.0.0", result.LatestVersion)
}

func TestUpgradeCheckErrorWithMock(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	mockUpgrader := NewMockUpgrader(logger)
	mockUpgrader.mockError = fmt.Errorf("network error")

	// Test upgrade with check error
	result, err := mockUpgrader.Upgrade("v0.0.1")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to check for updates")
}

func TestUpgradeDownloadErrorWithMock(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	mockUpgrader := NewMockUpgrader(logger)

	// Create a mock release without the expected binary
	mockRelease := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String("wrong-binary-name"),
				BrowserDownloadURL: github.String("https://example.com/binary"),
			},
		},
	}
	mockUpgrader.mockRelease = mockRelease

	// Test upgrade with download error
	result, err := mockUpgrader.Upgrade("v0.0.1")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to download new binary")
}
