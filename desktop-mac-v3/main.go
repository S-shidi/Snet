package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/tray.png
var trayIcon []byte

func main() {
	initLogging()
	log.Printf("SnetMac: starting")
	svc := &SnetService{}
	app := application.New(application.Options{
		Name:        "SnetMac",
		Description: "SNET macOS client",
		Assets: application.AssetOptions{
			Handler: newCtlProxy(svc, application.AssetFileServerFS(assets)),
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	app.RegisterService(application.NewService(svc))
	app.OnShutdown(func() {
		log.Printf("SnetMac: shutdown requested")
		svc.ShutdownDaemon()
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Snet",
		Width:            1100,
		Height:           800,
		MinWidth:         800,
		MinHeight:        600,
		Hidden:           false,
		URL:              "/",
		BackgroundColour: application.NewRGB(13, 16, 23),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: application.NSAppearanceNameDarkAqua,
		},
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		log.Printf("SnetMac: window closing, hiding instead")
		e.Cancel()
		window.Hide()
	})
	statusItem, ipItem, subnetItem := setupSystray(app, window, svc)
	var trayWatchOnce sync.Once
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		log.Printf("SnetMac: application started")
		showWindow(window)
		trayWatchOnce.Do(func() {
			go watchTrayStatus(statusItem, ipItem, subnetItem)
		})
	})

	log.Printf("SnetMac: starting snetd")
	if err := svc.StartDaemon(); err != nil {
		log.Printf("snetd: %v", err)
	}

	log.Printf("SnetMac: entering app.Run")
	if err := app.Run(); err != nil {
		log.Fatalf("SnetMac: app.Run failed: %v", err)
	}
	log.Printf("SnetMac: app.Run returned without error")
}

func initLogging() {
	f, err := os.OpenFile("/tmp/SnetMac-startup.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func showWindow(window *application.WebviewWindow) {
	window.Show()
	window.Focus()
	_ = exec.Command("osascript", "-e", `tell application "System Events" to set frontmost of process "SnetMac" to true`).Run()
}

func setupSystray(app *application.App, window *application.WebviewWindow, svc *SnetService) (*application.MenuItem, *application.MenuItem, *application.MenuItem) {
	tray := app.SystemTray.New()
	if len(trayIcon) == 0 {
		log.Printf("SnetMac: tray icon is empty; continuing without template icon")
	} else {
		tray.SetTemplateIcon(trayIcon)
	}
	tray.SetTooltip("SNET 虚拟组网")

	menu := app.NewMenu()
	statusItem := menu.Add("未连接").SetEnabled(false).SetBitmap(dotIcon(color.RGBA{R: 239, G: 68, B: 68, A: 255}))
	ipItem := menu.Add("IP: -").SetEnabled(false).SetBitmap(dotIcon(color.RGBA{R: 79, G: 140, B: 255, A: 255}))
	subnetItem := menu.Add("子网: -").SetEnabled(false).SetBitmap(dotIcon(color.RGBA{R: 154, G: 163, B: 178, A: 255}))
	subnetItem.SetHidden(true)
	menu.AddSeparator()
	openItem := menu.Add("打开控制面板")
	openItem.SetBitmap(dotIcon(color.RGBA{R: 52, G: 199, B: 123, A: 255}))
	openItem.OnClick(func(*application.Context) {
		showWindow(window)
	})
	quitItem := menu.Add("退出 SNET")
	quitItem.SetBitmap(dotIcon(color.RGBA{R: 239, G: 68, B: 68, A: 255}))
	quitItem.OnClick(func(*application.Context) {
		log.Printf("SnetMac: quit requested from tray")
		svc.ShutdownDaemon()
		app.Quit()
	})
	tray.SetMenu(menu)
	return statusItem, ipItem, subnetItem
}

func dotIcon(c color.RGBA) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy, r := 7.5, 7.5, 5.5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

type trayStatusResponse struct {
	Networks []struct {
		NetworkID   string `json:"networkId"`
		Name        string `json:"name"`
		IP          string `json:"ip"`
		Active      bool   `json:"active"`
		ServerState string `json:"serverState"`
		RelayPath   *struct {
			Ready bool `json:"ready"`
		} `json:"relayPath"`
	} `json:"networks"`
}

func queryTrayStatus() (string, string, string, color.RGBA) {
	red := color.RGBA{R: 239, G: 68, B: 68, A: 255}
	yellow := color.RGBA{R: 217, G: 161, B: 58, A: 255}
	green := color.RGBA{R: 52, G: 199, B: 123, A: 255}
	token, err := readCtlToken()
	if err != nil {
		return "未连接", "-", "", red
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+ctlAddr+"/ctl/status", nil)
	if err != nil {
		return "未连接", "-", "", red
	}
	req.Header.Set("X-Ctl-Token", token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "未连接", "-", "", red
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "未连接", "-", "", red
	}
	var st trayStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "未连接", "-", "", red
	}
	for _, n := range st.Networks {
		if !n.Active || n.ServerState == "gone" {
			continue
		}
		name := n.Name
		if name == "" {
			name = n.NetworkID
		}
		ip := n.IP
		if ip == "" {
			ip = "-"
		}
		subnets := queryOnlineSubnets(token, n.NetworkID)
		if n.RelayPath != nil && n.RelayPath.Ready {
			return "已连接 (" + name + ")", ip, subnets, green
		}
		return "打洞中", ip, subnets, yellow
	}
	return "未连接", "-", "", red
}

func queryOnlineSubnets(token, nid string) string {
	req, err := http.NewRequest(http.MethodGet, "http://"+ctlAddr+"/ctl/peers?nid="+url.QueryEscape(nid), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-Ctl-Token", token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var peers struct {
		Peers []struct {
			Online         bool     `json:"online"`
			AllowedSubnets []string `json:"allowedSubnets"`
		} `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return ""
	}
	seen := make(map[string]struct{})
	for _, p := range peers.Peers {
		if !p.Online {
			continue
		}
		for _, sub := range p.AllowedSubnets {
			if sub != "" {
				seen[sub] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for sub := range seen {
		out = append(out, sub)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func watchTrayStatus(statusItem, ipItem, subnetItem *application.MenuItem) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		label, ip, subnets, statusColor := queryTrayStatus()
		application.InvokeSync(func() {
			statusItem.SetLabel(label)
			statusItem.SetBitmap(dotIcon(statusColor))
			ipItem.SetLabel("IP: " + ip)
			if subnets == "" {
				subnetItem.SetHidden(true)
			} else {
				subnetItem.SetLabel("子网: " + subnets)
				subnetItem.SetHidden(false)
			}
		})
		<-ticker.C
	}
}
