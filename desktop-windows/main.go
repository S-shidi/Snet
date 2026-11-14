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
	"path/filepath"
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
	log.Printf("SnetWindows: starting")
	svc := &SnetService{}
	app := application.New(application.Options{
		Name:        "SnetWindows",
		Description: "SNET Windows client",
		Assets: application.AssetOptions{
			Handler: newCtlProxy(svc, application.AssetFileServerFS(assets)),
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})
	app.RegisterService(application.NewService(svc))
	app.OnShutdown(func() {
		log.Printf("SnetWindows: shutdown requested")
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
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		log.Printf("SnetWindows: window closing, hiding instead")
		e.Cancel()
		window.Hide()
	})
	statusItem, ipItem, subnetItem := setupSystray(app, window, svc)
	var trayWatchOnce sync.Once
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		log.Printf("SnetWindows: application started")
		showWindow(window)
		trayWatchOnce.Do(func() {
			go watchTrayStatus(statusItem, ipItem, subnetItem)
		})
		// Start the daemon off the UI thread. The window is already visible
		// and the frontend polls /desktop/status while UAC/startup completes.
		go func() {
			log.Printf("SnetWindows: starting snetd")
			if err := svc.StartDaemon(); err != nil {
				log.Printf("snetd: %v", err)
			}
		}()
	})

	log.Printf("SnetWindows: entering app.Run")
	if err := app.Run(); err != nil {
		log.Fatalf("SnetWindows: app.Run failed: %v", err)
	}
	log.Printf("SnetWindows: app.Run returned without error")
}

func initLogging() {
	dir := configDir()
	_ = os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "SnetWindows-startup.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func showWindow(window *application.WebviewWindow) {
	window.Show()
	window.Focus()
}

func setupSystray(app *application.App, window *application.WebviewWindow, svc *SnetService) (*application.MenuItem, *application.MenuItem, *application.MenuItem) {
	tray := app.SystemTray.New()
	if len(trayIcon) == 0 {
		log.Printf("SnetWindows: tray icon is empty; continuing without icon")
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("SNET 虚拟组网")

	menu := app.NewMenu()
	statusItem := menu.Add("未连接").SetEnabled(true).SetBitmap(dotIcon(color.RGBA{R: 239, G: 68, B: 68, A: 255}))
	ipItem := menu.Add("IP: -").SetEnabled(true).SetBitmap(ipIcon())
	subnetItem := menu.Add("子网: -").SetEnabled(true).SetBitmap(subnetIcon())
	subnetItem.SetHidden(true)
	menu.AddSeparator()
	openItem := menu.Add("打开控制面板")
	openItem.SetBitmap(windowIcon())
	openItem.OnClick(func(*application.Context) {
		showWindow(window)
	})
	quitItem := menu.Add("退出 SNET")
	quitItem.SetBitmap(quitIcon())
	quitItem.OnClick(func(*application.Context) {
		log.Printf("SnetWindows: quit requested from tray")
		svc.ShutdownDaemon()
		app.Quit()
	})
	tray.SetMenu(menu)
	return statusItem, ipItem, subnetItem
}

func makeIcon(draw func(*image.RGBA)) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw(img)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func setPixel(img *image.RGBA, x, y int, c color.RGBA) {
	if x >= 0 && x < 16 && y >= 0 && y < 16 {
		img.SetRGBA(x, y, c)
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx, dy := x1-x0, y1-y0
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		setPixel(img, x0, y0, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func dotIcon(c color.RGBA) []byte {
	return makeIcon(func(img *image.RGBA) {
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				dx, dy := float64(x)-7.5, float64(y)-7.5
				if dx*dx+dy*dy <= 5.5*5.5 {
					setPixel(img, x, y, c)
				}
			}
		}
	})
}

func ipIcon() []byte {
	ink := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	return makeIcon(func(img *image.RGBA) {
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				dx, dy := float64(x)-7.5, float64(y)-7.5
				if dx*dx+dy*dy >= 4.0*4.0 && dx*dx+dy*dy <= 5.8*5.8 {
					setPixel(img, x, y, ink)
				}
			}
		}
		drawLine(img, 3, 8, 12, 8, ink)
		drawLine(img, 8, 3, 8, 12, ink)
	})
}

func subnetIcon() []byte {
	ink := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	return makeIcon(func(img *image.RGBA) {
		for _, p := range [][2]int{{3, 8}, {12, 3}, {12, 13}} {
			for y := p[1] - 1; y <= p[1]+1; y++ {
				for x := p[0] - 1; x <= p[0]+1; x++ {
					setPixel(img, x, y, ink)
				}
			}
		}
		drawLine(img, 4, 8, 11, 4, ink)
		drawLine(img, 4, 8, 11, 12, ink)
	})
}

func windowIcon() []byte {
	ink := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	return makeIcon(func(img *image.RGBA) {
		drawLine(img, 3, 4, 12, 4, ink)
		drawLine(img, 3, 12, 12, 12, ink)
		drawLine(img, 3, 4, 3, 12, ink)
		drawLine(img, 12, 4, 12, 12, ink)
		drawLine(img, 3, 7, 12, 7, ink)
	})
}

func quitIcon() []byte {
	ink := color.RGBA{R: 239, G: 68, B: 68, A: 255}
	return makeIcon(func(img *image.RGBA) {
		drawLine(img, 4, 4, 11, 11, ink)
		drawLine(img, 11, 4, 4, 11, ink)
	})
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
		Routes []struct {
			CIDR   string `json:"cidr"`
			Status string `json:"status"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return ""
	}
	seen := make(map[string]struct{})
	for _, r := range peers.Routes {
		if r.Status == "active" && r.CIDR != "" {
			seen[r.CIDR] = struct{}{}
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
