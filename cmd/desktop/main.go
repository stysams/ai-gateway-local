// Command desktop is the Wails desktop shell for ai-gateway.
package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"sync/atomic"

	"ai-gateway/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:assets
var assets embed.FS

// desktopInstanceID is the Wails single-instance identity. A second desktop
// process must notify the first and exit so only one tray icon exists
// (docs/v1-scheme.md §15.1). serve mode never reaches this lock: it is a
// headless gateway process and uses gateway.lock instead (§14.2).
const desktopInstanceID = "local.ai-gateway.desktop"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(app.Main(os.Args[1:]))
	}

	var window atomic.Pointer[application.WebviewWindow]
	var showRequested atomic.Bool

	// Acquire the desktop single-instance lock before ensureGateway so a
	// second launch cannot spawn another serve process or another tray.
	desktop := application.New(application.Options{
		Name:        "ai-gateway",
		Description: "Local AI gateway control surface",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: desktopInstanceID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				showRequested.Store(true)
				activateDesktopWindow(window.Load())
			},
		},
	})

	port := app.ManagementPort()
	if err := ensureGateway(port); err != nil {
		log.Printf("gateway startup: %v", err)
	}
	apiURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	created := desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "ai-gateway",
		Width:            1180,
		Height:           760,
		MinWidth:         390,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(250, 250, 250),
		URL:              "/?api=" + apiURL,
	})
	window.Store(created)
	if showRequested.Load() {
		activateDesktopWindow(created)
	}
	tray := newTrayController(desktop, created, apiURL, port)
	created.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if tray.quitting.Load() {
			return
		}
		event.Cancel()
		created.Hide()
	})
	if err := desktop.Run(); err != nil {
		log.Fatal(err)
	}
}

func activateDesktopWindow(w *application.WebviewWindow) {
	if w == nil {
		return
	}
	w.Show()
	w.UnMinimise()
	w.Restore()
	w.Focus()
}
