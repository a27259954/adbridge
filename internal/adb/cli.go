package adb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// run executes the adb binary. Output is combined and trimmed.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	bin := c.Bin()
	if bin == "" {
		return "", ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// Openscreen mDNS works on Windows without Bonjour; required for
	// wireless pairing discovery on older platform-tools.
	cmd.Env = append(os.Environ(), "ADB_MDNS_OPENSCREEN=1")
	HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			return "", err
		}
		return s, errors.New(s)
	}
	return s, nil
}

// Connect runs `adb connect addr`. adb exits 0 even on failure, so the
// output is inspected.
func (c *Client) Connect(ctx context.Context, addr string) error {
	out, err := c.run(ctx, "connect", addr)
	if err != nil {
		return err
	}
	lo := strings.ToLower(out)
	if strings.Contains(lo, "connected to") && !strings.Contains(lo, "cannot") && !strings.Contains(lo, "failed") {
		return nil
	}
	return errors.New(out)
}

// Disconnect runs `adb disconnect addr`.
func (c *Client) Disconnect(ctx context.Context, addr string) error {
	out, err := c.run(ctx, "disconnect", addr)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(out), "error") {
		return errors.New(out)
	}
	return nil
}

// Pair runs `adb pair addr code` (Android 11+ wireless debugging).
func (c *Client) Pair(ctx context.Context, addr, code string) error {
	out, err := c.run(ctx, "pair", addr, code)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Successfully paired") {
		return errors.New(out)
	}
	return nil
}

// TCPIP restarts adbd on serial in TCP mode (USB → Wi-Fi for any Android version).
func (c *Client) TCPIP(ctx context.Context, serial string, port int) error {
	out, err := c.run(ctx, "-s", serial, "tcpip", fmt.Sprint(port))
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(out), "error") {
		return errors.New(out)
	}
	return nil
}

// Install runs `adb -s serial install -r -t apk`.
func (c *Client) Install(ctx context.Context, serial, apk string) (string, error) {
	out, err := c.run(ctx, "-s", serial, "install", "-r", "-t", apk)
	if err != nil {
		return out, err
	}
	if !strings.Contains(out, "Success") {
		return out, errors.New(out)
	}
	return out, nil
}

// MDNSService is one row of `adb mdns services`.
type MDNSService struct {
	Name string `json:"name"` // e.g. adb-R5CW30XXXX-AbCdEf
	Type string `json:"type"` // _adb-tls-connect._tcp / _adb-tls-pairing._tcp / _adb._tcp
	Addr string `json:"addr"` // ip:port
}

const (
	MDNSConnect = "_adb-tls-connect._tcp"
	MDNSPairing = "_adb-tls-pairing._tcp"
)

// MDNSServices lists services discovered by the adb server's mDNS backend.
func (c *Client) MDNSServices(ctx context.Context) ([]MDNSService, error) {
	out, err := c.run(ctx, "mdns", "services")
	if err != nil {
		return nil, err
	}
	return ParseMDNS(out), nil
}

// ParseMDNS parses `adb mdns services` output.
func ParseMDNS(out string) []MDNSService {
	var res []MDNSService
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[1], "_adb") {
			continue
		}
		res = append(res, MDNSService{Name: f[0], Type: strings.TrimSuffix(f[1], "."), Addr: f[len(f)-1]})
	}
	return res
}
