package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/adb/adbtest"
	"github.com/a27259954/adbridge/internal/audit"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/device"
	"github.com/a27259954/adbridge/internal/staging"
)

type fixture struct {
	srv   *Server
	cfg   *config.Store
	audit *audit.Log
	fake  *adbtest.Server
	recs  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fake := adbtest.New(t)
	fake.OnShell(func(serial, cmd string) (string, string, int) {
		switch {
		case strings.HasPrefix(cmd, "getprop"):
			return "[ro.serialno]: [PIX8]\n[ro.product.model]: [Pixel 8]\n[ro.build.version.sdk]: [35]\n__BATT__\nlevel: 90\n", "", 0
		case strings.HasPrefix(cmd, "echo"):
			return strings.TrimPrefix(cmd, "echo ") + "\n", "", 0
		}
		return "", "", 0
	})
	fake.SetDevices("192.168.1.30:5555 device model:Pixel_8 transport_id:1\n")

	mgr, err := device.New(&adb.Client{Addr: fake.Addr()}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mgr.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ds := mgr.List(); len(ds) == 1 && ds[0].ID == "PIX8" && ds[0].State == device.StateOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("device not ready: %+v", mgr.List())
		}
		time.Sleep(20 * time.Millisecond)
	}

	cfg, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	al := audit.New("")
	stage, err := staging.Open(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	recs := t.TempDir()
	srv := New(Deps{Devices: mgr, Config: cfg, Audit: al, Version: "test", Staging: stage, RecordDir: recs})
	return &fixture{srv: srv, cfg: cfg, audit: al, fake: fake, recs: recs}
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	r.Header.Set(hdrOrigin, OriginLocal) // spoof attempt: must be overwritten by the guard
	return b.base.RoundTrip(r)
}

func connect(t *testing.T, url string, hc *http.Client) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: hc, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestLocalToolsAndAudit(t *testing.T) {
	f := newFixture(t)
	ts := httptest.NewServer(f.srv.LocalHandler())
	defer ts.Close()
	cs := connect(t, ts.URL+"/mcp", nil)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 15 {
		t.Fatalf("expected the full tool set, got %d", len(tools.Tools))
	}

	out, isErr := callText(t, cs, "list_devices", nil)
	if isErr || !strings.Contains(out, `"PIX8"`) || !strings.Contains(out, `"wifi"`) {
		t.Fatalf("list_devices: %s", out)
	}

	out, isErr = callText(t, cs, "shell", map[string]any{"command": "echo hi"})
	if isErr || !strings.Contains(out, "exit 0") || !strings.Contains(out, "hi") {
		t.Fatalf("shell (local=full): %s", out)
	}

	entries := f.audit.Since(0)
	if len(entries) != 2 || entries[1].Tool != "shell" || !entries[1].OK || entries[1].Origin != OriginLocal || entries[1].Device != "Pixel 8" {
		t.Fatalf("audit: %+v", entries)
	}
}

func TestRemoteRequiresToken(t *testing.T) {
	f := newFixture(t)
	ts := httptest.NewServer(f.srv.RemoteHandler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
	resp, err = http.Post(ts.URL+"/mcp/wrong-token", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 with wrong path token, got %d", resp.StatusCode)
	}

	// Secret-path auth works.
	token := f.cfg.Get().MCP.Token
	cs := connect(t, ts.URL+"/mcp/"+token, nil)
	if out, isErr := callText(t, cs, "list_devices", nil); isErr {
		t.Fatalf("list_devices via path token: %s", out)
	}
}

func TestRemoteLevelIsEnforced(t *testing.T) {
	f := newFixture(t)
	ts := httptest.NewServer(f.srv.RemoteHandler())
	defer ts.Close()
	token := f.cfg.Get().MCP.Token
	cs := connect(t, ts.URL+"/mcp", &http.Client{Transport: bearer{token, http.DefaultTransport}})

	// Default remote level is read: shell must be denied, even though the
	// client sends a forged local-origin header.
	out, isErr := callText(t, cs, "shell", map[string]any{"command": "echo hi"})
	if !isErr || !strings.Contains(out, `requires permission level "full"`) {
		t.Fatalf("shell should be denied remotely: %s", out)
	}
	last, _ := f.audit.Last()
	if !last.Denied || last.Origin != OriginRemote {
		t.Fatalf("denial not audited: %+v", last)
	}

	f.cfg.Update(func(c *config.Config) { c.MCP.RemoteLevel = config.LevelFull })
	if out, isErr := callText(t, cs, "shell", map[string]any{"command": "echo ok"}); isErr || !strings.Contains(out, "ok") {
		t.Fatalf("shell after raising level: %s", out)
	}
}

func TestDisabledDeviceIsHidden(t *testing.T) {
	f := newFixture(t)
	if err := f.srv.d.Devices.SetAIEnabled("PIX8", false); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(f.srv.LocalHandler())
	defer ts.Close()
	cs := connect(t, ts.URL+"/mcp", nil)
	if out, _ := callText(t, cs, "list_devices", nil); strings.Contains(out, "PIX8") {
		t.Fatalf("disabled device listed: %s", out)
	}
	if out, isErr := callText(t, cs, "device_info", map[string]any{"device": "PIX8"}); !isErr || !strings.Contains(out, "not enabled") {
		t.Fatalf("disabled device usable: %s", out)
	}
}

func TestLocalOriginCheck(t *testing.T) {
	f := newFixture(t)
	ts := httptest.NewServer(f.srv.LocalHandler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403 for foreign Origin, got %d", resp.StatusCode)
	}
}
