package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	app := NewApp()
	// Start the privileged daemon before the WebView so /ctl is ready on the
	// first render. macOS shows the standard authorization prompt here.
	if err := app.ensureDaemon(); err != nil {
		app.setError(err)
		log.Printf("snetd: %v", err)
	}

	err := wails.Run(&options.App{
		Title:             "SNET",
		Width:             1040,
		Height:            760,
		MinWidth:          760,
		MinHeight:         560,
		StartHidden:       false,
		HideWindowOnClose: false,
		BackgroundColour:  &options.RGBA{R: 13, G: 16, B: 23, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: newCtlProxy(),
		},
		OnStartup: app.startup,
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
