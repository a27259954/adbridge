// Package config loads and persists adbridge settings (config.json).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Level is an MCP permission tier. Higher tiers include lower ones.
type Level string

const (
	LevelRead     Level = "read"     // logs, screenshots, device info
	LevelInteract Level = "interact" // + tap/swipe/input, start/stop apps
	LevelFull     Level = "full"     // + arbitrary shell, install, clear data
)

func (l Level) Rank() int {
	switch l {
	case LevelRead:
		return 1
	case LevelInteract:
		return 2
	case LevelFull:
		return 3
	}
	return 0
}

func (l Level) Valid() bool { return l.Rank() > 0 }

type MCP struct {
	// Addr is the local listen address. Keep it on loopback; remote access
	// goes through the tunnel.
	Addr string `json:"addr"`
	// Token authenticates remote (tunnel) requests. Stored in plain text on
	// purpose: %APPDATA% is only readable by this Windows user.
	Token string `json:"token"`
	// RequireTokenLocal also enforces the token for loopback clients.
	RequireTokenLocal bool  `json:"requireTokenLocal"`
	LocalLevel        Level `json:"localLevel"`
	RemoteLevel       Level `json:"remoteLevel"`
	// MaxOutputChars caps text returned by a single tool call.
	MaxOutputChars int `json:"maxOutputChars"`
}

type Tunnel struct {
	Provider       string `json:"provider"` // "ngrok" | "cloudflared"
	NgrokAuthtoken string `json:"ngrokAuthtoken"`
	// NgrokDomain is optional, e.g. "my-name.ngrok-free.app" (the free static domain).
	NgrokDomain string `json:"ngrokDomain"`
	// IdleMinutes stops the tunnel after this long without remote traffic. 0 = never.
	IdleMinutes int `json:"idleMinutes"`
}

type Config struct {
	// Language is the UI language (en, zh-CN, zh-TW, es, ja, ko). Empty = follow Windows.
	Language string `json:"language"`
	// ADBPath overrides adb.exe discovery. Empty = auto-detect.
	ADBPath string `json:"adbPath"`
	MCP     MCP    `json:"mcp"`
	Tunnel  Tunnel `json:"tunnel"`
}

// Languages are the supported UI languages; "" means follow the OS.
var Languages = []string{"", "en", "zh-CN", "zh-TW", "es", "ja", "ko"}

func Default() Config {
	return Config{
		MCP: MCP{
			Addr:           "127.0.0.1:8765",
			Token:          NewToken(),
			LocalLevel:     LevelFull,
			RemoteLevel:    LevelRead,
			MaxOutputChars: 60_000,
		},
		Tunnel: Tunnel{Provider: "ngrok", IdleMinutes: 30},
	}
}

// NewToken returns a random 32-byte hex token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Store is a concurrency-safe, file-backed Config.
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
	subs []func(Config)
}

// Open loads path, creating it with defaults when missing.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Default()}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, s.save()
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, err
	}
	s.normalize()
	return s, nil
}

// Get returns a copy of the current config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Update applies fn, validates, persists and notifies subscribers.
func (s *Store) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	fn(&s.cfg)
	s.normalize()
	err := s.save()
	cfg := s.cfg
	subs := append([]func(Config){}, s.subs...)
	s.mu.Unlock()
	for _, f := range subs {
		f(cfg)
	}
	return cfg, err
}

// OnChange registers fn to run after every Update.
func (s *Store) OnChange(fn func(Config)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

func (s *Store) normalize() {
	d := Default()
	c := &s.cfg
	if c.MCP.Addr == "" {
		c.MCP.Addr = d.MCP.Addr
	}
	if c.MCP.Token == "" {
		c.MCP.Token = NewToken()
	}
	if !c.MCP.LocalLevel.Valid() {
		c.MCP.LocalLevel = d.MCP.LocalLevel
	}
	if !c.MCP.RemoteLevel.Valid() {
		c.MCP.RemoteLevel = d.MCP.RemoteLevel
	}
	if c.MCP.MaxOutputChars <= 0 {
		c.MCP.MaxOutputChars = d.MCP.MaxOutputChars
	}
	if c.Tunnel.Provider != "ngrok" && c.Tunnel.Provider != "cloudflared" {
		c.Tunnel.Provider = d.Tunnel.Provider
	}
	if c.Tunnel.IdleMinutes < 0 {
		c.Tunnel.IdleMinutes = 0
	}
	if !slices.Contains(Languages, c.Language) {
		c.Language = ""
	}
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
