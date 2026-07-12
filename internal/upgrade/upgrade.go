package upgrade

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v60/github"
	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/version"
)

const (
	repoOwner = "virtualboard"
	repoName  = "vb-cli"

	requestTimeout        = 30 * time.Second
	dialTimeout           = 10 * time.Second
	responseHeaderTimeout = 15 * time.Second
	maxAPIResponseBytes   = 4 << 20
	maxChecksumBytes      = 1 << 20
	maxBinaryBytes        = 256 << 20
	maxChecksumEntries    = 128
	maxRedirects          = 5
	upgradeUserAgent      = "virtualboard-vb-upgrader"
)

var (
	releaseTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
	checksumPattern   = regexp.MustCompile(`^([0-9A-Fa-f]{64}) [ *](?:\./)?([A-Za-z0-9][A-Za-z0-9._-]*)$`)
	renameBinary      = os.Rename
)

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Upgrader handles the upgrade process for the vb binary.
type Upgrader struct {
	client     *github.Client
	logger     *logrus.Logger
	httpClient httpDoer
}

// VerifiedDownload retains the exact open file description whose bytes were
// checked against the release manifest. Production activation copies from this
// handle rather than reopening a pathname that another process could replace.
type VerifiedDownload struct {
	mu           sync.Mutex
	file         *os.File
	expectedHash string
	releaseTag   string
	size         int64
	cleanupPath  string
	closed       bool
	// beforeVersionProbe is test-only fault injection. Production downloads
	// leave it nil.
	beforeVersionProbe func(string)
	afterActivation    func(string)
}

// Close releases the verified source handle. On platforms where an open file
// cannot be unlinked immediately, it also removes the private temporary path
// only after the handle is closed.
func (d *VerifiedDownload) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	closeErr := d.file.Close()
	var removeErr error
	if d.cleanupPath != "" {
		removeErr = os.Remove(d.cleanupPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
	}
	return errors.Join(closeErr, removeErr)
}

// NewUpgrader creates an upgrader with bounded API and asset clients. A
// GITHUB_TOKEN authenticates only GitHub API requests; it is never attached to
// release-asset requests and is never written to logs.
func NewUpgrader(logger *logrus.Logger) *Upgrader {
	if logger == nil {
		logger = logrus.New()
	}
	assetClient := newBoundedHTTPClient()
	apiHTTPClient := newBoundedHTTPClient()
	apiHTTPClient.Transport = &boundedRoundTripper{
		base:  apiHTTPClient.Transport,
		limit: maxAPIResponseBytes,
	}
	apiClient := github.NewClient(apiHTTPClient)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		apiClient = apiClient.WithAuthToken(token)
	}
	return &Upgrader{
		client:     apiClient,
		logger:     logger,
		httpClient: assetClient,
	}
}

func newBoundedHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = dialTimeout
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	transport.ExpectContinueTimeout = time.Second
	transport.MaxResponseHeaderBytes = 1 << 20
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("refusing HTTPS downgrade redirect")
			}
			return nil
		},
	}
}

type boundedRoundTripper struct {
	base  http.RoundTripper
	limit int64
}

func (t *boundedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > t.limit {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP response exceeds %d-byte limit", t.limit)
	}
	resp.Body = &boundedReadCloser{
		Reader: io.LimitReader(resp.Body, t.limit+1),
		Closer: resp.Body,
	}
	return resp, nil
}

type boundedReadCloser struct {
	io.Reader
	io.Closer
}

// CheckForUpdate checks whether GitHub's latest stable release is newer.
func (u *Upgrader) CheckForUpdate(currentVersion string) (*github.RepositoryRelease, bool, error) {
	u.logger.Debug("Checking for updates...")
	if _, err := version.Parse(currentVersion); err != nil {
		return nil, false, fmt.Errorf("invalid current version: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	release, _, err := u.client.Repositories.GetLatestRelease(ctx, repoOwner, repoName)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get latest release: %w", err)
	}
	if err := validateRelease(release); err != nil {
		return nil, false, fmt.Errorf("invalid latest release: %w", err)
	}

	latestVersion := release.GetTagName()
	u.logger.Debugf("Current version: %s, Latest version: %s", currentVersion, latestVersion)
	newer, err := version.IsNewer(latestVersion, currentVersion)
	if err != nil {
		return nil, false, fmt.Errorf("failed to compare versions: %w", err)
	}
	return release, newer, nil
}

// GetBinaryName returns the release asset for the running platform. Unsupported
// platforms return an empty name; upgrade operations return a descriptive error.
func (u *Upgrader) GetBinaryName() string {
	name, _ := binaryNameFor(runtime.GOOS, runtime.GOARCH)
	return name
}

func binaryNameFor(goos, goarch string) (string, error) {
	switch goos {
	case "darwin":
		if goarch != "amd64" && goarch != "arm64" {
			return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
		}
		return fmt.Sprintf("vb-macos-%s", goarch), nil
	case "linux":
		if goarch != "amd64" && goarch != "arm64" {
			return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
		}
		return fmt.Sprintf("vb-linux-%s", goarch), nil
	default:
		return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
}

func validateRelease(release *github.RepositoryRelease) error {
	if release == nil {
		return errors.New("release is nil")
	}
	rawTag := release.GetTagName()
	tag := strings.TrimSpace(rawTag)
	if tag != rawTag {
		return fmt.Errorf("release tag contains surrounding whitespace")
	}
	if !releaseTagPattern.MatchString(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	parsedVersion, err := version.Parse(tag)
	if err != nil {
		return fmt.Errorf("invalid release tag %q: %w", tag, err)
	}
	if parsedVersion.Prerelease != "" {
		return fmt.Errorf("prerelease tag %q cannot be installed by vb upgrade", tag)
	}
	if release.GetDraft() {
		return errors.New("draft releases cannot be installed")
	}
	if release.GetPrerelease() {
		return errors.New("prereleases cannot be installed by vb upgrade")
	}
	return nil
}

// DownloadBinary downloads and verifies the binary for the running platform.
// The returned object owns an open handle to the verified inode; callers must
// close it.
func (u *Upgrader) DownloadBinary(release *github.RepositoryRelease) (result *VerifiedDownload, retErr error) {
	if err := validateRelease(release); err != nil {
		return nil, err
	}
	binaryName, err := binaryNameFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	binaryAsset, err := uniqueAsset(release, binaryName, maxBinaryBytes)
	if err != nil {
		return nil, err
	}
	checksumAsset, err := uniqueAsset(release, "checksums.txt", maxChecksumBytes)
	if err != nil {
		return nil, err
	}

	u.logger.Debug("Downloading and validating release checksums...")
	checksumData, err := u.downloadAssetBytes(checksumAsset, maxChecksumBytes)
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}
	checksums, err := parseChecksums(checksumData)
	if err != nil {
		return nil, fmt.Errorf("parse checksums.txt: %w", err)
	}
	expectedHash, ok := checksums[binaryName]
	if !ok {
		return nil, fmt.Errorf("no checksum found for %s in checksums.txt", binaryName)
	}

	u.logger.Debugf("Downloading verified asset %s", binaryName)
	resp, cancel, err := u.openAsset(binaryAsset, maxBinaryBytes)
	if err != nil {
		return nil, fmt.Errorf("download binary: %w", err)
	}
	defer cancel()

	tmpFile, err := os.CreateTemp("", "vb-upgrade-*")
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("create temporary binary: %w", err)
	}
	tmpPath := tmpFile.Name()
	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	written, copyErr := io.Copy(tmpFile, io.LimitReader(resp.Body, maxBinaryBytes+1))
	closeBodyErr := resp.Body.Close()
	if written > maxBinaryBytes && copyErr == nil {
		copyErr = fmt.Errorf("binary exceeds %d-byte limit", maxBinaryBytes)
	}
	syncErr := tmpFile.Sync()
	if err := errors.Join(copyErr, closeBodyErr, syncErr); err != nil {
		return nil, fmt.Errorf("persist downloaded binary: %w", err)
	}
	if written == 0 {
		return nil, errors.New("downloaded binary is empty")
	}
	if err := verifyOpenFileChecksum(tmpFile, expectedHash); err != nil {
		return nil, fmt.Errorf("checksum verification failed: %w", err)
	}
	// #nosec G302 -- the private verified download must be executable for replacement.
	if err := tmpFile.Chmod(0o700); err != nil {
		return nil, fmt.Errorf("mark downloaded binary executable: %w", err)
	}
	info, err := tmpFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect verified download: %w", err)
	}
	pathInfo, err := os.Lstat(tmpPath)
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) {
		return nil, fmt.Errorf("verified download path changed before capture")
	}
	cleanupPath := tmpPath
	if err := unlinkOpenDownload(tmpPath); err != nil {
		return nil, fmt.Errorf("hide verified download path: %w", err)
	}
	if runtime.GOOS != "windows" {
		cleanupPath = ""
	}
	u.logger.Debug("Checksum verified successfully")
	success = true
	return &VerifiedDownload{
		file:         tmpFile,
		expectedHash: expectedHash,
		releaseTag:   release.GetTagName(),
		size:         written,
		cleanupPath:  cleanupPath,
	}, nil
}

func uniqueAsset(release *github.RepositoryRelease, name string, maxSize int64) (*github.ReleaseAsset, error) {
	var match *github.ReleaseAsset
	for _, asset := range release.Assets {
		if asset == nil || asset.GetName() != name {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("release %s contains duplicate %s assets", release.GetTagName(), name)
		}
		match = asset
	}
	if match == nil {
		return nil, fmt.Errorf("%s not found in release %s", name, release.GetTagName())
	}
	if match.GetSize() < 0 || int64(match.GetSize()) > maxSize {
		return nil, fmt.Errorf("asset %s has invalid size %d", name, match.GetSize())
	}
	if err := validateAssetURL(match.GetBrowserDownloadURL()); err != nil {
		return nil, fmt.Errorf("asset %s has invalid download URL: %w", name, err)
	}
	return match, nil
}

func validateAssetURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("download URL must use HTTPS and include a host")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("download URL must not contain credentials or a fragment")
	}
	return nil
}

func (u *Upgrader) openAsset(asset *github.ReleaseAsset, limit int64) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.GetBrowserDownloadURL(), nil)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", upgradeUserAgent)
	resp, err := u.httpClient.Do(req) // #nosec G107 -- URL is validated release metadata.
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	if resp.StatusCode != http.StatusOK {
		closeErr := resp.Body.Close()
		cancel()
		if closeErr != nil {
			return nil, func() {}, fmt.Errorf("HTTP %d (close response: %v)", resp.StatusCode, closeErr)
		}
		return nil, func() {}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength < -1 {
		_ = resp.Body.Close()
		cancel()
		return nil, func() {}, fmt.Errorf("invalid Content-Length %d", resp.ContentLength)
	}
	if resp.ContentLength > limit {
		_ = resp.Body.Close()
		cancel()
		return nil, func() {}, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return resp, cancel, nil
}

func (u *Upgrader) downloadAssetBytes(asset *github.ReleaseAsset, limit int64) ([]byte, error) {
	resp, cancel, err := u.openAsset(asset, limit)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if resp.ContentLength > limit {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return data, nil
}

// downloadChecksumsFile is retained as the focused checksum-download helper
// used by package tests and older internal callers.
func (u *Upgrader) downloadChecksumsFile(release *github.RepositoryRelease) ([]byte, error) {
	if err := validateRelease(release); err != nil {
		return nil, err
	}
	asset, err := uniqueAsset(release, "checksums.txt", maxChecksumBytes)
	if err != nil {
		return nil, err
	}
	return u.downloadAssetBytes(asset, maxChecksumBytes)
}

// ReplaceBinary atomically replaces the running executable using the retained
// verified source handle and a same-directory stage.
func (u *Upgrader) ReplaceBinary(download *VerifiedDownload) error {
	if download == nil {
		return errors.New("verified download is required")
	}
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get current executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(currentPath); err == nil {
		currentPath = resolved
	}
	u.logger.Debugf("Replacing binary at %s", currentPath)
	if err := replaceBinaryAt(currentPath, download); err != nil {
		return err
	}
	return nil
}

func replaceBinaryAt(currentPath string, download *VerifiedDownload) error {
	if download == nil {
		return errors.New("verified download is required")
	}
	download.mu.Lock()
	defer download.mu.Unlock()
	if download.closed || download.file == nil {
		return errors.New("verified download is closed")
	}
	if runtime.GOOS == "windows" {
		return errors.New("atomic self-replacement is unsupported on Windows; install the verified release asset manually")
	}
	sourceInfo, err := download.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect verified download: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() || sourceInfo.Size() <= 0 || sourceInfo.Size() > maxBinaryBytes || sourceInfo.Size() != download.size {
		return fmt.Errorf("verified download is not a valid unchanged regular file (size %d)", sourceInfo.Size())
	}
	if err := verifyOpenFileChecksum(download.file, download.expectedHash); err != nil {
		return fmt.Errorf("verified download changed before replacement: %w", err)
	}

	guardPath := filepath.Join(filepath.Dir(currentPath), "."+filepath.Base(currentPath)+".upgrade.lock")
	guard, err := openUpgradeGuard(guardPath)
	if err != nil {
		return fmt.Errorf("open upgrade guard: %w", err)
	}
	defer guard.Close()
	guardInfo, err := guard.Stat()
	if err != nil {
		return fmt.Errorf("inspect upgrade guard: %w", err)
	}
	guardPathInfo, err := os.Lstat(guardPath)
	if err != nil || !guardPathInfo.Mode().IsRegular() || !os.SameFile(guardInfo, guardPathInfo) {
		return errors.New("upgrade guard path is unsafe or changed")
	}
	if err := lockUpgradeGuard(guard); err != nil {
		return fmt.Errorf("acquire upgrade guard: %w", err)
	}
	defer unlockUpgradeGuard(guard) // #nosec G104 -- best effort during return
	lockedGuardInfo, err := os.Lstat(guardPath)
	if err != nil || !lockedGuardInfo.Mode().IsRegular() || !os.SameFile(guardInfo, lockedGuardInfo) {
		return errors.New("upgrade guard path changed while acquiring the lock")
	}
	currentInfo, err := os.Lstat(currentPath)
	if err != nil {
		return fmt.Errorf("inspect current binary under upgrade guard: %w", err)
	}
	if !currentInfo.Mode().IsRegular() {
		return errors.New("current binary is not a regular file")
	}

	currentDir := filepath.Dir(currentPath)
	stageDir, err := os.MkdirTemp(currentDir, "."+filepath.Base(currentPath)+".upgrade-private-*")
	if err != nil {
		return fmt.Errorf("create private upgrade stage directory: %w", err)
	}
	defer func() { _ = os.Remove(stageDir) }()
	stageDirInfo, err := os.Lstat(stageDir)
	if err != nil || !stageDirInfo.IsDir() || stageDirInfo.Mode()&os.ModeSymlink != 0 || stageDirInfo.Mode().Perm() != 0o700 {
		return errors.New("private upgrade stage directory is unsafe")
	}
	backupPath := filepath.Join(stageDir, "current-backup")
	if err := os.Link(currentPath, backupPath); err != nil {
		return fmt.Errorf("create private current-binary rollback link: %w", err)
	}
	defer func() { _ = os.Remove(backupPath) }()
	backupInfo, err := os.Lstat(backupPath)
	if err != nil || !backupInfo.Mode().IsRegular() || !os.SameFile(currentInfo, backupInfo) {
		return errors.New("current-binary rollback link does not match the guarded executable")
	}
	stage, err := os.CreateTemp(stageDir, "payload-*")
	if err != nil {
		return fmt.Errorf("create same-directory upgrade stage: %w", err)
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()

	if _, err := download.file.Seek(0, io.SeekStart); err != nil {
		_ = stage.Close()
		return fmt.Errorf("rewind verified download: %w", err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(stage, hasher), io.LimitReader(download.file, maxBinaryBytes+1))
	if written > maxBinaryBytes && copyErr == nil {
		copyErr = fmt.Errorf("downloaded binary exceeds %d-byte limit", maxBinaryBytes)
	}
	if written != download.size && copyErr == nil {
		copyErr = fmt.Errorf("verified download size changed during copy: expected %d, copied %d", download.size, written)
	}
	if copyErr == nil {
		copyErr = compareChecksum(hasher.Sum(nil), download.expectedHash)
	}
	chmodErr := stage.Chmod(currentInfo.Mode().Perm())
	syncErr := stage.Sync()
	if err := errors.Join(copyErr, chmodErr, syncErr); err != nil {
		_ = stage.Close()
		return fmt.Errorf("prepare replacement binary: %w", err)
	}
	stageInfo, err := stage.Stat()
	if err != nil {
		_ = stage.Close()
		return fmt.Errorf("inspect replacement stage: %w", err)
	}
	if err := verifyOpenFileChecksum(stage, download.expectedHash); err != nil {
		_ = stage.Close()
		return fmt.Errorf("verify replacement stage: %w", err)
	}
	if err := verifyStageIdentity(stagePath, stageInfo); err != nil {
		_ = stage.Close()
		return err
	}
	// Close the writable descriptor before executing the stage. Some kernels
	// reject or defer execution while an executable remains open for writing.
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close writable replacement stage: %w", err)
	}
	activationStage, err := os.Open(stagePath) // #nosec G304 -- private same-directory stage whose identity is rebound below
	if err != nil {
		return fmt.Errorf("open replacement stage for activation: %w", err)
	}
	activationInfo, err := activationStage.Stat()
	if err != nil || !os.SameFile(stageInfo, activationInfo) {
		_ = activationStage.Close()
		return errors.New("replacement stage changed while reopening for activation")
	}
	if download.releaseTag != "" {
		if download.beforeVersionProbe != nil {
			download.beforeVersionProbe(stagePath)
		}
		if err := verifyBinaryVersionMarker(activationStage, download.releaseTag); err != nil {
			_ = activationStage.Close()
			return err
		}
		// The version probe scans only the retained read handle. Rebind both
		// content and identity immediately afterward so the subsequent namespace
		// activation cannot silently diverge from the verified inode.
		if err := verifyOpenFileChecksum(activationStage, download.expectedHash); err != nil {
			_ = activationStage.Close()
			return fmt.Errorf("replacement stage changed during version check: %w", err)
		}
		if err := verifyStageIdentity(stagePath, stageInfo); err != nil {
			_ = activationStage.Close()
			return err
		}
	}
	if err := verifyStageIdentity(stagePath, stageInfo); err != nil {
		_ = activationStage.Close()
		return err
	}
	guardedCurrent, err := os.Lstat(currentPath)
	if err != nil || !guardedCurrent.Mode().IsRegular() || !os.SameFile(currentInfo, guardedCurrent) {
		_ = activationStage.Close()
		return errors.New("current binary changed before activation")
	}
	// On supported Unix platforms, rename over an existing file is a single
	// atomic namespace operation. A failed rename leaves the current binary in
	// place; a successful rename never exposes a missing executable path.
	if err := renameBinary(stagePath, currentPath); err != nil {
		_ = activationStage.Close()
		return fmt.Errorf("atomically activate replacement binary (current binary preserved): %w", err)
	}
	if download.afterActivation != nil {
		download.afterActivation(currentPath)
	}
	activatedInfo, err := os.Lstat(currentPath)
	if err != nil || !activatedInfo.Mode().IsRegular() || !os.SameFile(stageInfo, activatedInfo) {
		_ = activationStage.Close()
		return restoreUpgradeBackup(backupPath, currentPath, currentDir, errors.New("replacement path does not reference the verified activated inode"))
	}
	if err := verifyOpenFileChecksum(activationStage, download.expectedHash); err != nil {
		_ = activationStage.Close()
		return restoreUpgradeBackup(backupPath, currentPath, currentDir, fmt.Errorf("activated binary no longer matches the verified digest: %w", err))
	}
	if err := activationStage.Close(); err != nil {
		return restoreUpgradeBackup(backupPath, currentPath, currentDir, fmt.Errorf("close replacement activation handle: %w", err))
	}
	if err := os.Remove(backupPath); err != nil {
		return restoreUpgradeBackup(backupPath, currentPath, currentDir, fmt.Errorf("rollback-link cleanup failed after activation: %w", err))
	}
	if err := os.Remove(stageDir); err != nil {
		return fmt.Errorf("replacement activated but private stage cleanup failed: %w", err)
	}
	if err := syncUpgradeDirectory(currentDir); err != nil {
		return fmt.Errorf("replacement activated but directory sync failed: %w", err)
	}
	return nil
}

func restoreUpgradeBackup(backupPath, currentPath, currentDir string, cause error) error {
	if err := os.Rename(backupPath, currentPath); err != nil {
		return errors.Join(cause, fmt.Errorf("restore previous binary: %w", err))
	}
	if err := syncUpgradeDirectory(currentDir); err != nil {
		return errors.Join(cause, fmt.Errorf("previous binary restored but directory sync failed: %w", err))
	}
	return fmt.Errorf("%w (previous binary restored)", cause)
}

func verifyStageIdentity(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect replacement stage path: %w", err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return errors.New("replacement stage path changed before activation")
	}
	return nil
}

func verifyBinaryVersionMarker(file *os.File, expected string) error {
	if file == nil {
		return errors.New("downloaded binary version handle is nil")
	}
	marker := []byte(version.MarkerPrefix + expected + version.MarkerSuffix)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind downloaded binary for version marker: %w", err)
	}
	defer func() { _, _ = file.Seek(0, io.SeekStart) }()

	const chunkSize = 64 << 10
	window := make([]byte, chunkSize+len(marker)-1)
	carry := 0
	for {
		read, readErr := file.Read(window[carry:])
		total := carry + read
		if bytes.Contains(window[:total], marker) {
			return nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read downloaded binary version marker: %w", readErr)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		carry = len(marker) - 1
		if carry > total {
			carry = total
		}
		copy(window[:carry], window[total-carry:total])
	}
	return fmt.Errorf("downloaded binary version mismatch: release %s marker is absent", expected)
}

// parseChecksums strictly parses sha256sum output into filename -> hash.
func parseChecksums(data []byte) (map[string]string, error) {
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxChecksumBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		matches := checksumPattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			return nil, fmt.Errorf("invalid checksum entry on line %d", lineNumber)
		}
		name := matches[2]
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
			return nil, fmt.Errorf("invalid checksum filename %q on line %d", name, lineNumber)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry for %s", name)
		}
		if len(checksums) >= maxChecksumEntries {
			return nil, fmt.Errorf("checksums.txt exceeds %d entries", maxChecksumEntries)
		}
		checksums[name] = strings.ToLower(matches[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(checksums) == 0 {
		return nil, errors.New("no valid checksum entries found")
	}
	return checksums, nil
}

// verifyChecksum computes SHA-256 of a file and compares it in constant time.
func verifyChecksum(filePath, expectedHash string) error {
	// #nosec G304 -- filePath is a controlled temporary download.
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	verifyErr := verifyOpenFileChecksum(f, expectedHash)
	closeErr := f.Close()
	return errors.Join(verifyErr, closeErr)
}

func verifyOpenFileChecksum(file *os.File, expectedHash string) error {
	if file == nil {
		return errors.New("checksum source is nil")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	written, copyErr := io.Copy(h, io.LimitReader(file, maxBinaryBytes+1))
	if copyErr != nil {
		return copyErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if written > maxBinaryBytes {
		return fmt.Errorf("binary exceeds %d-byte limit", maxBinaryBytes)
	}
	return compareChecksum(h.Sum(nil), expectedHash)
}

func compareChecksum(actual []byte, expectedHash string) error {
	expected, err := hex.DecodeString(expectedHash)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("expected checksum must be exactly 64 hexadecimal characters")
	}
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return fmt.Errorf("expected %s, got %s", strings.ToLower(expectedHash), hex.EncodeToString(actual))
	}
	return nil
}

// copyFile is retained for package compatibility and tests. It checks every
// copy, sync, and close result rather than relying on deferred Close calls.
func copyFile(src, dst string) error {
	// #nosec G304 -- paths are controlled by the caller.
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	// #nosec G304 -- paths are controlled by the caller.
	destination, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		_ = source.Close()
		return err
	}
	_, copyErr := io.Copy(destination, source)
	syncErr := destination.Sync()
	closeDestinationErr := destination.Close()
	closeSourceErr := source.Close()
	return errors.Join(copyErr, syncErr, closeDestinationErr, closeSourceErr)
}

// UpgradeResult contains the result of an upgrade operation.
type UpgradeResult struct {
	Message         string
	CurrentVersion  string
	LatestVersion   string
	Upgraded        bool
	UpdateAvailable bool
}

// Check reports whether a verified stable update exists without downloading or
// replacing a binary.
func (u *Upgrader) Check(currentVersion string) (*UpgradeResult, error) {
	release, hasUpdate, err := u.CheckForUpdate(currentVersion)
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("You are already running the latest version (%s)", currentVersion)
	if hasUpdate {
		message = fmt.Sprintf("Update available from %s to %s (no files changed)", currentVersion, release.GetTagName())
	}
	return &UpgradeResult{
		Message:         message,
		CurrentVersion:  currentVersion,
		LatestVersion:   release.GetTagName(),
		Upgraded:        false,
		UpdateAvailable: hasUpdate,
	}, nil
}

// Upgrade performs the complete upgrade process.
func (u *Upgrader) Upgrade(currentVersion string) (*UpgradeResult, error) {
	u.logger.Info("Checking for updates...")
	release, hasUpdate, err := u.CheckForUpdate(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}
	if !hasUpdate {
		return &UpgradeResult{
			Message:         fmt.Sprintf("You are already running the latest version (%s)", currentVersion),
			CurrentVersion:  currentVersion,
			LatestVersion:   release.GetTagName(),
			Upgraded:        false,
			UpdateAvailable: false,
		}, nil
	}

	u.logger.Infof("Found newer version: %s", release.GetTagName())
	download, err := u.DownloadBinary(release)
	if err != nil {
		return nil, fmt.Errorf("failed to download new binary: %w", err)
	}
	defer download.Close() // #nosec G104 -- best-effort cleanup after result
	if err := u.ReplaceBinary(download); err != nil {
		return nil, fmt.Errorf("failed to replace binary: %w", err)
	}
	return &UpgradeResult{
		Message:         fmt.Sprintf("Successfully upgraded from %s to %s", currentVersion, release.GetTagName()),
		CurrentVersion:  currentVersion,
		LatestVersion:   release.GetTagName(),
		Upgraded:        true,
		UpdateAvailable: false,
	}, nil
}
