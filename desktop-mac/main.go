package main

import (
	"context"
	"embed"
	"log"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend
var assets embed.FS

//go:embed assets/tray.png
var trayIcon []byte

func main() {
	app := NewApp()
	// Install/start the LaunchDaemon before the WebView so /ctl is ready on
	// first render. Only the first install prompts for administrator access.
	if err := app.ensureDaemon(); err != nil {
		app.setError(err)
		log.Printf("snetd: %v", err)
	}

	// fyne.io/systray must not run on the main thread while Wails owns the
	// macOS application loop.
	go startTray(app)

	err := wails.Run(&options.App{
		Title:             "SNET",
		Width:             1100,
		Height:            800,
		MinWidth:          800,
		MinHeight:         600,
		StartHidden:       true,
		HideWindowOnClose: true,
		BackgroundColour:  &options.RGBA{R: 13, G: 16, B: 23, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: newCtlProxy(),
		},
		OnStartup:     app.startup,
		OnBeforeClose: app.beforeClose,
		OnShutdown: func(ctx context.Context) {
			systray.Quit()
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		systray.Quit()
		log.Fatal(err)
	}
}

func startTray(app *App) {
	systray.Run(func() {
		systray.SetTemplateIcon(trayIcon, trayIcon)
		systray.SetTooltip("SNET 虚拟组网")

		show := systray.AddMenuItem("打开控制面板", "显示 SNET 控制面板")
		hide := systray.AddMenuItem("关闭窗口", "隐藏到菜单栏")
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出 SNET", "退出应用")

		go func() {
			for {
				select {
				case <-show.ClickedCh:
					app.ShowWindow()
				case <-hide.ClickedCh:
					app.HideWindow()
				case <-quit.ClickedCh:
					app.Quit()
					return
				}
			}
		}()
	}, func() {})
}
