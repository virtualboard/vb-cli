package upgrade

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-github/v60/github"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/virtualboard/vb-cli/internal/version"
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

	expectedName, err := binaryNameFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		assert.Empty(t, upgrader.GetBinaryName())
		return
	}
	assert.Equal(t, expectedName, upgrader.GetBinaryName())
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
	if runtime.GOOS == "windows" {
		assert.NotZero(t, info.Mode().Perm()&0o200)
	} else {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
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

func TestParseChecksums(t *testing.T) {
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "standard sha256sum output",
			input: fmt.Sprintf("%s  ./vb-macos-arm64\n%s  ./vb-linux-amd64\n", hashA, hashB),
			want: map[string]string{
				"vb-macos-arm64": hashA,
				"vb-linux-amd64": hashB,
			},
		},
		{
			name:  "without dot-slash prefix",
			input: fmt.Sprintf("%s  vb-macos-arm64\n", hashA),
			want: map[string]string{
				"vb-macos-arm64": hashA,
			},
		},
		{
			name:  "with blank lines",
			input: fmt.Sprintf("%s  ./vb-macos-arm64\n\n%s  ./vb-linux-amd64\n\n", hashA, hashB),
			want: map[string]string{
				"vb-macos-arm64": hashA,
				"vb-linux-amd64": hashB,
			},
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no valid entries",
			input:   "malformed line\nanother bad line\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseChecksums([]byte(tt.input))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	// Create a temp file with known content
	tmpDir := t.TempDir()
	content := []byte("test binary content")
	filePath := filepath.Join(tmpDir, "test-binary")
	require.NoError(t, os.WriteFile(filePath, content, 0o600))

	// Compute expected hash
	h := sha256.New()
	_, _ = h.Write(content)
	expectedHash := fmt.Sprintf("%x", h.Sum(nil))

	// Should pass with correct hash
	assert.NoError(t, verifyChecksum(filePath, expectedHash))

	// Should fail with wrong hash
	err := verifyChecksum(filePath, "0000000000000000000000000000000000000000000000000000000000000000")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected")

	// Should fail with non-existent file
	assert.Error(t, verifyChecksum(filepath.Join(tmpDir, "nonexistent"), expectedHash))
}

func TestDownloadBinaryWithChecksum(t *testing.T) {
	binaryContent := []byte("mock binary content for checksum test")
	h := sha256.New()
	_, _ = h.Write(binaryContent)
	binaryHash := fmt.Sprintf("%x", h.Sum(nil))

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	upgrader := NewUpgrader(logger)
	binaryName := requireSupportedUpgradePlatform(t)

	checksumContent := fmt.Sprintf("%s  ./%s\n", binaryHash, binaryName)

	// Create a test HTTP server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			_, _ = w.Write(binaryContent)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksumContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Inject test HTTP client
	upgrader.httpClient = server.Client()

	release := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String(binaryName),
				BrowserDownloadURL: github.String(server.URL + "/binary"),
			},
			{
				Name:               github.String("checksums.txt"),
				BrowserDownloadURL: github.String(server.URL + "/checksums.txt"),
			},
		},
	}

	download, err := upgrader.DownloadBinary(release)
	require.NoError(t, err)
	defer download.Close()

	// Verify the exact retained source handle contains the downloaded bytes.
	info, err := download.file.Stat()
	require.NoError(t, err)
	assert.True(t, info.Size() > 0)
	if runtime.GOOS != "windows" {
		if _, err := os.Lstat(download.file.Name()); !os.IsNotExist(err) {
			t.Fatalf("verified download retained a swappable pathname: %v", err)
		}
	}
}

func TestDownloadBinaryChecksumMismatch(t *testing.T) {
	binaryContent := []byte("mock binary content")

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	upgrader := NewUpgrader(logger)
	binaryName := requireSupportedUpgradePlatform(t)

	// Provide a wrong checksum
	checksumContent := fmt.Sprintf("0000000000000000000000000000000000000000000000000000000000000000  ./%s\n", binaryName)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			_, _ = w.Write(binaryContent)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksumContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	upgrader.httpClient = server.Client()

	release := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String(binaryName),
				BrowserDownloadURL: github.String(server.URL + "/binary"),
			},
			{
				Name:               github.String("checksums.txt"),
				BrowserDownloadURL: github.String(server.URL + "/checksums.txt"),
			},
		},
	}

	_, err := upgrader.DownloadBinary(release)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checksum verification failed")
}

func TestDownloadBinaryMissingChecksums(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	upgrader := NewUpgrader(logger)
	binaryName := requireSupportedUpgradePlatform(t)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("binary data"))
	}))
	defer server.Close()

	upgrader.httpClient = server.Client()

	// Release without checksums.txt asset
	release := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String(binaryName),
				BrowserDownloadURL: github.String(server.URL + "/binary"),
			},
		},
	}

	_, err := upgrader.DownloadBinary(release)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checksums.txt not found")
}

func TestDownloadChecksumsFileHTTPError(t *testing.T) {
	logger := logrus.New()
	upgrader := NewUpgrader(logger)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	upgrader.httpClient = server.Client()

	release := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String("checksums.txt"),
				BrowserDownloadURL: github.String(server.URL + "/checksums.txt"),
			},
		},
	}

	_, err := upgrader.downloadChecksumsFile(release)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestDownloadBinaryChecksumMissingEntry(t *testing.T) {
	binaryContent := []byte("mock binary")

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	upgrader := NewUpgrader(logger)
	binaryName := requireSupportedUpgradePlatform(t)

	// Checksums file exists but doesn't have an entry for our binary
	checksumContent := strings.Repeat("a", 64) + "  ./some-other-binary\n"

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			_, _ = w.Write(binaryContent)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksumContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	upgrader.httpClient = server.Client()

	release := &github.RepositoryRelease{
		TagName: github.String("v1.0.0"),
		Assets: []*github.ReleaseAsset{
			{
				Name:               github.String(binaryName),
				BrowserDownloadURL: github.String(server.URL + "/binary"),
			},
			{
				Name:               github.String("checksums.txt"),
				BrowserDownloadURL: github.String(server.URL + "/checksums.txt"),
			},
		},
	}

	_, err := upgrader.DownloadBinary(release)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no checksum found")
}

func requireSupportedUpgradePlatform(t *testing.T) string {
	t.Helper()
	name, err := binaryNameFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("in-place upgrade is unsupported on this runtime: %v", err)
	}
	return name
}

func TestBinaryNameForRejectsUnsupportedPlatforms(t *testing.T) {
	valid := map[[2]string]string{
		{"darwin", "amd64"}: "vb-macos-amd64",
		{"darwin", "arm64"}: "vb-macos-arm64",
		{"linux", "amd64"}:  "vb-linux-amd64",
		{"linux", "arm64"}:  "vb-linux-arm64",
	}
	for platform, expected := range valid {
		name, err := binaryNameFor(platform[0], platform[1])
		if err != nil || name != expected {
			t.Fatalf("binaryNameFor(%q, %q) = %q, %v", platform[0], platform[1], name, err)
		}
	}
	for _, platform := range [][2]string{{"linux", "386"}, {"windows", "amd64"}, {"windows", "arm64"}, {"freebsd", "amd64"}} {
		if name, err := binaryNameFor(platform[0], platform[1]); err == nil || name != "" {
			t.Fatalf("unsupported platform %v returned %q, %v", platform, name, err)
		}
	}
}

func TestReleaseAndAssetValidationIsStrict(t *testing.T) {
	for _, release := range []*github.RepositoryRelease{
		nil,
		{TagName: github.String("")},
		{TagName: github.String("1.2.3")},
		{TagName: github.String("v1.2")},
		{TagName: github.String(" v1.2.3 ")},
		{TagName: github.String("v1.2.3"), Draft: github.Bool(true)},
		{TagName: github.String("v1.2.3-rc.1"), Prerelease: github.Bool(true)},
		{TagName: github.String("v1.2.3-rc.1"), Prerelease: github.Bool(false)},
	} {
		if err := validateRelease(release); err == nil {
			t.Fatalf("validateRelease accepted %+v", release)
		}
	}
	valid := &github.RepositoryRelease{TagName: github.String("v1.2.3")}
	if err := validateRelease(valid); err != nil {
		t.Fatal(err)
	}

	valid.Assets = []*github.ReleaseAsset{
		{Name: github.String("checksums.txt"), BrowserDownloadURL: github.String("https://example.com/checksums.txt")},
		{Name: github.String("checksums.txt"), BrowserDownloadURL: github.String("https://example.com/duplicate")},
	}
	if _, err := uniqueAsset(valid, "checksums.txt", maxChecksumBytes); err == nil {
		t.Fatal("duplicate release assets were accepted")
	}
	valid.Assets = []*github.ReleaseAsset{{
		Name:               github.String("checksums.txt"),
		BrowserDownloadURL: github.String("http://example.com/checksums.txt"),
	}}
	if _, err := uniqueAsset(valid, "checksums.txt", maxChecksumBytes); err == nil {
		t.Fatal("insecure release asset URL was accepted")
	}
	valid.Assets[0].BrowserDownloadURL = github.String("https://user:secret@example.com/checksums.txt")
	if _, err := uniqueAsset(valid, "checksums.txt", maxChecksumBytes); err == nil {
		t.Fatal("credential-bearing release asset URL was accepted")
	}
	valid.Assets[0].BrowserDownloadURL = github.String("https://example.com/checksums.txt")
	valid.Assets[0].Size = github.Int(maxChecksumBytes + 1)
	if _, err := uniqueAsset(valid, "checksums.txt", maxChecksumBytes); err == nil {
		t.Fatal("oversized release asset metadata was accepted")
	}
}

func TestParseChecksumsRejectsMalformedAndDuplicateEntries(t *testing.T) {
	hash := strings.Repeat("a", 64)
	invalid := []string{
		"abc123  vb-linux-amd64\n",
		hash + " vb-linux-amd64\n",
		hash + "  ../vb-linux-amd64\n",
		hash + "  vb-linux-amd64\n" + hash + "  vb-linux-amd64\n",
		hash + "  vb-linux-amd64\nmalformed\n",
	}
	for _, input := range invalid {
		if _, err := parseChecksums([]byte(input)); err == nil {
			t.Fatalf("parseChecksums accepted %q", input)
		}
	}
	upper := strings.ToUpper(hash) + "  vb-linux-amd64\n"
	parsed, err := parseChecksums([]byte(upper))
	if err != nil || parsed["vb-linux-amd64"] != hash {
		t.Fatalf("uppercase checksum was not normalized: %v, %v", parsed, err)
	}
}

func TestNewUpgraderUsesBoundedClientsAndGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-secret-token")
	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)
	upgrader := NewUpgrader(logger)
	assetClient, ok := upgrader.httpClient.(*http.Client)
	if !ok || assetClient.Timeout != requestTimeout {
		t.Fatalf("asset client is not bounded: %#v", upgrader.httpClient)
	}

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	upgrader.client.BaseURL = baseURL
	upgrader.client.UploadURL = baseURL
	if _, newer, err := upgrader.CheckForUpdate("v0.1.0"); err != nil || !newer {
		t.Fatalf("authenticated update check failed: newer=%v err=%v", newer, err)
	}
	if authorization != "Bearer test-secret-token" {
		t.Fatalf("GitHub API authorization = %q", authorization)
	}
	if strings.Contains(logs.String(), "test-secret-token") {
		t.Fatal("GITHUB_TOKEN was written to updater logs")
	}
}

func TestAPIAndAssetResponsesAreBounded(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	upgrader := NewUpgrader(logrus.New())
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(maxAPIResponseBytes+1))
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()
	baseURL, err := url.Parse(apiServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	upgrader.client.BaseURL = baseURL
	upgrader.client.UploadURL = baseURL
	if _, _, err := upgrader.CheckForUpdate("v0.1.0"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized API response error = %v", err)
	}

	assetServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(maxChecksumBytes+1))
		w.WriteHeader(http.StatusOK)
	}))
	defer assetServer.Close()
	upgrader.httpClient = assetServer.Client()
	asset := &github.ReleaseAsset{BrowserDownloadURL: github.String(assetServer.URL + "/checksums.txt")}
	if _, err := upgrader.downloadAssetBytes(asset, maxChecksumBytes); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized asset response error = %v", err)
	}
}

func TestReplaceBinaryAtIsAtomicAndPreservesMode(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	download := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(download, []byte("new-binary"), 0o700))
	verified := openTestVerifiedDownload(t, download, "")
	if runtime.GOOS == "windows" {
		err := replaceBinaryAt(current, verified)
		if err == nil || !strings.Contains(err.Error(), "unsupported on Windows") {
			t.Fatalf("Windows self-replacement error = %v", err)
		}
		data, readErr := os.ReadFile(current)
		if readErr != nil || string(data) != "old-binary" {
			t.Fatalf("Windows self-replacement changed current binary: %q, %v", data, readErr)
		}
		return
	}
	if err := replaceBinaryAt(current, verified); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	require.NoError(t, err)
	if string(data) != "new-binary" {
		t.Fatalf("replacement content = %q", data)
	}
	info, err := os.Stat(current)
	require.NoError(t, err)
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("replacement mode = %o", info.Mode().Perm())
	}
	if data, err := os.ReadFile(download); err != nil || string(data) != "new-binary" {
		t.Fatalf("replaceBinaryAt unexpectedly consumed its source: %q, %v", data, err)
	}
}

func TestReplaceBinaryAtActivationFailurePreservesCurrentBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	download := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(download, []byte("new-binary"), 0o700))
	verified := openTestVerifiedDownload(t, download, "")
	originalRename := renameBinary
	renameBinary = func(string, string) error { return errors.New("injected activation failure") }
	t.Cleanup(func() { renameBinary = originalRename })
	if err := replaceBinaryAt(current, verified); err == nil || !strings.Contains(err.Error(), "current binary preserved") {
		t.Fatalf("activation failure = %v", err)
	}
	data, err := os.ReadFile(current)
	require.NoError(t, err)
	if string(data) != "old-binary" {
		t.Fatalf("preserved content = %q", data)
	}
	info, err := os.Stat(current)
	require.NoError(t, err)
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("preserved mode = %o", info.Mode().Perm())
	}
}

func TestReplaceBinaryAtRejectsVerifiedSourceMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	source := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(source, []byte("verified-binary"), 0o700))
	verified := openTestVerifiedDownload(t, source, "")
	require.NoError(t, os.WriteFile(source, []byte("swapped-binary"), 0o700))

	if err := replaceBinaryAt(current, verified); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("mutated verified source error = %v", err)
	}
	data, err := os.ReadFile(current)
	require.NoError(t, err)
	if string(data) != "old-binary" {
		t.Fatalf("source mutation replaced current binary: %q", data)
	}
}

func TestReplaceBinaryAtRequiresReleaseVersionMatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	source := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(source, []byte("mock-binary\x00"+version.MarkerPrefix+"v9.9.9"+version.MarkerSuffix+"\x00"), 0o700))
	verified := openTestVerifiedDownload(t, source, "v1.2.3")

	if err := replaceBinaryAt(current, verified); err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Fatalf("version mismatch error = %v", err)
	}
	data, err := os.ReadFile(current)
	require.NoError(t, err)
	if string(data) != "old-binary" {
		t.Fatalf("version mismatch replaced current binary: %q", data)
	}
}

func TestReplaceBinaryAtRejectsVersionMarkerPrefixCollision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	source := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(source, []byte("mock-binary\x00"+version.MarkerPrefix+"v1.2.30"+version.MarkerSuffix+"\x00"), 0o700))
	verified := openTestVerifiedDownload(t, source, "v1.2.3")

	err := replaceBinaryAt(current, verified)
	if err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Fatalf("prefix-collision error = %v", err)
	}
	data, readErr := os.ReadFile(current)
	require.NoError(t, readErr)
	if string(data) != "old-binary" {
		t.Fatalf("prefix collision replaced current binary: %q", data)
	}
}

func TestReplaceBinaryAtAcceptsMatchingReleaseVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	source := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(source, []byte("mock-binary\x00"+version.MarkerPrefix+"v1.2.3"+version.MarkerSuffix+"\x00"), 0o700))
	verified := openTestVerifiedDownload(t, source, "v1.2.3")

	if err := replaceBinaryAt(current, verified); err != nil {
		t.Fatalf("matching version replacement: %v", err)
	}
	data, err := os.ReadFile(current)
	require.NoError(t, err)
	if !bytes.Contains(data, []byte("v1.2.3")) {
		t.Fatalf("matching replacement content = %q", data)
	}
}

func TestReplaceBinaryAtRejectsVersionProbePathSwapWithoutExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	source := filepath.Join(dir, "download")
	marker := filepath.Join(dir, "attacker-executed")
	t.Setenv("VB_ATTACK_MARKER", marker)
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(source, []byte("mock-binary\x00"+version.MarkerPrefix+"v1.2.3"+version.MarkerSuffix+"\x00"), 0o700))
	verified := openTestVerifiedDownload(t, source, "v1.2.3")
	verified.beforeVersionProbe = func(stagePath string) {
		detached := stagePath + ".verified"
		if err := os.Rename(stagePath, detached); err != nil {
			t.Errorf("detach verified stage: %v", err)
			return
		}
		attacker := "#!/bin/sh\nprintf attacked > \"$VB_ATTACK_MARKER\"\nprintf 'v1.2.3\\n'\n"
		if err := os.WriteFile(stagePath, []byte(attacker), 0o750); err != nil {
			t.Errorf("publish attacker stage: %v", err)
		}
	}

	err := replaceBinaryAt(current, verified)
	if err == nil || !strings.Contains(err.Error(), "stage path changed") {
		t.Fatalf("stage swap error = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("path-swapped binary executed during version probe: %v", err)
	}
	data, readErr := os.ReadFile(current)
	require.NoError(t, readErr)
	if string(data) != "old-binary" {
		t.Fatalf("stage swap replaced current binary: %q", data)
	}
}

func TestReplaceBinaryAtRestoresCurrentAfterAmbiguousActivation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows self-replacement is intentionally unsupported")
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "vb")
	source := filepath.Join(dir, "download")
	require.NoError(t, os.WriteFile(current, []byte("old-binary"), 0o750))
	require.NoError(t, os.WriteFile(source, []byte("mock-binary\x00"+version.MarkerPrefix+"v1.2.3"+version.MarkerSuffix+"\x00"), 0o700))
	verified := openTestVerifiedDownload(t, source, "v1.2.3")
	verified.afterActivation = func(currentPath string) {
		if err := os.Rename(currentPath, currentPath+".detached-verified"); err != nil {
			t.Errorf("detach activated binary: %v", err)
			return
		}
		if err := os.WriteFile(currentPath, []byte("attacker-binary"), 0o750); err != nil {
			t.Errorf("publish attacker binary: %v", err)
		}
	}

	err := replaceBinaryAt(current, verified)
	if err == nil || !strings.Contains(err.Error(), "previous binary restored") {
		t.Fatalf("ambiguous activation error = %v", err)
	}
	data, readErr := os.ReadFile(current)
	require.NoError(t, readErr)
	if string(data) != "old-binary" {
		t.Fatalf("ambiguous activation did not restore current binary: %q", data)
	}
}

func openTestVerifiedDownload(t *testing.T, path, releaseTag string) *VerifiedDownload {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	file, err := os.Open(path)
	require.NoError(t, err)
	info, err := file.Stat()
	require.NoError(t, err)
	download := &VerifiedDownload{
		file:         file,
		expectedHash: fmt.Sprintf("%x", digest[:]),
		releaseTag:   releaseTag,
		size:         info.Size(),
	}
	t.Cleanup(func() { _ = download.Close() })
	return download
}
