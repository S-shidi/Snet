// SPDX-License-Identifier: BSD-3-Clause
package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/S-shidi/snet/shared/buildinfo"
)

const (
	defaultReleasePageURL = "https://github.com/S-shidi/Snet/releases/latest"
	githubReleaseAPIURL   = "https://api.github.com/repos/S-shidi/Snet/releases/latest"
	ghproxyPrefix         = "https://ghproxy.net/"
)

var defaultReleaseAPIEndpoints = []string{
	githubReleaseAPIURL,
	ghproxyPrefix + githubReleaseAPIURL,
}

// UpgradeSupported reports whether the current platform supports in-app
// self-upgrade (desktop installer replacement or Android package installer).
// Container and Linux server deployments return false and show the host
// docker-compose upgrade command instead.
func UpgradeSupported() bool {
	return UpgradeSupportedForPlatform(runtime.GOOS)
}

// UpgradeSupportedForPlatform reports whether a given GOOS supports in-app
// self-upgrade.
func UpgradeSupportedForPlatform(goos string) bool {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin", "windows", "android":
		return true
	default:
		return false
	}
}

// UpdateCheckResp is the payload returned by GET /ctl/check-update.
type UpdateCheckResp struct {
	CurrentVersion   string            `json:"currentVersion"`
	LatestVersion    string            `json:"latestVersion"`
	HasUpdate        bool              `json:"hasUpdate"`
	UpgradeSupported bool              `json:"upgradeSupported"`
	Platform         string            `json:"platform"`
	ReleaseURL       string            `json:"releaseUrl"`
	PublishedAt      string            `json:"publishedAt,omitempty"`
	ReleaseNotes     string            `json:"releaseNotes,omitempty"`
	AssetName        string            `json:"assetName,omitempty"`
	AssetURL         string            `json:"assetUrl,omitempty"`
	SHA256URL        string            `json:"sha256Url,omitempty"`
	Assets           map[string]string `json:"assets,omitempty"`
}

// CompareVersions compares two semantic version strings (with or without a
// leading "v"). It returns -1 if a < b, 0 if a == b, and 1 if a > b.
func CompareVersions(a, b string) int {
	av := parseVersionParts(a)
	bv := parseVersionParts(b)
	n := len(av)
	if len(bv) > n {
		n = len(bv)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(av) {
			ai = av[i]
		}
		if i < len(bv) {
			bi = bv[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func parseVersionParts(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if idx := strings.IndexAny(v, "+"); idx >= 0 {
		v = v[:idx]
	}
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	parts := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts
}

type githubReleasePayload struct {
	TagName     string               `json:"tag_name"`
	HTMLURL     string               `json:"html_url"`
	PublishedAt string               `json:"published_at"`
	Body        string               `json:"body"`
	Assets      []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckUpdate queries the GitHub Releases API (falling back to ghproxy.net)
// and returns the latest version metadata for the given platform.
func CheckUpdate(ctx context.Context, platform string) (UpdateCheckResp, error) {
	return CheckLatestRelease(ctx, buildinfo.Version, platform, defaultReleaseAPIEndpoints)
}

// CheckLatestRelease queries the provided release API endpoints in order and
// builds an UpdateCheckResp for currentVersion and platform.
func CheckLatestRelease(ctx context.Context, currentVersion, platform string, endpoints []string) (UpdateCheckResp, error) {
	if strings.TrimSpace(platform) == "" {
		platform = runtime.GOOS
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	cur := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(currentVersion), "v"), "V")
	if len(endpoints) == 0 {
		endpoints = defaultReleaseAPIEndpoints
	}

	client := &http.Client{Timeout: 8 * time.Second}
	var lastErr error
	for _, ep := range endpoints {
		reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ep, nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "snet-client/"+cur)
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("release API %s: HTTP %d", ep, resp.StatusCode)
			continue
		}
		var rel githubReleasePayload
		if err := json.Unmarshal(body, &rel); err != nil {
			lastErr = err
			continue
		}
		latest := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v"), "V")
		if latest == "" {
			lastErr = errors.New("release API returned empty tag_name")
			continue
		}
		releaseURL := strings.TrimSpace(rel.HTMLURL)
		if releaseURL == "" {
			releaseURL = defaultReleasePageURL
		}
		assets, names, shaURL := resolveReleaseAssets(latest, rel.Assets)
		assetURL := assets[platform]
		assetName := names[platform]
		return UpdateCheckResp{
			CurrentVersion:   cur,
			LatestVersion:    latest,
			HasUpdate:        CompareVersions(cur, latest) < 0,
			UpgradeSupported: UpgradeSupportedForPlatform(platform),
			Platform:         platform,
			ReleaseURL:       releaseURL,
			PublishedAt:      rel.PublishedAt,
			ReleaseNotes:     rel.Body,
			AssetName:        assetName,
			AssetURL:         assetURL,
			SHA256URL:        shaURL,
			Assets:           assets,
		}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("无法连接更新服务器")
	}
	return UpdateCheckResp{
		CurrentVersion:   cur,
		UpgradeSupported: UpgradeSupportedForPlatform(platform),
		Platform:         platform,
		ReleaseURL:       defaultReleasePageURL,
	}, lastErr
}

func resolveReleaseAssets(version string, raw []githubReleaseAsset) (urls map[string]string, names map[string]string, windowsSHA256URL string) {
	urls = make(map[string]string, 4)
	names = make(map[string]string, 4)
	for _, a := range raw {
		name := strings.TrimSpace(a.Name)
		dl := strings.TrimSpace(a.BrowserDownloadURL)
		if name == "" || dl == "" {
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".dmg"):
			urls["darwin"] = dl
			names["darwin"] = name
		case strings.HasSuffix(lower, ".apk"):
			urls["android"] = dl
			names["android"] = name
		case strings.HasPrefix(lower, "snet-setup-") && strings.HasSuffix(lower, ".exe"):
			urls["windows"] = dl
			names["windows"] = name
		case strings.HasSuffix(lower, "-win-setup.zip") && urls["windows"] == "":
			urls["windows"] = dl
			names["windows"] = name
		case strings.HasPrefix(lower, "sha256sums-windows") && strings.HasSuffix(lower, ".txt"):
			windowsSHA256URL = dl
			urls["windowsSha256"] = dl
		}
	}
	base := fmt.Sprintf("https://github.com/S-shidi/Snet/releases/download/v%s", version)
	if urls["darwin"] == "" {
		names["darwin"] = fmt.Sprintf("Snet-v%s-mac.dmg", version)
		urls["darwin"] = base + "/" + names["darwin"]
	}
	if urls["windows"] == "" {
		names["windows"] = fmt.Sprintf("Snet-Setup-v%s.exe", version)
		urls["windows"] = base + "/" + names["windows"]
	}
	if urls["android"] == "" {
		names["android"] = fmt.Sprintf("Snet-v%s-android.apk", version)
		urls["android"] = base + "/" + names["android"]
	}
	return urls, names, windowsSHA256URL
}

// DownloadURLCandidates returns the primary URL followed by the ghproxy.net
// mirror candidate when the URL points to GitHub.
func DownloadURLCandidates(rawURL string) []string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil
	}
	if strings.HasPrefix(u, ghproxyPrefix) {
		orig := strings.TrimPrefix(u, ghproxyPrefix)
		if orig != "" {
			return []string{orig, u}
		}
		return []string{u}
	}
	if strings.HasPrefix(u, "https://github.com/") || strings.HasPrefix(u, "https://api.github.com/") || strings.HasPrefix(u, "https://objects.githubusercontent.com/") {
		return []string{u, ghproxyPrefix + u}
	}
	return []string{u}
}

// FetchExpectedSHA256 downloads a SHA256SUMS file (with mirror fallback) and
// extracts the hex digest for targetFileName.
func FetchExpectedSHA256(ctx context.Context, sha256URL, targetFileName string) (string, error) {
	candidates := DownloadURLCandidates(sha256URL)
	if len(candidates) == 0 {
		return "", errors.New("empty sha256 url")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for _, u := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("fetch sha256 %s: HTTP %d", u, resp.StatusCode)
			continue
		}
		if sum := parseSHA256Sums(string(data), targetFileName); sum != "" {
			return sum, nil
		}
		lastErr = fmt.Errorf("sha256 for %s not found in %s", targetFileName, u)
	}
	return "", lastErr
}

func parseSHA256Sums(content, targetFileName string) string {
	targetFileName = strings.ToLower(strings.TrimSpace(filepath.Base(targetFileName)))
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(strings.TrimSpace(fields[0]))
		name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fields[len(fields)-1]), "*"))
		name = filepath.Base(name)
		if len(hash) == 64 && (targetFileName == "" || name == targetFileName) {
			return hash
		}
	}
	return ""
}

// DownloadAndVerifyAsset downloads an installer package from assetURL (falling
// back to ghproxy.net on failure), reports byte progress via onProgress,
// verifies its integrity and optional SHA-256 digest, and atomically writes it
// to destPath. It returns the computed SHA-256 hex digest.
func DownloadAndVerifyAsset(ctx context.Context, assetURL, expectedSHA256, destPath string, onProgress func(downloaded, total int64)) (string, error) {
	candidates := DownloadURLCandidates(assetURL)
	if len(candidates) == 0 {
		return "", errors.New("安装包下载地址为空")
	}
	if strings.TrimSpace(destPath) == "" {
		return "", errors.New("目标保存路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", fmt.Errorf("创建下载目录失败: %w", err)
	}

	var lastErr error
	for _, candidate := range candidates {
		digest, err := downloadSingleCandidate(ctx, candidate, expectedSHA256, destPath, onProgress)
		if err == nil {
			return digest, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func downloadSingleCandidate(ctx context.Context, urlStr, expectedSHA256, destPath string, onProgress func(downloaded, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "snet-client/"+buildinfo.Version)
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败 (%s): HTTP %d", urlStr, resp.StatusCode)
	}

	partPath := fmt.Sprintf("%s.part.%d", destPath, time.Now().UnixNano())
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(partPath)
	}

	total := resp.ContentLength
	if onProgress != nil {
		onProgress(0, total)
	}

	hasher := sha256.New()
	buf := make([]byte, 64*1024)
	var downloaded int64
	var head [512]byte
	var headLen int

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if headLen < len(head) {
				copied := copy(head[headLen:], chunk)
				headLen += copied
			}
			if _, wErr := f.Write(chunk); wErr != nil {
				cleanup()
				return "", fmt.Errorf("写入文件失败: %w", wErr)
			}
			_, _ = hasher.Write(chunk)
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			cleanup()
			return "", fmt.Errorf("读取下载流失败: %w", readErr)
		}
	}

	if err := f.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("同步文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("关闭文件失败: %w", err)
	}

	if downloaded == 0 {
		_ = os.Remove(partPath)
		return "", errors.New("下载的文件为空")
	}
	if total > 0 && downloaded != total {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("下载文件不完整 (%d / %d 字节)", downloaded, total)
	}
	if err := verifyInstallerHeader(destPath, head[:headLen]); err != nil {
		_ = os.Remove(partPath)
		return "", err
	}

	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if want != "" && !strings.EqualFold(actualSHA256, want) {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("安装包 SHA-256 校验失败: 期望 %s，实际 %s", want, actualSHA256)
	}

	_ = os.Remove(destPath)
	if err := os.Rename(partPath, destPath); err != nil {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("保存安装包失败: %w", err)
	}
	return actualSHA256, nil
}

func verifyInstallerHeader(destPath string, head []byte) error {
	trimmed := bytes.TrimSpace(head)
	lowerHead := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lowerHead, []byte("<!doctype html")) || bytes.HasPrefix(lowerHead, []byte("<html")) {
		return errors.New("下载内容异常（收到 HTML 错误页而非安装包）")
	}
	ext := strings.ToLower(filepath.Ext(destPath))
	switch ext {
	case ".exe":
		if len(head) < 2 || head[0] != 'M' || head[1] != 'Z' {
			return errors.New("Windows 安装包格式校验失败（无效的 PE 可执行文件）")
		}
	case ".apk", ".zip":
		if len(head) < 4 || head[0] != 'P' || head[1] != 'K' || head[2] != 0x03 || head[3] != 0x04 {
			return errors.New("安装包格式校验失败（无效的 ZIP/APK 归档）")
		}
	}
	return nil
}

// UpgradeDownloadState tracks an asynchronous download & verify task started
// via the daemon's /ctl/upgrade/download endpoint.
type UpgradeDownloadState struct {
	State      string `json:"state"` // idle | downloading | verifying | ready | error
	Version    string `json:"version,omitempty"`
	AssetURL   string `json:"assetUrl,omitempty"`
	FilePath   string `json:"filePath,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Percent    int    `json:"percent"`
	Error      string `json:"error,omitempty"`
}

type upgradeDownloadManager struct {
	mu    sync.Mutex
	state UpgradeDownloadState
}

var defaultUpgradeDownloader = &upgradeDownloadManager{
	state: UpgradeDownloadState{State: "idle"},
}

func (m *upgradeDownloadManager) status() UpgradeDownloadState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *upgradeDownloadManager) start(version, assetURL, sha256URL, expectedSHA256 string) (UpgradeDownloadState, error) {
	version = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(version), "v"), "V")
	assetURL = strings.TrimSpace(assetURL)
	if assetURL == "" {
		return UpgradeDownloadState{}, errors.New("assetUrl is required")
	}
	baseName := filepath.Base(assetURL)
	if idx := strings.IndexAny(baseName, "?#"); idx >= 0 {
		baseName = baseName[:idx]
	}
	if baseName == "" || baseName == "." || baseName == "/" {
		baseName = fmt.Sprintf("snet-upgrade-%s.pkg", version)
	}
	destPath := filepath.Join(os.TempDir(), "snet-upgrade", baseName)

	m.mu.Lock()
	if m.state.State == "downloading" || m.state.State == "verifying" {
		st := m.state
		m.mu.Unlock()
		return st, nil
	}
	m.state = UpgradeDownloadState{
		State:    "downloading",
		Version:  version,
		AssetURL: assetURL,
		FilePath: destPath,
	}
	st := m.state
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		wantSHA := strings.TrimSpace(expectedSHA256)
		if wantSHA == "" && strings.TrimSpace(sha256URL) != "" {
			if sum, err := FetchExpectedSHA256(ctx, sha256URL, baseName); err == nil {
				wantSHA = sum
			}
		}

		digest, err := DownloadAndVerifyAsset(ctx, assetURL, wantSHA, destPath, func(downloaded, total int64) {
			m.mu.Lock()
			m.state.Downloaded = downloaded
			m.state.Total = total
			if total > 0 {
				pct := int(downloaded * 100 / total)
				if pct > 100 {
					pct = 100
				}
				m.state.Percent = pct
			}
			m.mu.Unlock()
		})

		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			m.state.State = "error"
			m.state.Error = err.Error()
			return
		}
		m.state.State = "ready"
		m.state.SHA256 = digest
		m.state.Percent = 100
	}()

	return st, nil
}

