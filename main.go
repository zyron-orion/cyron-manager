package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	webviewDir := ""
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		webviewDir = filepath.Join(base, "CYRON", "WebView2")
		_ = os.MkdirAll(webviewDir, 0o755)
	}
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "CYRON Manager",
		Width:     1280,
		Height:    860,
		MinWidth:  320,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true},
		BackgroundColour: &options.RGBA{R: 14, G: 17, B: 22, A: 1},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.cyron.manager",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				app.focusWindow()
			},
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewUserDataPath:  webviewDir,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
