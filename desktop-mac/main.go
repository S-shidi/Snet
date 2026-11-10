package main

import (
	"embed"
	"log"

	"github.com/efeenesc/systray"
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
	// Start the privileged daemon before the WebView so /ctl is ready on the
	// first render. macOS shows the standard authorization prompt here.
	if err := app.ensureDaemon(); err != nil {
		app.setError(err)
		log.Printf("snetd: %v", err)
	}

	go startTray(app)

	err := wails.Run(&options.App{
		Title:             "SNET",
		Width:             1040,
		Height:            760,
		MinWidth:          760,
		MinHeight:         560,
		StartHidden:       true,
		HideWindowOnClose: true,
		BackgroundColour:  &options.RGBA{R: 13, G: 16, B: 23, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: newCtlProxy(),
		},
		OnStartup:     app.startup,
		OnBeforeClose: app.beforeClose,
		OnShutdown:    app.shutdown,
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
		hide := systray.AddMenuItem("隐藏控制面板", "隐藏到菜单栏")
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
