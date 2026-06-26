package main

import (
	"context"
	_ "embed"
	"log"
	"time"

	desktopapp "github.com/nashory/agx/internal/desktop"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed appicon.png
var appIcon []byte

func applyApplicationIcon() {
	setApplicationIcon(appIcon)
	time.AfterFunc(250*time.Millisecond, func() { setApplicationIcon(appIcon) })
	time.AfterFunc(time.Second, func() { setApplicationIcon(appIcon) })
}

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
		Mac:              &mac.Options{About: &mac.AboutInfo{Title: "AGX", Icon: appIcon}},
		Linux:            &linux.Options{Icon: appIcon, ProgramName: "AGX", WebviewGpuPolicy: linux.WebviewGpuPolicyNever},
		BackgroundColour: &options.RGBA{R: 248, G: 250, B: 252, A: 1},
		Bind: []interface{}{
			app,
		},
		OnStartup: func(ctx context.Context) {
			applyApplicationIcon()
			wailsruntime.WindowSetAlwaysOnTop(ctx, false)
			app.Startup(ctx)
		},
		OnDomReady: func(ctx context.Context) {
			applyApplicationIcon()
		},
		OnShutdown: func(ctx context.Context) {
			_ = app.Close()
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
