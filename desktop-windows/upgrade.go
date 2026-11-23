// SPDX-License-Identifier: BSD-3-Clause
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const ghproxyMirror = "https://ghproxy.net/"

// DesktopUpgradeStatus represents the current state of a desktop self-upgrade.
type DesktopUpgradeStatus struct {
	State      string `json:"state"` // idle | downloading | verifying | installing | restarting | error
	Version    string `json:"version,omitempty"`
	Message    string `json:"message,omitempty"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Percent    int    `json:"percent"`
	Error      string `json:"error,omitempty"`
}

type desktopUpgrader struct {
	mu     sync.Mutex
	status DesktopUpgradeStatus
}

var winUpgrader = &desktopUpgrader{
	status: DesktopUpgradeStatus{State: "idle"},
}

// UpgradeStatus returns the current desktop upgrade progress snapshot.
func (s *SnetService) UpgradeStatus() DesktopUpgradeStatus {
	return winUpgrader.get()
}

// StartUpgrade downloads the latest Windows NSIS installer, verifies it,
// launches a silent upgrade (/S), and restarts Snet.
func (s *SnetService) StartUpgrade(version, assetURL string) (DesktopUpgradeStatus, error) {
	return winUpgrader.start(s, version, assetURL)
}

func (u *desktopUpgrader) get() DesktopUpgradeStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *desktopUpgrader) update(fn func(*DesktopUpgradeStatus)) {
	u.mu.Lock()
	fn(&u.status)
	u.mu.Unlock()
}

func (u *desktopUpgrader) fail(err error) {
	log.Printf("[Snet upgrade] error: %v", err)
	u.mu.Lock()
	u.status.State = "error"
	u.status.Error = err.Error()
	u.status.Message = "升级失败: " + err.Error()
	u.mu.Unlock()
}

func (u *desktopUpgrader) start(svc *SnetService, version, assetURL string) (DesktopUpgradeStatus, error) {
	ver := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(version), "v"), "V")
	if ver == "" {
		return DesktopUpgradeStatus{}, errors.New("目标版本号不能为空")
	}
	dlURL := strings.TrimSpace(assetURL)
	if dlURL == "" {
		dlURL = fmt.Sprintf("https://github.com/S-shidi/Snet/releases/download/v%s/Snet-Setup-v%s.exe", ver, ver)
	}

	u.mu.Lock()
	switch u.status.State {
	case "downloading", "verifying", "installing", "restarting":
		st := u.status
		u.mu.Unlock()
		return st, nil
	}
	u.status = DesktopUpgradeStatus{
		State:   "downloading",
		Version: ver,
		Message: "正在准备下载更新包…",
		Percent: 0,
	}
	st := u.status
	u.mu.Unlock()

	go u.runWindowsUpgrade(svc, ver, dlURL)
	return st, nil
}

func (u *desktopUpgrader) runWindowsUpgrade(svc *SnetService, version, assetURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	upgradeDir := filepath.Join(os.TempDir(), "snet-upgrade")
	if err := os.MkdirAll(upgradeDir, 0o755); err != nil {
		u.fail(fmt.Errorf("创建临时升级目录失败: %w", err))
		return
	}

	isZip := strings.HasSuffix(strings.ToLower(assetURL), ".zip")
	dlName := fmt.Sprintf("Snet-Setup-v%s.exe", version)
	if isZip {
		dlName = fmt.Sprintf("Snet-v%s-win-Setup.zip", version)
	}
	dlPath := filepath.Join(upgradeDir, dlName)
	_ = os.Remove(dlPath)

	progressCh := make(chan [2]int64, 32)
	doneProgress := make(chan struct{})
	go func() {
		defer close(doneProgress)
		for pair := range progressCh {
			dl, total := pair[0], pair[1]
			u.update(func(st *DesktopUpgradeStatus) {
				st.Downloaded = dl
				st.Total = total
				if total > 0 {
					pct := int(dl * 80 / total)
					if pct > 80 {
						pct = 80
					}
					st.Percent = pct
				}
				st.Message = "正在下载 Windows 安装包…"
			})
		}
	}()

	actualSHA, err := downloadWithMirrorFallback(ctx, assetURL, dlPath, func(dl, total int64) {
		select {
		case progressCh <- [2]int64{dl, total}:
		default:
		}
	})
	close(progressCh)
	<-doneProgress
	if err != nil {
		u.fail(fmt.Errorf("下载更新包失败: %w", err))
		return
	}

	u.update(func(st *DesktopUpgradeStatus) {
		st.State = "verifying"
		st.Percent = 85
		st.Message = "正在校验安装包完整性…"
	})

	// Best-effort SHA-256 verification against published SHA256SUMS.
	shaURL := fmt.Sprintf("https://github.com/S-shidi/Snet/releases/download/v%s/SHA256SUMS-windows-v%s.txt", version, version)
	if wantSHA := fetchExpectedSHA256(ctx, shaURL, dlName); wantSHA != "" && !strings.EqualFold(wantSHA, actualSHA) {
		_ = os.Remove(dlPath)
		u.fail(fmt.Errorf("安装包 SHA-256 校验不匹配 (期望 %s，实际 %s)", wantSHA, actualSHA))
		return
	}

	installerExe := dlPath
	if isZip {
		extractedExe := filepath.Join(upgradeDir, fmt.Sprintf("Snet-Setup-v%s.exe", version))
		if err := extractExeFromZip(dlPath, extractedExe); err != nil {
			_ = os.Remove(dlPath)
			u.fail(fmt.Errorf("解压安装包失败: %w", err))
			return
		}
		_ = os.Remove(dlPath)
		installerExe = extractedExe
	}

	if err := verifyPEHeader(installerExe); err != nil {
		_ = os.Remove(installerExe)
		u.fail(err)
		return
	}

	u.update(func(st *DesktopUpgradeStatus) {
		st.State = "installing"
		st.Percent = 92
		st.Message = "正在启动静默安装程序…"
	})

	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, err := windows.UTF16PtrFromString(installerExe)
	if err != nil {
		u.fail(err)
		return
	}
	argsPtr, _ := windows.UTF16PtrFromString("/S")
	dirPtr, _ := windows.UTF16PtrFromString(upgradeDir)

	if err := windows.ShellExecute(0, verbPtr, exePtr, argsPtr, dirPtr, windows.SW_SHOWNORMAL); err != nil {
		u.fail(fmt.Errorf("启动安装程序失败（可能取消了管理员授权）: %w", err))
		return
	}

	u.update(func(st *DesktopUpgradeStatus) {
		st.State = "restarting"
		st.Percent = 100
		st.Message = "升级完成，即将重启…"
	})

	// Allow the frontend to render "restarting" before we release Snet.exe for
	// the NSIS installer to overwrite and relaunch.
	time.Sleep(800 * time.Millisecond)
	svc.ShutdownDaemon()
	os.Exit(0)
}

func extractExeFromZip(zipPath, destExe string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.OpenFile(destExe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, rc); err != nil {
				_ = out.Close()
				return err
			}
			return out.Close()
		}
	}
	return errors.New("ZIP 压缩包内未找到 .exe 安装程序")
}

func verifyPEHeader(exePath string) error {
	f, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("读取安装包头部失败: %w", err)
	}
	if magic[0] != 'M' || magic[1] != 'Z' {
		return errors.New("下载的安装程序格式无效（非有效的 Windows 可执行文件）")
	}
	return nil
}

func candidateURLs(rawURL string) []string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil
	}
	if strings.HasPrefix(u, "https://github.com/") || strings.HasPrefix(u, "https://objects.githubusercontent.com/") {
		return []string{u, ghproxyMirror + u}
	}
	if strings.HasPrefix(u, ghproxyMirror) {
		orig := strings.TrimPrefix(u, ghproxyMirror)
		if orig != "" {
			return []string{orig, u}
		}
	}
	return []string{u}
}

func fetchExpectedSHA256(ctx context.Context, shaURL, targetName string) string {
	targetName = strings.ToLower(strings.TrimSpace(filepath.Base(targetName)))
	client := &http.Client{Timeout: 10 * time.Second}
	for _, u := range candidateURLs(shaURL) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		data, rErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if rErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			fields := strings.Fields(strings.TrimSpace(sc.Text()))
			if len(fields) < 2 {
				continue
			}
			hash := strings.ToLower(fields[0])
			name := strings.ToLower(filepath.Base(strings.TrimPrefix(fields[len(fields)-1], "*")))
			if len(hash) == 64 && name == targetName {
				return hash
			}
		}
	}
	return ""
}

func downloadWithMirrorFallback(ctx context.Context, rawURL, destPath string, onProgress func(downloaded, total int64)) (string, error) {
	candidates := candidateURLs(rawURL)
	if len(candidates) == 0 {
		return "", errors.New("下载地址为空")
	}
	var lastErr error
	for _, cand := range candidates {
		digest, err := downloadFile(ctx, cand, destPath, onProgress)
		if err == nil {
			return digest, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func downloadFile(ctx context.Context, urlStr, destPath string, onProgress func(downloaded, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Snet-Desktop-Updater")
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d (%s)", resp.StatusCode, urlStr)
	}

	partPath := destPath + ".part"
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
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
	var head [256]byte
	var headLen int

	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if headLen < len(head) {
				headLen += copy(head[headLen:], chunk)
			}
			if _, wErr := f.Write(chunk); wErr != nil {
				cleanup()
				return "", wErr
			}
			_, _ = hasher.Write(chunk)
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			cleanup()
			return "", rErr
		}
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", err
	}
	if downloaded < 1024 {
		_ = os.Remove(partPath)
		return "", errors.New("下载的安装包大小异常")
	}
	if total > 0 && downloaded != total {
		_ = os.Remove(partPath)
		return "", fmt.Errorf("下载未完成 (%d/%d)", downloaded, total)
	}
	lowerHead := bytes.ToLower(bytes.TrimSpace(head[:headLen]))
	if bytes.HasPrefix(lowerHead, []byte("<!doctype html")) || bytes.HasPrefix(lowerHead, []byte("<html")) {
		_ = os.Remove(partPath)
		return "", errors.New("下载内容异常（收到 HTML 页面而非安装包）")
	}
	_ = os.Remove(destPath)
	if err := os.Rename(partPath, destPath); err != nil {
		_ = os.Remove(partPath)
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
