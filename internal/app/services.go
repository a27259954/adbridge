package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/audit"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/device"
	"github.com/a27259954/adbridge/internal/mcpsrv"
	"github.com/a27259954/adbridge/internal/pairing"
	"github.com/a27259954/adbridge/internal/paths"
	"github.com/a27259954/adbridge/internal/tunnel"
)

func timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// ---- devices -----------------------------------------------------------------

// DeviceService manages connections and per-device settings.
type DeviceService struct{ c *Core }

func (s *DeviceService) List() []device.Device { return s.c.Devices.List() }

func (s *DeviceService) Connect(addr string) error {
	ctx, cancel := timeout(20 * time.Second)
	defer cancel()
	return s.c.Devices.Connect(ctx, addr)
}

func (s *DeviceService) Pair(addr, code string) error {
	ctx, cancel := timeout(40 * time.Second)
	defer cancel()
	return s.c.Devices.Pair(ctx, addr, code)
}

func (s *DeviceService) Disconnect(id string) error {
	ctx, cancel := timeout(10 * time.Second)
	defer cancel()
	return s.c.Devices.Disconnect(ctx, id)
}

func (s *DeviceService) Forget(id string) error {
	ctx, cancel := timeout(10 * time.Second)
	defer cancel()
	return s.c.Devices.Forget(ctx, id)
}

// USBToWifi switches a USB device to Wi-Fi and returns the new address.
func (s *DeviceService) USBToWifi(id string) (string, error) {
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	return s.c.Devices.USBToWifi(ctx, id)
}

func (s *DeviceService) SetAlias(id, alias string) error { return s.c.Devices.SetAlias(id, alias) }
func (s *DeviceService) SetAIEnabled(id string, on bool) error {
	return s.c.Devices.SetAIEnabled(id, on)
}
func (s *DeviceService) SetAutoReconnect(id string, on bool) error {
	return s.c.Devices.SetAutoReconnect(id, on)
}

type ShellOutput struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
}

// Shell runs a one-shot command for the Shell panel.
func (s *DeviceService) Shell(id, cmd string) (ShellOutput, error) {
	ctx, cancel := timeout(60 * time.Second)
	defer cancel()
	_, res, err := s.c.Devices.Shell(ctx, id, cmd)
	if err != nil {
		return ShellOutput{}, err
	}
	out := string(res.Stdout)
	if len(res.Stderr) > 0 {
		out += string(res.Stderr)
	}
	return ShellOutput{Output: out, ExitCode: res.ExitCode}, nil
}

func (s *DeviceService) Info(id string) (device.Info, error) {
	ctx, cancel := timeout(15 * time.Second)
	defer cancel()
	return s.c.Devices.Info(ctx, id)
}

// Screenshot returns a data URL for the preview pane.
func (s *DeviceService) Screenshot(id string) (string, error) {
	ctx, cancel := timeout(15 * time.Second)
	defer cancel()
	data, mime, _, _, err := s.c.Devices.Screenshot(ctx, id, 900, 80)
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

type ADBStatus struct {
	Path     string `json:"path"`
	Version  string `json:"version"`
	ServerOK bool   `json:"serverOk"`
	Error    string `json:"error,omitempty"`
}

func (s *DeviceService) ADBStatus() ADBStatus {
	st := ADBStatus{Path: s.c.ADB.Bin()}
	ctx, cancel := timeout(5 * time.Second)
	defer cancel()
	if st.Path != "" {
		st.Version, _ = adb.BinaryVersion(ctx, st.Path)
	} else if s.c.adbErr != nil {
		st.Error = s.c.adbErr.Error()
	}
	if err := s.c.Devices.TrackError(); err != nil {
		if st.Error == "" {
			st.Error = err.Error()
		}
	} else {
		st.ServerOK = true
	}
	return st
}

// ---- logs --------------------------------------------------------------------

type LogService struct{ c *Core }

// Since returns new log lines for the device after seq (polled by the UI).
func (s *LogService) Since(id string, seq uint64, max int) []device.LogEntry {
	r := s.c.Devices.Logcat(id)
	if r == nil {
		return nil
	}
	if max <= 0 || max > 5000 {
		max = 5000
	}
	return r.Since(seq, max)
}

func (s *LogService) Clear(id string) error {
	ctx, cancel := timeout(10 * time.Second)
	defer cancel()
	return s.c.Devices.ClearLogcat(ctx, id)
}

// ---- pairing -----------------------------------------------------------------

type PairService struct{ c *Core }

func (s *PairService) StartQR() (pairing.Status, error) { return s.c.Pairing.Start() }
func (s *PairService) Cancel()                          { s.c.Pairing.Cancel() }
func (s *PairService) Status() pairing.Status           { return s.c.Pairing.Status() }

// ---- MCP ---------------------------------------------------------------------

type MCPService struct{ c *Core }

type MCPState struct {
	Server      mcpsrv.Status `json:"server"`
	Tunnel      tunnel.Status `json:"tunnel"`
	LocalLevel  config.Level  `json:"localLevel"`
	RemoteLevel config.Level  `json:"remoteLevel"`
	TokenTail   string        `json:"tokenTail"`
}

func (s *MCPService) State() MCPState {
	cfg := s.c.Config.Get().MCP
	tail := cfg.Token
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return MCPState{
		Server: s.c.MCP.Status(), Tunnel: s.c.Tunnel.Status(),
		LocalLevel: cfg.LocalLevel, RemoteLevel: cfg.RemoteLevel, TokenTail: tail,
	}
}

// Token returns the full token (for the copy button only).
func (s *MCPService) Token() string { return s.c.Config.Get().MCP.Token }

func (s *MCPService) RegenerateToken() error {
	_, err := s.c.Config.Update(func(c *config.Config) { c.MCP.Token = config.NewToken() })
	return err
}

// SetLevel sets the permission level for "local" or "remote" clients.
func (s *MCPService) SetLevel(origin string, level string) error {
	l := config.Level(level)
	if !l.Valid() {
		return errors.New("invalid level " + level)
	}
	_, err := s.c.Config.Update(func(c *config.Config) {
		if origin == mcpsrv.OriginRemote {
			c.MCP.RemoteLevel = l
		} else {
			c.MCP.LocalLevel = l
		}
	})
	return err
}

func (s *MCPService) StartTunnel() error { return s.c.Tunnel.Start() }
func (s *MCPService) StopTunnel()        { s.c.Tunnel.Stop() }

// SetServerRunning is the kill switch for the local endpoint (and tunnel).
func (s *MCPService) SetServerRunning(on bool) error {
	var err error
	if on {
		err = s.c.MCP.Start()
	} else {
		s.c.Tunnel.Stop()
		s.c.MCP.Stop()
	}
	emit(EvMCP, s.c.MCP.Status())
	return err
}

func (s *MCPService) AuditSince(seq uint64) []audit.Entry { return s.c.Audit.Since(seq) }

type ClientConfig struct {
	URL       string `json:"url"`
	SecretURL string `json:"secretUrl,omitempty"`
	JSON      string `json:"json"`
	ClaudeCLI string `json:"claudeCli"`
}

// ClientConfig renders ready-to-paste MCP client settings for "local" or "remote".
func (s *MCPService) ClientConfig(origin string) (ClientConfig, error) {
	cfg := s.c.Config.Get().MCP
	server := map[string]any{"type": "http"}
	var cc ClientConfig
	if origin == mcpsrv.OriginRemote {
		st := s.c.Tunnel.Status()
		if st.State != tunnel.StateOn {
			return cc, errors.New("tunnel is not running")
		}
		base := strings.TrimSuffix(st.URL, "/")
		cc.URL = base + "/mcp"
		cc.SecretURL = base + "/mcp/" + cfg.Token
		server["url"] = cc.URL
		server["headers"] = map[string]string{"Authorization": "Bearer " + cfg.Token}
		cc.ClaudeCLI = "claude mcp add --transport http adbridge " + cc.URL + ` --header "Authorization: Bearer ` + cfg.Token + `"`
	} else {
		st := s.c.MCP.Status()
		if !st.Running {
			return cc, errors.New("MCP server is not running")
		}
		cc.URL = st.URL
		server["url"] = cc.URL
		if cfg.RequireTokenLocal {
			server["headers"] = map[string]string{"Authorization": "Bearer " + cfg.Token}
		}
		cc.ClaudeCLI = "claude mcp add --transport http adbridge " + cc.URL
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"adbridge": server}}, "", "  ")
	cc.JSON = string(b)
	return cc, nil
}

// ---- settings ----------------------------------------------------------------

type SettingsService struct{ c *Core }

func (s *SettingsService) Get() config.Config { return s.c.Config.Get() }

// Save persists settings and applies what changed.
func (s *SettingsService) Save(next config.Config) error {
	prev := s.c.Config.Get()
	if next.ADBPath != prev.ADBPath {
		bin, err := adb.Locate(next.ADBPath)
		if err != nil {
			return err
		}
		s.c.ADB.SetBin(bin)
		s.c.adbErr = nil
	}
	saved, err := s.c.Config.Update(func(c *config.Config) {
		token := c.MCP.Token
		*c = next
		c.MCP.Token = token // token changes only through RegenerateToken
	})
	if err != nil {
		return err
	}
	if saved.MCP.Addr != prev.MCP.Addr {
		err = s.c.MCP.Restart()
		emit(EvMCP, s.c.MCP.Status())
	}
	return err
}

func (s *SettingsService) OpenDataDir() error   { return openFolder(paths.DataDir()) }
func (s *SettingsService) OpenConfigDir() error { return openFolder(paths.ConfigDir()) }

func (s *SettingsService) Version() string { return s.c.Version }

// Language returns the effective UI language (configured, or the Windows
// display language when set to auto).
func (s *SettingsService) Language() string { return s.c.Language() }

func openFolder(dir string) error {
	cmd := exec.Command("explorer.exe", dir)
	return cmd.Start()
}
