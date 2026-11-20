// SPDX-License-Identifier: BSD-3-Clause
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
	showEvent, first := acquireSingleInstance()
	if !first {
		os.Exit(0)
	}
	initLogging()
	log.Printf("Snet: starting")
	svc := &SnetService{}
	app := application.New(application.Options{
		Name:        "Snet",
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
		log.Printf("Snet: shutdown requested")
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
		log.Printf("Snet: window closing, hiding instead")
		e.Cancel()
		window.Hide()
	})
	statusItem, ipItem, allowRelayItem, routeRoot, menu := setupSystray(app, window, svc)
	var trayWatchOnce sync.Once
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		log.Printf("Snet: application started")
		showWindow(window)
		trayWatchOnce.Do(func() {
			go watchTrayStatus(statusItem, ipItem, allowRelayItem, routeRoot, menu)
		})
		watchShowRequests(showEvent, func() {
			log.Printf("Snet: second launch requested window")
			showWindow(window)
		})
		// Start the daemon off the UI thread. The window is already visible
		// and the frontend polls /desktop/status while UAC/startup completes.
		go func() {
			log.Printf("Snet: starting snetd")
			if err := svc.StartDaemon(); err != nil {
				log.Printf("snetd: %v", err)
			}
		}()
	})

	log.Printf("Snet: entering app.Run")
	if err := app.Run(); err != nil {
		log.Fatalf("Snet: app.Run failed: %v", err)
	}
	log.Printf("Snet: app.Run returned without error")
}

func initLogging() {
	dir := configDir()
	_ = os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "Snet-startup.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func showWindow(window *application.WebviewWindow) {
	window.Show()
	window.Focus()
}

func setupSystray(app *application.App, window *application.WebviewWindow, svc *SnetService) (*application.MenuItem, *application.MenuItem, *application.MenuItem, *application.Menu, *application.Menu) {
	tray := app.SystemTray.New()
	if len(trayIcon) == 0 {
		log.Printf("Snet: tray icon is empty; continuing without icon")
	} else {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("Snet")
	tray.OnClick(func() { tray.OpenMenu() })
	tray.OnRightClick(func() { tray.OpenMenu() })
	tray.OnDoubleClick(func() { showWindow(window) })

	menu := app.NewMenu()
	statusItem := menu.Add("未连接").SetEnabled(true).SetBitmap(dotIcon(color.RGBA{R: 239, G: 68, B: 68, A: 255}))
	ipItem := menu.Add("IP: -").SetEnabled(true).SetBitmap(ipIcon())
	menu.AddSeparator()
	allowRelayItem := menu.Add("允许本设备作为中继网关").SetBitmap(blankIcon())
	// Windows cannot host the delegated relay gateway safely; keep the
	// capability hidden instead of letting users toggle a non-functional option.
	allowRelayItem.SetHidden(true)
	menu.AddSeparator()
	routeRoot := menu.AddSubmenu("子网路由")
	if routeItem := menu.FindByLabel("子网路由"); routeItem != nil {
		routeItem.SetBitmap(subnetIcon())
	}
	routeRoot.Add("正在加载…").SetEnabled(false)
	menu.AddSeparator()
	openItem := menu.Add("打开控制面板")
	openItem.SetBitmap(windowIcon())
	openItem.OnClick(func(*application.Context) { showWindow(window) })
	menu.AddSeparator()
	quitItem := menu.Add("退出 Snet")
	quitItem.SetBitmap(quitIcon())
	quitItem.OnClick(func(*application.Context) {
		log.Printf("Snet: quit requested from tray")
		svc.ShutdownDaemon()
		app.Quit()
	})
	tray.SetMenu(menu)
	return statusItem, ipItem, allowRelayItem, routeRoot, menu
}

func blankIcon() []byte {
	return makeIcon(func(*image.RGBA) {})
}

func relayCheckIcon(on bool) []byte {
	if !on {
		return blankIcon()
	}
	ink := color.RGBA{R: 52, G: 211, B: 153, A: 255}
	return makeIcon(func(img *image.RGBA) {
		drawThickLine(img, 3, 8, 6, 11, ink)
		drawThickLine(img, 6, 11, 13, 4, ink)
	})
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

func drawThickLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for _, d := range [][2]int{{0, 0}, {-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		drawLine(img, x0+d[0], y0+d[1], x1+d[0], y1+d[1], c)
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
	AllowRelay bool `json:"allowRelay"`
	UseRelay   bool `json:"useRelay"`
}

type trayRoute struct {
	CIDR    string
	Enabled bool
}

type trayNetwork struct {
	NID    string
	Name   string
	Routes []trayRoute
}

type trayStatus struct {
	Label      string
	IP         string
	Color      color.RGBA
	AllowRelay bool
	Networks   []trayNetwork
}

func queryTrayStatus() trayStatus {
	red := color.RGBA{R: 239, G: 68, B: 68, A: 255}
	yellow := color.RGBA{R: 217, G: 161, B: 58, A: 255}
	green := color.RGBA{R: 52, G: 199, B: 123, A: 255}
	empty := trayStatus{Label: "未连接", IP: "-", Color: red}
	token, err := readCtlToken()
	if err != nil {
		return empty
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+ctlAddr+"/ctl/status", nil)
	if err != nil {
		return empty
	}
	req.Header.Set("X-Ctl-Token", token)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return empty
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return empty
	}
	var st trayStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return empty
	}
	out := empty
	for _, n := range st.Networks {
		if !n.Active || n.ServerState == "gone" {
			continue
		}
		name := n.Name
		if name == "" {
			name = n.NetworkID
		}
		routes := queryOnlineSubnets(token, n.NetworkID)
		out.Networks = append(out.Networks, trayNetwork{NID: n.NetworkID, Name: name, Routes: routes})
		if len(out.Networks) == 1 {
			out.IP = n.IP
			if out.IP == "" {
				out.IP = "-"
			}
			out.AllowRelay = st.AllowRelay
			out.Color = yellow
			out.Label = "打洞中"
			if n.RelayPath != nil && n.RelayPath.Ready {
				out.Color = green
				out.Label = "已连接 (" + name + ")"
			}
		}
	}
	return out
}

func queryOnlineSubnets(token, nid string) []trayRoute {
	req, err := http.NewRequest(http.MethodGet, "http://"+ctlAddr+"/ctl/peers?nid="+url.QueryEscape(nid), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("X-Ctl-Token", token)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var peers struct {
		Routes []struct {
			CIDR    string `json:"cidr"`
			Status  string `json:"status"`
			Enabled *bool  `json:"enabled"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return nil
	}
	out := make([]trayRoute, 0, len(peers.Routes))
	seen := make(map[string]struct{}, len(peers.Routes))
	for _, r := range peers.Routes {
		if r.Status != "active" || r.CIDR == "" {
			continue
		}
		if _, ok := seen[r.CIDR]; ok {
			continue
		}
		seen[r.CIDR] = struct{}{}
		enabled := true
		if r.Enabled != nil {
			enabled = *r.Enabled
		}
		out = append(out, trayRoute{CIDR: r.CIDR, Enabled: enabled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CIDR < out[j].CIDR })
	return out
}

func updateTrayMenu(menu *application.Menu) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tray menu update: %v", r)
		}
	}()
	menu.Update()
}

func rebuildTrayRoutes(root *application.Menu, networks []trayNetwork) {
	root.Clear()
	total := 0
	for _, network := range networks {
		if len(network.Routes) == 0 {
			continue
		}
		if total > 0 {
			root.AddSeparator()
		}
		total++
		for _, route := range network.Routes {
			route := route
			nid := network.NID
			item := root.AddCheckbox(route.CIDR, route.Enabled)
			item.OnClick(func(ctx *application.Context) {
				checked := ctx.IsChecked()
				go func() {
					if err := setRouteEnabled(nid, route.CIDR, checked); err != nil {
						log.Printf("tray route %s/%s: %v", nid, route.CIDR, err)
					}
				}()
			})
		}
	}
	if total == 0 {
		root.Add("暂无子网路由").SetEnabled(false)
	}
}

func watchTrayStatus(statusItem, ipItem, allowRelayItem *application.MenuItem, routeRoot, menu *application.Menu) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastRoutes := "\x00"
	for {
		st := queryTrayStatus()
		sig := trayRouteSignature(st.Networks)
		routeChanged := false
		application.InvokeSync(func() {
			statusItem.SetLabel(st.Label)
			statusItem.SetBitmap(dotIcon(st.Color))
			ipItem.SetLabel("IP: " + st.IP)
			allowRelayItem.SetBitmap(relayCheckIcon(st.AllowRelay))
			if sig != lastRoutes {
				rebuildTrayRoutes(routeRoot, st.Networks)
				lastRoutes = sig
				routeChanged = true
			}
		})
		if routeChanged {
			updateTrayMenu(menu)
		}
		<-ticker.C
	}
}

func trayRouteSignature(networks []trayNetwork) string {
	var b strings.Builder
	for _, n := range networks {
		b.WriteString(n.NID)
		b.WriteByte(':')
		for _, r := range n.Routes {
			b.WriteString(r.CIDR)
			if r.Enabled {
				b.WriteString("+")
			} else {
				b.WriteString("-")
			}
			b.WriteByte(',')
		}
		b.WriteByte(';')
	}
	return b.String()
}
