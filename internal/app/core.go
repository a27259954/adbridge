// Package app is the only package that knows about Wails. It wires the
// framework-free core (adb, device, mcpsrv, tunnel, ...) together and
// exposes it to the frontend as bound services and events.
package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/audit"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/device"
	"github.com/a27259954/adbridge/internal/mcpsrv"
	"github.com/a27259954/adbridge/internal/pairing"
	"github.com/a27259954/adbridge/internal/paths"
	"github.com/a27259954/adbridge/internal/staging"
	"github.com/a27259954/adbridge/internal/tunnel"
)

// Event names emitted to the frontend.
const (
	EvDevices = "devices"
	EvPairing = "pairing"
	EvTunnel  = "tunnel"
	EvAudit   = "audit"
	EvMCP     = "mcp"
)

func init() {
	application.RegisterEvent[[]device.Device](EvDevices)
	application.RegisterEvent[pairing.Status](EvPairing)
	application.RegisterEvent[tunnel.Status](EvTunnel)
	application.RegisterEvent[audit.Entry](EvAudit)
	application.RegisterEvent[mcpsrv.Status](EvMCP)
}

// Core owns every long-lived component.
type Core struct {
	Version string
	Log     *slog.Logger

	Config  *config.Store
	ADB     *adb.Client
	Devices *device.Manager
	Pairing *pairing.Service
	Audit   *audit.Log
	MCP     *mcpsrv.Server
	Tunnel  *tunnel.Manager

	adbErr   error
	store    storage
	cancel   context.CancelFunc
	stopOnce sync.Once
}

// storage is where the core keeps state. The demo build swaps all of it for
// a throwaway directory so screenshots never touch real user data.
type storage struct {
	config, devices, logs, uploads, recordings, webview string
}

func defaultStorage() storage {
	return storage{
		config:     paths.ConfigFile(),
		devices:    paths.DevicesFile(),
		logs:       paths.LogDir(),
		uploads:    paths.UploadsDir(),
		recordings: paths.RecordingsDir(),
		webview:    paths.WebviewDir(),
	}
}

// demoMode is implemented in demo.go, compiled only with -tags demo: it runs
// adbridge against an in-process fake adb server with three phones.
type demoMode interface {
	setup() (adbAddr string, st storage, err error)
	start(c *Core)
}

var demo demoMode

// WebviewDir is the WebView2 user data folder.
func (c *Core) WebviewDir() string { return c.store.webview }

func emit(name string, data any) {
	if a := application.Get(); a != nil {
		a.Event.Emit(name, data)
	}
}

// NewCore builds the component graph. Nothing runs until Start.
func NewCore(version string, log *slog.Logger) (*Core, error) {
	st, adbAddr := defaultStorage(), ""
	if demo != nil {
		var err error
		if adbAddr, st, err = demo.setup(); err != nil {
			return nil, err
		}
	}
	cfg, err := config.Open(st.config)
	if err != nil {
		return nil, err
	}
	c := &Core{Version: version, Log: log, Config: cfg, store: st}

	bin, err := adb.Locate(cfg.Get().ADBPath)
	c.adbErr = err
	c.ADB = adb.New(bin)
	if adbAddr != "" {
		c.ADB.Addr = adbAddr
	}

	c.Devices, err = device.New(c.ADB, st.devices, log)
	if err != nil {
		return nil, err
	}
	c.Pairing = pairing.New(c.Devices, func(s pairing.Status) { emit(EvPairing, s) })
	c.Audit = audit.New(st.logs)
	stage, err := staging.Open(st.uploads)
	if err != nil {
		return nil, err
	}

	var tun *tunnel.Manager
	c.MCP = mcpsrv.New(mcpsrv.Deps{
		Devices: c.Devices, Config: cfg, Audit: c.Audit, Version: version, Log: log,
		Staging: stage, RecordDir: st.recordings,
		OnRemote: func() { tun.Touch() },
	})
	tun = tunnel.New(cfg, c.MCP.RemoteHandler(), log, func(s tunnel.Status) { emit(EvTunnel, s) })
	c.Tunnel = tun
	return c, nil
}

// Start launches background work. Safe to call once.
func (c *Core) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.Devices.Subscribe(func(ds []device.Device) { emit(EvDevices, ds) })
	c.Audit.OnEntry(func(e audit.Entry) { emit(EvAudit, e) })
	go c.Devices.Run(ctx)
	if err := c.MCP.Start(); err != nil {
		c.Log.Error("mcp server failed to start", "err", err)
	}
	emit(EvMCP, c.MCP.Status())
	if demo != nil {
		go demo.start(c)
	}
}

// Stop shuts everything down.
func (c *Core) Stop() {
	c.stopOnce.Do(func() {
		c.Tunnel.Stop()
		c.MCP.Stop()
		if c.cancel != nil {
			c.cancel()
		}
		c.Audit.Close()
	})
}

// Services returns the Wails services bound to the frontend.
func (c *Core) Services() []application.Service {
	return []application.Service{
		application.NewService(&Lifecycle{c}),
		application.NewService(&DeviceService{c}),
		application.NewService(&LogService{c}),
		application.NewService(&PairService{c}),
		application.NewService(&MCPService{c}),
		application.NewService(&SettingsService{c}),
	}
}

// Lifecycle starts and stops the core with the application.
type Lifecycle struct{ c *Core }

func (l *Lifecycle) ServiceStartup(context.Context, application.ServiceOptions) error {
	l.c.Start()
	return nil
}

func (l *Lifecycle) ServiceShutdown() error {
	l.c.Stop()
	return nil
}
