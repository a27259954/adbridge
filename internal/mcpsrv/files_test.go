package mcpsrv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func localSession(t *testing.T, f *fixture) *mcp.ClientSession {
	ts := httptest.NewServer(f.srv.LocalHandler())
	t.Cleanup(ts.Close)
	return connect(t, ts.URL+"/mcp", nil)
}

func TestUploadPushInstallFlow(t *testing.T) {
	f := newFixture(t)
	cs := localSession(t, f)
	payload := bytes.Repeat([]byte{0xCA, 0xFE, 0x00, 0x01}, 50_000) // 200 KB binary

	half := len(payload) / 2
	res := callTool(t, cs, "upload_file", map[string]any{"name": "app.apk", "data_base64": base64.StdEncoding.EncodeToString(payload[:half])})
	if res.IsError {
		t.Fatalf("upload chunk 1: %s", firstText(res))
	}
	var it struct {
		ID       string `json:"upload_id"`
		Complete bool   `json:"complete"`
		Size     int64  `json:"size"`
	}
	json.Unmarshal([]byte(firstText(res)), &it)
	res = callTool(t, cs, "upload_file", map[string]any{"upload_id": it.ID, "data_base64": base64.StdEncoding.EncodeToString(payload[half:]), "final": true})
	json.Unmarshal([]byte(firstText(res)), &it)
	if res.IsError || !it.Complete || it.Size != int64(len(payload)) {
		t.Fatalf("upload chunk 2: %s", firstText(res))
	}

	res = callTool(t, cs, "push_file", map[string]any{"upload_id": it.ID, "remote_path": "/data/local/tmp/app.apk"})
	if res.IsError {
		t.Fatalf("push_file: %s", firstText(res))
	}
	if got, _ := f.fake.File("/data/local/tmp/app.apk"); !bytes.Equal(got, payload) {
		t.Fatalf("device received %d bytes, want %d", len(got), len(payload))
	}

	// Inline push and a relative path rejection.
	res = callTool(t, cs, "push_file", map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte("{}")), "remote_path": "/sdcard/a.json"})
	if res.IsError {
		t.Fatalf("inline push: %s", firstText(res))
	}
	if res = callTool(t, cs, "push_file", map[string]any{"data_base64": "e30=", "remote_path": "sdcard/a.json"}); !res.IsError {
		t.Fatal("relative remote path accepted")
	}

	// The audit log must not contain the base64 payload.
	for _, e := range f.audit.Since(0) {
		if len(e.Args) > 600 {
			t.Fatalf("audit args not shortened for %s: %d chars", e.Tool, len(e.Args))
		}
	}
}

func TestPullFileTextBinaryAndPaging(t *testing.T) {
	f := newFixture(t)
	cs := localSession(t, f)
	f.fake.SetFile("/sdcard/log.txt", []byte("line one\nline two\n"))
	f.fake.SetFile("/sdcard/db.bin", []byte{0, 1, 2, 3, 4, 5})

	res := callTool(t, cs, "pull_file", map[string]any{"remote_path": "/sdcard/log.txt"})
	if out := firstText(res); res.IsError || !strings.Contains(out, "line two") || !strings.Contains(out, "18 bytes") {
		t.Fatalf("text pull: %s", out)
	}

	res = callTool(t, cs, "pull_file", map[string]any{"remote_path": "/sdcard/log.txt", "offset": 5, "length": 3})
	if out := firstText(res); !strings.HasSuffix(out, "\none") || !strings.Contains(out, "next offset 8") {
		t.Fatalf("paged pull: %q", out)
	}

	res = callTool(t, cs, "pull_file", map[string]any{"remote_path": "/sdcard/db.bin"})
	var blob []byte
	for _, c := range res.Content {
		if er, ok := c.(*mcp.EmbeddedResource); ok {
			blob = er.Resource.Blob
		}
	}
	if res.IsError || !bytes.Equal(blob, []byte{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("binary pull: %s blob=%v", firstText(res), blob)
	}

	if res = callTool(t, cs, "pull_file", map[string]any{"remote_path": "/sdcard/missing"}); !res.IsError {
		t.Fatal("missing file should error")
	}
}

func TestDeviceHTTP(t *testing.T) {
	f := newFixture(t)
	cs := localSession(t, f)
	f.fake.OnSocket("localabstract:chrome_devtools_remote", func(conn net.Conn) {
		defer conn.Close()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		body := `[{"type":"page","url":"` + req.URL.Path + `"}]`
		resp := http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": {"application/json"}},
			ContentLength: int64(len(body)), Body: ioNopCloser(body)}
		resp.Write(conn)
	})
	res := callTool(t, cs, "device_http", map[string]any{"socket": "localabstract:chrome_devtools_remote", "path": "/json"})
	if out := firstText(res); res.IsError || !strings.Contains(out, "HTTP 200") || !strings.Contains(out, `"url":"/json"`) {
		t.Fatalf("device_http: %s", out)
	}
	if res = callTool(t, cs, "device_http", map[string]any{"socket": "tcp:80;reboot"}); !res.IsError {
		t.Fatal("invalid socket accepted")
	}
}

func TestScreenshotBurstAndScreenrecord(t *testing.T) {
	f := newFixture(t)
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 40, 80)))
	f.fake.OnShell(func(serial, cmd string) (string, string, int) {
		switch {
		case strings.HasPrefix(cmd, "getprop"):
			return "[ro.serialno]: [PIX8]\n[ro.product.model]: [Pixel 8]\n__BATT__\nlevel: 90\n", "", 0
		case cmd == "screencap -p":
			return pngBuf.String(), "", 0
		case strings.HasPrefix(cmd, "screenrecord"):
			f.fake.SetFile("/data/local/tmp/adbridge-rec.mp4", []byte("fake-mp4"))
			return "", "", 0
		}
		return "", "", 0
	})
	cs := localSession(t, f)

	res := callTool(t, cs, "screenshot", map[string]any{"count": 3, "interval_ms": 10})
	images := 0
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			images++
		}
	}
	if res.IsError || images != 3 {
		t.Fatalf("burst: %d images, %s", images, firstText(res))
	}

	res = callTool(t, cs, "screenrecord", map[string]any{"seconds": 1})
	var video []byte
	for _, c := range res.Content {
		if er, ok := c.(*mcp.EmbeddedResource); ok && er.Resource.MIMEType == "video/mp4" {
			video = er.Resource.Blob
		}
	}
	if res.IsError || string(video) != "fake-mp4" {
		t.Fatalf("screenrecord: %s", firstText(res))
	}
	if entries, _ := os.ReadDir(f.recs); len(entries) != 1 {
		t.Fatalf("recording not saved on the PC: %d files", len(entries))
	}
	if res = callTool(t, cs, "screenrecord", map[string]any{"size": "big; reboot"}); !res.IsError {
		t.Fatal("invalid size accepted")
	}
}

func TestFileToolsNeedFullRemotely(t *testing.T) {
	f := newFixture(t)
	ts := httptest.NewServer(f.srv.RemoteHandler())
	defer ts.Close()
	cs := connect(t, ts.URL+"/mcp/"+f.cfg.Get().MCP.Token, nil)
	args := map[string]map[string]any{
		"upload_file":  {"data_base64": "e30="},
		"push_file":    {"remote_path": "/x", "data_base64": "e30="},
		"pull_file":    {"remote_path": "/x"},
		"port_forward": {"action": "list"},
	}
	for tool, a := range args {
		res := callTool(t, cs, tool, a)
		if !res.IsError || !strings.Contains(firstText(res), `requires permission level "full"`) {
			t.Fatalf("%s should be denied at remote read level: %s", tool, firstText(res))
		}
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func ioNopCloser(s string) nopCloser { return nopCloser{strings.NewReader(s)} }
