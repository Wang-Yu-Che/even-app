package main

import (
	"embed"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/systray-glasses-template.png
var systrayGlassesTemplate []byte

func init() {
}

// main function serves as the application's entry point. It initializes the application, creates a window,
// and starts a goroutine that emits a time-based event every second. It subsequently runs the application and
// logs any error that might occur.
func main() {
	service := NewEvenService()

	// Create a new Wails application by providing the necessary options.
	// Variables 'Name' and 'Description' are for application metadata.
	// 'Assets' configures the asset server with the 'FS' variable pointing to the frontend files.
	// 'Bind' is a list of Go struct instances. The frontend has access to the methods of these instances.
	// 'Mac' options tailor the application when running an macOS.
	app := application.New(application.Options{
		Name:        "Even Glasses",
		Description: "A macOS client for Even Realities G2",
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	// Create a new window with the necessary options.
	// 'Title' is the title of the window.
	// 'Mac' options tailor the window when running on macOS.
	// 'BackgroundColour' is the background colour of the window.
	// 'URL' is the URL that will be loaded into the webview.
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Even Glasses",
		Width:  1080,
		Height: 720,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		// The client ships the light Even Realities palette by default, so the
		// window paints that colour before the webview has anything to draw.
		// A dark value here would flash black on every launch.
		BackgroundColour: application.NewRGB(238, 238, 238),
		URL:              "/",
	})

	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		window.Hide()
		event.Cancel()
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		window.Show().Focus()
	})

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(systrayGlassesTemplate)
	}
	tray.SetTooltip("Even Glasses · 眼镜显示")

	menu := app.NewMenu()
	statusItem := menu.Add("眼镜当前无显示").SetEnabled(false)
	menu.AddSeparator()
	previewItems := make([]*application.MenuItem, maxRows)
	for index := range previewItems {
		previewItems[index] = menu.Add(" ").SetEnabled(false).SetHidden(true)
	}
	menu.AddSeparator()
	menu.Add("显示 Even Glasses").OnClick(func(*application.Context) {
		window.Show().Focus()
	})
	menu.Add("退出 Even Glasses").OnClick(func(*application.Context) {
		app.Quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(tray.ShowMenu)

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go updateTrayPreview(service, tray, statusItem, previewItems)
	})

	// Run the application. This blocks until the application has been exited.
	err := app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}

func updateTrayPreview(service *EvenService, tray *application.SystemTray, statusItem *application.MenuItem, previewItems []*application.MenuItem) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status := service.CodexStatus()
		label, heading, rows := trayPreview(status)
		tray.SetLabel(label)
		statusItem.SetLabel(heading)
		for index, item := range previewItems {
			if index < len(rows) {
				item.SetLabel(rows[index]).SetHidden(false)
			} else {
				item.SetHidden(true)
			}
		}
		<-ticker.C
	}
}

func trayPreview(status CodexStatus) (label, heading string, rows []string) {
	if len(status.Rows) == 0 {
		return "", "眼镜当前无显示", nil
	}

	stateLabel := map[string]string{
		"thinking":    "AI",
		"tool":        "RUN",
		"needs_input": "INPUT",
		"permission":  "INPUT",
		"paused":      "PAUSE",
		"done":        "DONE",
		"error":       "ERROR",
		"failed":      "ERROR",
	}[status.State]
	if stateLabel == "" {
		stateLabel = strings.ToUpper(status.State)
	}
	if stateLabel != "" {
		label = " " + stateLabel
	}
	return label, "眼镜当前显示", append([]string(nil), status.Rows...)
}
