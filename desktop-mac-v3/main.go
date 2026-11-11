package main

import (
	"embed"
	"log"
	"os/exec"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/tray.png
var trayIcon []byte

func main() {
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
	app.OnShutdown(svc.ShutdownDaemon)

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
	window.OnWindowEvent(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		window.Hide()
	})
	setupSystray(app, window, svc)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		showWindow(app, window)
	})

	if err := svc.StartDaemon(); err != nil {
		log.Printf("snetd: %v", err)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func showWindow(app *application.App, window *application.WebviewWindow) {
	app.Show()
	window.Show()
	window.Focus()
	_ = exec.Command("osascript", "-e", `tell application "System Events" to set frontmost of process "SnetMac" to true`).Run()
}

func setupSystray(app *application.App, window *application.WebviewWindow, svc *SnetService) {
	tray := app.SystemTray.New()
	tray.SetTemplateIcon(trayIcon)
	tray.SetTooltip("SNET 虚拟组网")

	menu := app.NewMenu()
	menu.Add("打开控制面板").OnClick(func(*application.Context) {
		showWindow(app, window)
	})
	menu.Add("退出 SNET").OnClick(func(*application.Context) {
		svc.ShutdownDaemon()
		app.Quit()
	})
	tray.SetMenu(menu)
}
