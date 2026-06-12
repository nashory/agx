package main

import (
	"context"
	"log"

	desktopapp "github.com/nashory/agx/internal/desktop"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func main() {
	app, err := desktopapp.NewApp()
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:     "AGX",
		Width:     1440,
		Height:    940,
		MinWidth:  900,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		AlwaysOnTop:      false,
		Mac:              &mac.Options{},
		BackgroundColour: &options.RGBA{R: 248, G: 250, B: 252, A: 1},
		Bind: []interface{}{
			app,
		},
		OnStartup: func(ctx context.Context) {
			wailsruntime.WindowSetAlwaysOnTop(ctx, false)
			app.Startup(ctx)
		},
		OnShutdown: func(ctx context.Context) {
			_ = app.Close()
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
