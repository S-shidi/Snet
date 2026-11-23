// SPDX-License-Identifier: BSD-3-Clause
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	macTargetAppPath = "/Applications/Snet.app"
	ghproxyMirror    = "https://ghproxy.net/"
)

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

var macUpgrader = &desktopUpgrader{
	status: DesktopUpgradeStatus{State: "idle"},
}

// UpgradeStatus returns the current desktop upgrade progress snapshot.
func (s *SnetService) UpgradeStatus() DesktopUpgradeStatus {
	return macUpgrader.get()
}

// StartUpgrade downloads the latest macOS DMG, mounts it, replaces
// /Applications/Snet.app with rollback on failure, and relaunches Snet.
func (s *SnetService) StartUpgrade(version, assetURL string) (DesktopUpgradeStatus, error) {
	return macUpgrader.start(s, version, assetURL)
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
		dlURL = fmt.Sprintf("https://github.com/S-shidi/Snet/releases/download/v%s/Snet-v%s-mac.dmg", ver, ver)
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

	go u.runMacUpgrade(svc, ver, dlURL)
	return st, nil
}

func (u *desktopUpgrader) runMacUpgrade(svc *SnetService, version, assetURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	dmgPath := filepath.Join(os.TempDir(), fmt.Sprintf("Snet-v%s-mac.dmg", version))
	_ = os.Remove(dmgPath)

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
				st.Message = "正在下载 DMG 安装包…"
			})
		}
	}()

	err := downloadWithMirrorFallback(ctx, assetURL, dmgPath, func(dl, total int64) {
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
	defer os.Remove(dmgPath)

	u.update(func(st *DesktopUpgradeStatus) {
		st.State = "verifying"
		st.Percent = 85
		st.Message = "正在挂载并校验安装包…"
	})

	mountDir, err := os.MkdirTemp("", "snet-dmg-mount-*")
	if err != nil {
		u.fail(fmt.Errorf("创建挂载目录失败: %w", err))
		return
	}
	defer os.RemoveAll(mountDir)

	attachOut, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noverify", "-mountpoint", mountDir, dmgPath).CombinedOutput()
	if err != nil {
		u.fail(fmt.Errorf("挂载 DMG 失败: %v (%s)", err, strings.TrimSpace(string(attachOut))))
		return
	}
	detachDMG := func() {
		_ = exec.Command("hdiutil", "detach", mountDir, "-force").Run()
	}
	defer detachDMG()

	srcApp := filepath.Join(mountDir, "Snet.app")
	srcExe := filepath.Join(srcApp, "Contents", "MacOS", "Snet")
	if info, statErr := os.Stat(srcExe); statErr != nil || info.IsDir() {
		u.fail(errors.New("DMG 安装包内未找到有效的 Snet.app"))
		return
	}

	u.update(func(st *DesktopUpgradeStatus) {
		st.State = "installing"
		st.Percent = 92
		st.Message = "正在覆盖安装 /Applications/Snet.app…"
	})

	if err := replaceMacAppBundle(srcApp, macTargetAppPath); err != nil {
		u.fail(err)
		return
	}

	detachDMG()

	u.update(func(st *DesktopUpgradeStatus) {
		st.State = "restarting"
		st.Percent = 100
		st.Message = "升级完成，即将重启…"
	})

	// Allow the frontend poll to observe the "restarting" state before we exit.
	time.Sleep(900 * time.Millisecond)

	relaunchCmd := fmt.Sprintf("sleep 0.6 && open -n %s", shellQuote(macTargetAppPath))
	if err := exec.Command("/bin/sh", "-c", relaunchCmd).Start(); err != nil {
		u.fail(fmt.Errorf("重启新版本失败，请手动从应用程序启动 Snet: %w", err))
		return
	}
	log.Printf("[Snet upgrade] upgraded to v%s, exiting for relaunch", version)
	os.Exit(0)
}

// replaceMacAppBundle replaces targetApp with srcApp atomically using a backup
// copy so any failure restores the previous working bundle.
func replaceMacAppBundle(srcApp, targetApp string) error {
	backupApp := filepath.Join(os.TempDir(), fmt.Sprintf("Snet.app.backup.%d", time.Now().UnixNano()))
	_ = os.RemoveAll(backupApp)
	defer os.RemoveAll(backupApp)

	_, targetExistsErr := os.Stat(targetApp)
	targetExists := targetExistsErr == nil

	if targetExists {
		if out, err := exec.Command("ditto", targetApp, backupApp).CombinedOutput(); err != nil {
			return fmt.Errorf("备份旧版本失败: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}

	stagingApp := targetApp + ".new"
	_ = os.RemoveAll(stagingApp)
	if _, err := exec.Command("ditto", srcApp, stagingApp).CombinedOutput(); err == nil {
		_ = exec.Command("xattr", "-cr", stagingApp).Run()
		if rmErr := os.RemoveAll(targetApp); rmErr == nil {
			if renErr := os.Rename(stagingApp, targetApp); renErr == nil {
				_ = exec.Command("xattr", "-cr", targetApp).Run()
				return nil
			}
		}
		_ = os.RemoveAll(stagingApp)
		if targetExists {
			_ = os.RemoveAll(targetApp)
			_ = exec.Command("ditto", backupApp, targetApp).Run()
		}
	}

	// Fallback to administrator privileges via osascript when /Applications
	// requires elevation. Also runs rollback from backupApp if ditto fails.
	home, _ := os.UserHomeDir()
	installScript := filepath.Join(targetApp, "Contents", "Resources", "install-daemon.sh")
	shellCmd := fmt.Sprintf(
		`set -e; rm -rf %s; if ! ditto %s %s; then if [ -d %s ]; then rm -rf %s && ditto %s %s; fi; exit 1; fi; xattr -cr %s; if [ -x %s ]; then sh %s %s %s || true; fi`,
		shellQuote(targetApp),
		shellQuote(srcApp), shellQuote(targetApp),
		shellQuote(backupApp), shellQuote(targetApp), shellQuote(backupApp), shellQuote(targetApp),
		shellQuote(targetApp),
		shellQuote(installScript), shellQuote(installScript), shellQuote(targetApp), shellQuote(home),
	)
	osa := fmt.Sprintf("do shell script %s with administrator privileges", appleScriptQuote(shellCmd))
	if out, err := exec.Command("osascript", "-e", osa).CombinedOutput(); err != nil {
		if targetExists {
			if _, stErr := os.Stat(targetApp); stErr != nil {
				_ = exec.Command("ditto", backupApp, targetApp).Run()
			}
		}
		return fmt.Errorf("覆盖 /Applications/Snet.app 失败: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func downloadWithMirrorFallback(ctx context.Context, rawURL, destPath string, onProgress func(downloaded, total int64)) error {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return errors.New("下载地址为空")
	}
	candidates := []string{u}
	if strings.HasPrefix(u, "https://github.com/") || strings.HasPrefix(u, "https://objects.githubusercontent.com/") {
		candidates = append(candidates, ghproxyMirror+u)
	} else if strings.HasPrefix(u, ghproxyMirror) {
		orig := strings.TrimPrefix(u, ghproxyMirror)
		if orig != "" {
			candidates = []string{orig, u}
		}
	}
	var lastErr error
	for _, cand := range candidates {
		if err := downloadFile(ctx, cand, destPath, onProgress); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func downloadFile(ctx context.Context, urlStr, destPath string, onProgress func(downloaded, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Snet-Desktop-Updater")
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d (%s)", resp.StatusCode, urlStr)
	}

	partPath := destPath + ".part"
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(partPath)
	}

	total := resp.ContentLength
	if onProgress != nil {
		onProgress(0, total)
	}

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
				return wErr
			}
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
			return rErr
		}
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if downloaded < 1024 {
		_ = os.Remove(partPath)
		return errors.New("下载的安装包大小异常")
	}
	if total > 0 && downloaded != total {
		_ = os.Remove(partPath)
		return fmt.Errorf("下载未完成 (%d/%d)", downloaded, total)
	}
	lowerHead := bytes.ToLower(bytes.TrimSpace(head[:headLen]))
	if bytes.HasPrefix(lowerHead, []byte("<!doctype html")) || bytes.HasPrefix(lowerHead, []byte("<html")) {
		_ = os.Remove(partPath)
		return errors.New("下载内容异常（收到 HTML 页面而非 DMG 安装包）")
	}
	_ = os.Remove(destPath)
	return os.Rename(partPath, destPath)
}
