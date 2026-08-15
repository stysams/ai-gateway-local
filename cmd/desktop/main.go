// Command desktop is the Wails desktop shell for ai-gateway.
package main

import (
	"embed"
	"fmt"
	"log"
	"os"

	"ai-gateway/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:assets
var assets embed.FS

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(app.Main(os.Args[1:]))
	}

	port := app.ManagementPort()
	if err := ensureGateway(port); err != nil {
		log.Printf("gateway startup: %v", err)
	}
	apiURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	desktop := application.New(application.Options{
		Name:        "ai-gateway",
		Description: "Local AI gateway control surface",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
	})
	window := desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "ai-gateway",
		Width:            1180,
		Height:           760,
		MinWidth:         390,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(250, 250, 250),
		URL:              "/?api=" + apiURL,
	})
	tray := newTrayController(desktop, window, apiURL, port)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if tray.quitting.Load() {
			return
		}
		event.Cancel()
		window.Hide()
	})
	if err := desktop.Run(); err != nil {
		log.Fatal(err)
	}
}
