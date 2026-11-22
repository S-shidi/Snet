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
	release, first := acquireSingleInstance()
	if !first {
		return
	}
	defer release()
	initLogging()
	log.Printf("Snet: starting")
	svc := &SnetService{}
	app := application.New(application.Options{
		Name:        "Snet",
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
		log.Printf("Snet: shutdown requested")
		svc.ShutdownDaemon()
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Snet",
		Width:            560,
		Height:           800,
		MinWidth:         480,
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
		log.Printf("Snet: window closing, hiding instead")
		e.Cancel()
		window.Hide()
	})
	statusItem, allowRelayItem, networkItems, routeRoot, menu := setupSystray(app, window, svc)
	var trayWatchOnce sync.Once
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		log.Printf("Snet: application started")
		showWindow(window)
		// Install the native title-bar drag handle once AppKit has created the
		// NSWindow. Dispatch from a goroutine so InvokeSync can never wait on
		// the main thread that is currently delivering this callback.
		go func() {
			for attempt := 0; attempt < 50; attempt++ {
				installed := false
				application.InvokeSync(func() {
					if native := window.NativeWindow(); native != nil {
						installMacDragRegion(native)
						installed = true
					}
				})
				if installed {
					log.Printf("Snet: native drag region installed")
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			log.Printf("Snet: native drag region not installed: no NSWindow")
		}()
		trayWatchOnce.Do(func() {
			go watchTrayStatus(statusItem, allowRelayItem, networkItems, routeRoot, menu)
		})
	})

	log.Printf("Snet: starting snetd")
	if err := svc.StartDaemon(); err != nil {
		log.Printf("snetd: %v", err)
	}

	log.Printf("Snet: entering app.Run")
	if err := app.Run(); err != nil {
		log.Fatalf("Snet: app.Run failed: %v", err)
	}
	log.Printf("Snet: app.Run returned without error")
}

func initLogging() {
	f, err := os.OpenFile("/tmp/Snet-startup.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func showWindow(window *application.WebviewWindow) {
	window.Show()
	window.Focus()
	_ = exec.Command("osascript", "-e", `tell application "System Events" to set frontmost of process "Snet" to true`).Run()
}

var (
	trayRelayAllow bool
)

func setupSystray(app *application.App, window *application.WebviewWindow, svc *SnetService) (*application.MenuItem, *application.MenuItem, []*application.MenuItem, *application.Menu, *application.Menu) {
	tray := app.SystemTray.New()
	if len(trayIcon) == 0 {
		log.Printf("Snet: tray icon is empty; continuing without template icon")
	} else {
		tray.SetTemplateIcon(trayIcon)
	}
	tray.SetTooltip("Snet")
	tray.OnClick(func() { tray.OpenMenu() })
	tray.OnRightClick(func() { tray.OpenMenu() })
	tray.OnDoubleClick(func() { showWindow(window) })

	menu := app.NewMenu()
	statusItem := menu.Add("未连接").SetEnabled(true).SetBitmap(dotIcon(color.RGBA{R: 239, G: 68, B: 68, A: 255}))
	// Fixed pool of top-level network rows: one line per network with a status
	// dot, name and this device's IP. The placeholder status row is hidden once
	// at least one network exists.
	const maxTrayNetworks = 16
	networkItems := make([]*application.MenuItem, 0, maxTrayNetworks)
	for i := 0; i < maxTrayNetworks; i++ {
		networkItems = append(networkItems, menu.Add("").SetHidden(true).SetEnabled(true))
	}
	menu.AddSeparator()
	allowRelayItem := menu.Add("允许本设备作为中继网关").SetBitmap(relayCheckIcon(trayRelayAllow))
	routeRoot := menu.AddSubmenu("子网路由")
	if routeItem := menu.FindByLabel("子网路由"); routeItem != nil {
		routeItem.SetBitmap(subnetIcon())
	}
	routeRoot.Add("正在加载…").SetEnabled(false).SetBitmap(blankIcon())
	openItem := menu.Add("打开控制面板")
	openItem.SetBitmap(windowIcon())
	openItem.OnClick(func(*application.Context) { showWindow(window) })
	allowRelayItem.OnClick(func(*application.Context) {
		next := !trayRelayAllow
		if err := setRelayOptions(next, true); err != nil {
			log.Printf("tray relay options: %v", err)
			return
		}
		trayRelayAllow = next
		allowRelayItem.SetBitmap(relayCheckIcon(trayRelayAllow))
	})
	quitItem := menu.Add("退出 Snet")
	quitItem.SetBitmap(quitIcon())
	quitItem.OnClick(func(*application.Context) {
		log.Printf("Snet: quit requested from tray")
		svc.ShutdownDaemon()
		app.Quit()
	})
	tray.SetMenu(menu)
	return statusItem, allowRelayItem, networkItems, routeRoot, menu
}

func blankIcon() []byte {
	return makeIcon(func(*image.RGBA) {})
}

func relayCheckIcon(on bool) []byte {
	if !on {
		return dotIcon(color.RGBA{R: 105, G: 118, B: 140, A: 255})
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

func routeStateIcon(route trayRoute) []byte {
	if route.Active && route.Enabled {
		return routeIcon()
	}
	return routeCrossIcon()
}

func routeCrossIcon() []byte {
	ink := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	return makeIcon(func(img *image.RGBA) {
		drawLine(img, 4, 4, 11, 11, ink)
		drawLine(img, 11, 4, 4, 11, ink)
	})
}

func routeIcon() []byte {
	ink := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	return makeIcon(func(img *image.RGBA) {
		drawLine(img, 3, 8, 12, 8, ink)
		drawLine(img, 9, 5, 12, 8, ink)
		drawLine(img, 9, 11, 12, 8, ink)
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
		Interface   string `json:"interface"`
		ServerState string `json:"serverState"`
		State       string `json:"state"`
		Owner       bool   `json:"owner"`
		JoinedAt    string `json:"joinedAt"`
		PeerStats   map[string]struct {
			RxBytes int64 `json:"RxBytes"`
			TxBytes int64 `json:"TxBytes"`
		} `json:"peerStats"`
		RelayPath *struct {
			Ready       bool `json:"ready"`
			DirectReady int  `json:"directReady"`
			RelayReady  int  `json:"relayReady"`
		} `json:"relayPath"`
	} `json:"networks"`
	AllowRelay bool `json:"allowRelay"`
	UseRelay   bool `json:"useRelay"`
}

type trayRoute struct {
	CIDR    string
	Enabled bool
	Active  bool
}

type trayNetwork struct {
	NID      string
	Name     string
	IP       string
	Status   string
	Online   bool
	Owner    bool
	JoinedAt string
	Color    color.RGBA
	Routes   []trayRoute
}

type trayStatus struct {
	Label      string
	Color      color.RGBA
	AllowRelay bool
	Networks   []trayNetwork
}

func queryTrayStatus() trayStatus {
	red := color.RGBA{R: 239, G: 68, B: 68, A: 255}
	yellow := color.RGBA{R: 217, G: 161, B: 58, A: 255}
	green := color.RGBA{R: 52, G: 199, B: 123, A: 255}
	gray := color.RGBA{R: 105, G: 118, B: 140, A: 255}
	empty := trayStatus{Label: "未连接", Color: red}
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
	out.AllowRelay = st.AllowRelay
	for _, n := range st.Networks {
		if n.ServerState == "gone" {
			continue
		}
		name := n.Name
		if name == "" {
			name = n.NetworkID
		}
		hasTraffic := false
		for _, ps := range n.PeerStats {
			if ps.RxBytes > 0 || ps.TxBytes > 0 {
				hasTraffic = true
				break
			}
		}
		online := n.Active && n.State != "error" && n.Interface != "" && n.RelayPath != nil &&
			(n.RelayPath.Ready || n.RelayPath.DirectReady > 0 || n.RelayPath.RelayReady > 0 || hasTraffic)
		status := "已断开"
		statusColor := gray
		switch {
		case !n.Active:
		case n.State == "error":
			status = "异常"
			statusColor = red
		case online:
			status = "已连接"
			statusColor = green
		case n.Interface != "":
			status = "等待路径"
			statusColor = yellow
		case n.State == "active":
			status = "启动中"
			statusColor = yellow
		}
		ip := n.IP
		if ip == "" {
			ip = "-"
		}
		routes := queryTrayRoutes(token, n.NetworkID)
		out.Networks = append(out.Networks, trayNetwork{
			NID: n.NetworkID, Name: name, IP: ip, Status: status, Online: online,
			Owner: n.Owner, JoinedAt: n.JoinedAt, Color: statusColor, Routes: routes,
		})
	}
	sort.SliceStable(out.Networks, func(i, j int) bool {
		a, b := out.Networks[i], out.Networks[j]
		if a.Owner != b.Owner {
			return a.Owner
		}
		if a.JoinedAt != b.JoinedAt {
			if a.JoinedAt == "" {
				return false
			}
			if b.JoinedAt == "" {
				return true
			}
			return a.JoinedAt < b.JoinedAt
		}
		return a.Name < b.Name
	})
	if len(out.Networks) == 0 {
		out.Label = "未连接"
		out.Color = red
	}
	return out
}

func queryTrayRoutes(token, nid string) []trayRoute {
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
		if r.CIDR == "" {
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
		out = append(out, trayRoute{CIDR: r.CIDR, Enabled: enabled, Active: r.Status == "active"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CIDR < out[j].CIDR })
	return out
}

func updateTrayNetworkItems(items []*application.MenuItem, networks []trayNetwork) {
	for i, item := range items {
		if i >= len(networks) {
			item.SetHidden(true)
			continue
		}
		network := networks[i]
		label := network.Name + " · " + network.IP
		if network.IP == "-" {
			label = network.Name
		}
		item.SetLabel(label).SetBitmap(dotIcon(network.Color)).SetHidden(false)
	}
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
			item := root.AddCheckbox(route.CIDR, route.Enabled).SetBitmap(routeStateIcon(route))
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
		root.Add("暂无子网路由").SetEnabled(false).SetBitmap(subnetIcon())
	}
}

func watchTrayStatus(statusItem, allowRelayItem *application.MenuItem, networkItems []*application.MenuItem, routeRoot, menu *application.Menu) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastRoutes := "\x00"
	lastNetworkState := "\x00"
	for {
		st := queryTrayStatus()
		sig := trayRouteSignature(st.Networks)
		netSig := trayNetworkSignature(st.Networks)
		changed := false
		application.InvokeSync(func() {
			statusItem.SetLabel(st.Label)
			statusItem.SetBitmap(dotIcon(st.Color))
			statusItem.SetHidden(len(st.Networks) > 0)
			trayRelayAllow = st.AllowRelay
			allowRelayItem.SetBitmap(relayCheckIcon(st.AllowRelay))
			if netSig != lastNetworkState {
				updateTrayNetworkItems(networkItems, st.Networks)
				lastNetworkState = netSig
				changed = true
			}
			if sig != lastRoutes {
				rebuildTrayRoutes(routeRoot, st.Networks)
				lastRoutes = sig
				changed = true
			}
		})
		if changed {
			updateTrayMenu(menu)
		}
		<-ticker.C
	}
}

func trayNetworkSignature(networks []trayNetwork) string {
	var b strings.Builder
	for _, n := range networks {
		b.WriteString(n.NID)
		b.WriteByte(':')
		b.WriteString(n.Name)
		b.WriteByte(':')
		b.WriteString(n.Status)
		b.WriteByte(':')
		b.WriteString(n.IP)
		b.WriteByte(';')
	}
	return b.String()
}

func trayRouteSignature(networks []trayNetwork) string {
	var b strings.Builder
	for _, n := range networks {
		b.WriteString(n.NID)
		b.WriteByte(':')
		for _, r := range n.Routes {
			b.WriteString(r.CIDR)
			if r.Active {
				b.WriteByte('a')
			} else {
				b.WriteByte('i')
			}
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
