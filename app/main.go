// CG50 Studio: a desktop app around the fx-CG50 emulator core (../emu_go), built with Wails.
// The window is a web page (frontend/, plain HTML/CSS/JS, no build step); this Go side owns
// the emulator, its run loop, files and dialogs, and serves the screen at /frame.
//
//	wails dev      (from app/: live reload of frontend/)
//	wails build    (app/build/bin/)
//
// Flags: -hidden (no window: testing through the dev page), -minimized.
package main

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend
var assets embed.FS

const appName = "CG50 Studio"

func main() {
	front, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	app := NewApp()
	// -hidden: no window at all (the app and, under `wails dev`, http://localhost:34115 still
	// work), for automated testing without taking the user's focus. -minimized: start minimized.
	hidden := slices.Contains(os.Args[1:], "-hidden")
	state := options.Normal
	if slices.Contains(os.Args[1:], "-minimized") {
		state = options.Minimised
	}
	err = wails.Run(&options.App{
		StartHidden:      hidden,
		WindowStartState: state,
		Title:     appName,
		Width:     1400,
		Height:    860,
		MinWidth:  960,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets:  front,
			Handler: frameServer{app}, // /frame
		},
		// the page's own background (frontend/css/app.css --room), so no flash on start
		BackgroundColour: &options.RGBA{R: 14, G: 27, B: 31, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
