package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
)

func checkRemotePath(p string) error {
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("remote path must be absolute: %q", p)
	}
	return nil
}

// PushFile copies a local file to the device.
func (m *Manager) PushFile(ctx context.Context, id, local, remote string, perm os.FileMode) (int64, error) {
	f, err := os.Open(local)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return m.PushReader(ctx, id, f, remote, perm)
}

// PushReader copies r to remote on the device.
func (m *Manager) PushReader(ctx context.Context, id string, r io.Reader, remote string, perm os.FileMode) (int64, error) {
	if err := checkRemotePath(remote); err != nil {
		return 0, err
	}
	d, err := m.Online(id)
	if err != nil {
		return 0, err
	}
	return m.ADB.Push(ctx, d.Active, r, remote, perm)
}

// StatFile returns info for a remote path.
func (m *Manager) StatFile(ctx context.Context, id, remote string) (adb.FileInfo, error) {
	if err := checkRemotePath(remote); err != nil {
		return adb.FileInfo{}, err
	}
	d, err := m.Online(id)
	if err != nil {
		return adb.FileInfo{}, err
	}
	return m.ADB.Stat(ctx, d.Active, remote)
}

// PullFile reads a remote file of at most limit bytes.
func (m *Manager) PullFile(ctx context.Context, id, remote string, limit int64) ([]byte, error) {
	if err := checkRemotePath(remote); err != nil {
		return nil, err
	}
	d, err := m.Online(id)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	_, err = m.ADB.Pull(ctx, d.Active, remote, &buf, limit)
	return buf.Bytes(), err
}

var sizeRe = regexp.MustCompile(`^\d{2,4}x\d{2,4}$`)

const recPath = "/data/local/tmp/adbridge-rec.mp4"

// ScreenRecord records the screen for seconds (1..180) and returns the MP4.
func (m *Manager) ScreenRecord(ctx context.Context, id string, seconds int, size string, bitrate int) ([]byte, error) {
	if seconds <= 0 {
		seconds = 5
	}
	if seconds > 180 {
		return nil, errors.New("screenrecord supports at most 180 seconds")
	}
	if bitrate <= 0 {
		bitrate = 2_000_000
	}
	cmd := fmt.Sprintf("screenrecord --time-limit %d --bit-rate %d", seconds, bitrate)
	if size != "" {
		if !sizeRe.MatchString(size) {
			return nil, fmt.Errorf("invalid size %q (want WIDTHxHEIGHT)", size)
		}
		cmd += " --size " + size
	}
	d, err := m.Online(id)
	if err != nil {
		return nil, err
	}
	if _, err := m.sh(ctx, d.Active, cmd+" "+recPath); err != nil {
		return nil, fmt.Errorf("screenrecord: %w", err)
	}
	defer m.ADB.Shell(context.Background(), d.Active, "rm -f "+recPath)
	var buf bytes.Buffer
	if _, err := m.ADB.Pull(ctx, d.Active, recPath, &buf, 256<<20); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- port forwarding ---------------------------------------------------------

func (m *Manager) AddForward(ctx context.Context, id, local, remote string) (string, error) {
	d, err := m.Online(id)
	if err != nil {
		return "", err
	}
	return m.ADB.AddForward(ctx, d.Active, local, remote)
}

func (m *Manager) RemoveForward(ctx context.Context, id, local string) error {
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	return m.ADB.RemoveForward(ctx, d.Active, local)
}

func (m *Manager) AddReverse(ctx context.Context, id, remote, local string) error {
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	return m.ADB.AddReverse(ctx, d.Active, remote, local)
}

func (m *Manager) RemoveReverse(ctx context.Context, id, remote string) error {
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	return m.ADB.RemoveReverse(ctx, d.Active, remote)
}

// Forwards lists forward and reverse rules for a device.
func (m *Manager) Forwards(ctx context.Context, id string) (fwd, rev []adb.Forward, err error) {
	d, err := m.Online(id)
	if err != nil {
		return nil, nil, err
	}
	if fwd, err = m.ADB.ListForwards(ctx, d.Active); err != nil {
		return nil, nil, err
	}
	rev, err = m.ADB.ListReverses(ctx, d.Active)
	return fwd, rev, err
}

// ---- HTTP to a device socket -------------------------------------------------

// HTTPResponse is the result of DeviceHTTP.
type HTTPResponse struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"-"`
}

// DeviceHTTP sends an HTTP request to a socket on the device (e.g.
// "tcp:8080" or "localabstract:chrome_devtools_remote") through the adb
// transport. No PC port is opened, so it works for remote MCP clients too.
func (m *Manager) DeviceHTTP(ctx context.Context, id, spec, method, path string, headers map[string]string, body []byte, maxBody int64) (HTTPResponse, error) {
	if !adb.ValidSocketSpec(spec) {
		return HTTPResponse{}, fmt.Errorf("invalid socket %q", spec)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	d, err := m.Online(id)
	if err != nil {
		return HTTPResponse{}, err
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return m.ADB.Dial(ctx, d.Active, spec)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	defer tr.CloseIdleConnections()
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), "http://localhost"+path, bytes.NewReader(body))
	if err != nil {
		return HTTPResponse{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return HTTPResponse{Status: resp.StatusCode, Headers: resp.Header, Body: b}, err
}
