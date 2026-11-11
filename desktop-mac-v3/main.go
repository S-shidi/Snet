package main

import (
	"embed"
	"io"
	"log"
	"os"
	"os/exec"

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
	setupSystray(app, window, svc)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		log.Printf("SnetMac: application started")
		showWindow(window)
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

func setupSystray(app *application.App, window *application.WebviewWindow, svc *SnetService) {
	tray := app.SystemTray.New()
	if len(trayIcon) == 0 {
		log.Printf("SnetMac: tray icon is empty; continuing without template icon")
	} else {
		tray.SetTemplateIcon(trayIcon)
	}
	tray.SetTooltip("SNET 虚拟组网")

	menu := app.NewMenu()
	menu.Add("打开控制面板").OnClick(func(*application.Context) {
		showWindow(window)
	})
	menu.Add("退出 SNET").OnClick(func(*application.Context) {
		log.Printf("SnetMac: quit requested from tray")
		svc.ShutdownDaemon()
		app.Quit()
	})
	tray.SetMenu(menu)
}
