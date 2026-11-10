package main

import (
	"embed"
	"log"

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

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Snet",
		Width:            1100,
		Height:           800,
		MinWidth:         800,
		MinHeight:        600,
		Hidden:           true,
		URL:              "/",
		BackgroundColour: application.NewRGB(13, 16, 23),
		Mac: application.MacWindow{
			TitleBar:   application.MacTitleBarHiddenInset,
			Appearance: application.NSAppearanceNameDarkAqua,
		},
	})
	window.Hide()
	window.OnWindowEvent(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		window.Hide()
	})
	setupSystray(app, window)

	if err := svc.StartDaemon(); err != nil {
		log.Printf("snetd: %v", err)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func setupSystray(app *application.App, window *application.WebviewWindow) {
	tray := app.SystemTray.New()
	tray.SetTemplateIcon(trayIcon)
	tray.SetTooltip("SNET 虚拟组网")

	menu := app.NewMenu()
	menu.Add("打开控制面板").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	menu.Add("退出 SNET").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(func() {
		window.Show()
		window.Focus()
	})
}
