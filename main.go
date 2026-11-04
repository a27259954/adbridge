// adbridge: multi-device ADB bridge with an MCP endpoint for AI debugging.
package main

import (
	"embed"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/a27259954/adbridge/internal/app"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/paths"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	if err := paths.Ensure(); err != nil {
		panic(err)
	}
	log := newLogger()

	core, err := app.NewCore(version, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}

	var window *application.WebviewWindow
	show := func() {
		if window != nil {
			window.Show()
			window.Restore()
			window.Focus()
		}
	}

	wapp := application.New(application.Options{
		Name:        "adbridge",
		Description: "Multi-device ADB bridge with an MCP server for AI debugging",
		Icon:        icon,
		Services:    core.Services(),
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Windows: application.WindowsOptions{
			// Keep WebView2 data out of the exe's folder.
			WebviewUserDataPath: paths.WebviewDir(),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "io.github.a27259954.adbridge",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { show() },
		},
	})

	window = wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "adbridge",
		Width:            1180,
		Height:           720,
		MinWidth:         860,
		MinHeight:        520,
		BackgroundColour: application.NewRGB(17, 19, 23),
		URL:              "/",
	})

	// Closing the window keeps adbridge (and the MCP endpoint) running in the tray.
	var quitting atomic.Bool
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !quitting.Load() {
			window.Hide()
			e.Cancel()
		}
	})

	tray := wapp.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("adbridge")
	labels := core.Tray()
	menu := wapp.NewMenu()
	openItem := menu.Add(labels.Open).OnClick(func(*application.Context) { show() })
	menu.AddSeparator()
	stopItem := menu.Add(labels.StopTunnel).OnClick(func(*application.Context) { core.Tunnel.Stop() })
	menu.AddSeparator()
	quitItem := menu.Add(labels.Quit).OnClick(func(*application.Context) {
		quitting.Store(true)
		wapp.Quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(show)
	// Follow language changes made in Settings.
	core.Config.OnChange(func(config.Config) {
		l := core.Tray()
		openItem.SetLabel(l.Open)
		stopItem.SetLabel(l.StopTunnel)
		quitItem.SetLabel(l.Quit)
		menu.Update()
	})

	if err := wapp.Run(); err != nil {
		log.Error("app exited", "err", err)
	}
	core.Stop()
}

// newLogger writes to %LOCALAPPDATA%\adbridge\logs\app.log, rotating at 5 MB.
func newLogger() *slog.Logger {
	p := filepath.Join(paths.LogDir(), "app.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(p, p+".1")
	}
	var w io.Writer = os.Stderr
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		w = io.MultiWriter(f, os.Stderr)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
