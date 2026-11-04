// Package mcpsrv exposes the device manager as an MCP server over
// Streamable HTTP.
//
// Two handlers share one mcp.Server:
//   - Local: served on 127.0.0.1 (DNS-rebinding protected, token optional).
//   - Remote: served on the tunnel (token required, separate permission level).
//
// The middleware stamps each request with its origin in a private header
// (overwriting anything the client sent) so tool handlers can apply the
// right permission level.
package mcpsrv

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/a27259954/adbridge/internal/audit"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/device"
	"github.com/a27259954/adbridge/internal/staging"
)

const (
	OriginLocal  = "local"
	OriginRemote = "remote"

	hdrOrigin = "X-Adbridge-Origin"
	hdrPeer   = "X-Adbridge-Peer"
)

type Deps struct {
	Devices *device.Manager
	Config  *config.Store
	Audit   *audit.Log
	// Staging holds chunked uploads (upload_file); nil disables those tools.
	Staging *staging.Store
	// RecordDir receives screenrecord MP4s ("" = do not keep them).
	RecordDir string
	Version   string
	Log       *slog.Logger
	// OnRemote is called for every authenticated remote request (tunnel idle timer).
	OnRemote func()
}

type Status struct {
	Running bool   `json:"running"`
	Addr    string `json:"addr"`
	URL     string `json:"url"`
	Error   string `json:"error,omitempty"`
}

type Server struct {
	d      Deps
	mcp    *mcp.Server
	local  http.Handler
	remote http.Handler

	mu     sync.Mutex
	http   *http.Server
	status Status
}

func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.OnRemote == nil {
		d.OnRemote = func() {}
	}
	s := &Server{d: d}
	// No SDK logger: in stateless mode it logs connect/disconnect for every
	// request. Tool calls are recorded by the audit log instead.
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "adbridge", Title: "adbridge", Version: d.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	s.registerTools()
	s.registerFileTools()
	get := func(*http.Request) *mcp.Server { return s.mcp }
	// Stateless + JSON responses: no long-lived SSE streams, so it works
	// through any HTTP tunnel and survives app restarts.
	s.local = s.guard(mcp.NewStreamableHTTPHandler(get, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxBody}), OriginLocal)
	s.remote = s.guard(mcp.NewStreamableHTTPHandler(get, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true, MaxRequestBodyBytes: maxBody}), OriginRemote)
	return s
}

// maxBody fits one 4 MiB upload chunk after base64 and JSON overhead.
const maxBody = 8 << 20

const instructions = `adbridge controls Android devices connected to the user's PC over adb.
Start with list_devices. The "device" argument accepts a device id, alias or serial and may be omitted when exactly one device is available.
To interact with the UI prefer ui_tree followed by tap {element: N} over guessing coordinates; screenshot coordinates must be scaled back to device pixels (the screenshot result states the factor).
To install a build made elsewhere: upload_file in base64 chunks of at most 4 MiB (final=true on the last), then install_apk {upload_id}. pull_file/push_file move files; device_http reaches HTTP servers on the device (e.g. localabstract:chrome_devtools_remote) without any PC port.
Tools are gated by a permission level (read < interact < full) chosen by the user in adbridge. If a call is denied, tell the user which level is needed instead of retrying.`

// RemoteHandler is mounted on the tunnel.
func (s *Server) RemoteHandler() http.Handler { return s.remote }

// LocalHandler is mounted on the loopback listener.
func (s *Server) LocalHandler() http.Handler { return s.local }

// Start listens on the configured loopback address.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http != nil {
		return nil
	}
	addr := s.d.Config.Get().MCP.Addr
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.status = Status{Addr: addr, Error: err.Error()}
		return err
	}
	srv := &http.Server{Handler: s.local, ReadHeaderTimeout: 10 * time.Second}
	s.http = srv
	s.status = Status{Running: true, Addr: ln.Addr().String(), URL: "http://" + ln.Addr().String() + "/mcp"}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.status.Running, s.status.Error = false, err.Error()
			s.mu.Unlock()
		}
	}()
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.http
	s.http = nil
	s.status.Running = false
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func (s *Server) Restart() error { s.Stop(); return s.Start() }

func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// guard authenticates, normalises the path and stamps the origin.
func (s *Server) guard(next http.Handler, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.d.Config.Get().MCP
		path := strings.TrimSuffix(r.URL.Path, "/")
		var pathToken string
		switch {
		case path == "/mcp":
		case strings.HasPrefix(path, "/mcp/"):
			pathToken = strings.TrimPrefix(path, "/mcp/")
		case path == "" && r.Method == http.MethodGet:
			fmt.Fprintln(w, "adbridge MCP endpoint: POST /mcp (Streamable HTTP)")
			return
		default:
			http.NotFound(w, r)
			return
		}
		if origin == OriginRemote || cfg.RequireTokenLocal || pathToken != "" {
			if !tokenOK(r, pathToken, cfg.Token) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="adbridge"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if origin == OriginLocal && !localOriginOK(r.Header.Get("Origin")) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		r.Header.Set(hdrOrigin, origin)
		r.Header.Set(hdrPeer, peer(r, origin))
		r.Header.Del("Authorization") // never reaches tool handlers or logs
		r.URL.Path = "/mcp"
		if origin == OriginRemote {
			s.d.OnRemote()
		}
		next.ServeHTTP(w, r)
	})
}

func tokenOK(r *http.Request, pathToken, want string) bool {
	if want == "" {
		return false
	}
	got := pathToken
	if got == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// localOriginOK blocks web pages from driving the local endpoint while
// still allowing browser-based tools served from localhost (MCP Inspector).
func localOriginOK(origin string) bool {
	if origin == "" {
		return true
	}
	for _, p := range []string{"http://localhost", "http://127.0.0.1", "http://[::1]", "https://localhost", "https://127.0.0.1"} {
		if origin == p || strings.HasPrefix(origin, p+":") {
			return true
		}
	}
	return false
}

func peer(r *http.Request, origin string) string {
	if origin == OriginRemote {
		for _, h := range []string{"Cf-Connecting-Ip", "X-Forwarded-For"} {
			if v := r.Header.Get(h); v != "" {
				ip, _, _ := strings.Cut(v, ",")
				return strings.TrimSpace(ip)
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
