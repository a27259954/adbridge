package adb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client talks to one adb server. The adb.exe path is used for CLI-only
// operations and for starting the server; it can change at runtime.
type Client struct {
	Addr string

	mu  sync.RWMutex
	bin string
}

// New returns a client for the default server address.
func New(bin string) *Client { return &Client{Addr: DefaultAddr, bin: bin} }

// Bin returns the adb executable path ("" if unknown).
func (c *Client) Bin() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.bin }

// SetBin changes the adb executable path.
func (c *Client) SetBin(p string) { c.mu.Lock(); c.bin = p; c.mu.Unlock() }

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	return d.DialContext(ctx, "tcp", c.Addr)
}

// Host sends a host-scoped request (e.g. "host:version") and returns the
// length-prefixed payload of the reply.
func (c *Client) Host(ctx context.Context, req string) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	defer bindContext(ctx, conn)()
	if err := writeRequest(conn, req); err != nil {
		return "", ctxErr(ctx, err)
	}
	if err := readStatus(conn); err != nil {
		return "", ctxErr(ctx, err)
	}
	s, err := readLenPrefixed(conn)
	return s, ctxErr(ctx, err)
}

// ServerVersion returns the adb server's internal protocol version (e.g. 41).
func (c *Client) ServerVersion(ctx context.Context) (int, error) {
	s, err := c.Host(ctx, "host:version")
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 16, 32)
	return int(v), err
}

// EnsureServer starts the adb server if it is not reachable.
func (c *Client) EnsureServer(ctx context.Context) error {
	if _, err := c.ServerVersion(ctx); err == nil {
		return nil
	}
	if _, err := c.run(ctx, "start-server"); err != nil {
		return fmt.Errorf("start adb server: %w", err)
	}
	_, err := c.ServerVersion(ctx)
	return err
}

// Devices returns the current device list (host:devices-l).
func (c *Client) Devices(ctx context.Context) ([]Entry, error) {
	s, err := c.Host(ctx, "host:devices-l")
	if err != nil {
		return nil, err
	}
	return ParseDevices(s), nil
}

// TrackDevices streams full device lists whenever the set or a state
// changes. The channel closes when ctx is done or the server goes away.
func (c *Client) TrackDevices(ctx context.Context) (<-chan []Entry, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	stop := bindContext(ctx, conn)
	if err := writeRequest(conn, "host:track-devices-l"); err != nil {
		stop()
		conn.Close()
		return nil, err
	}
	if err := readStatus(conn); err != nil {
		stop()
		conn.Close()
		return nil, err
	}
	ch := make(chan []Entry, 1)
	go func() {
		defer close(ch)
		defer conn.Close()
		defer stop()
		r := bufio.NewReader(conn)
		for {
			s, err := readLenPrefixed(r)
			if err != nil {
				return
			}
			select {
			case ch <- ParseDevices(s):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// openService switches a fresh connection to serial's transport and opens
// service on it. The caller owns the returned conn.
func (c *Client) openService(ctx context.Context, serial, service string) (net.Conn, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		conn.Close()
		return nil, err
	}
	if err := writeRequest(conn, "host:transport:"+serial); err != nil {
		return fail(err)
	}
	if err := readStatus(conn); err != nil {
		return fail(err)
	}
	if err := writeRequest(conn, service); err != nil {
		return fail(err)
	}
	if err := readStatus(conn); err != nil {
		return fail(err)
	}
	return conn, nil
}

// maxOutput bounds how much a single non-streaming call buffers.
const maxOutput = 64 << 20

// Exec runs cmd via exec: (raw stdout, no pty, binary safe), e.g. screencap -p.
func (c *Client) Exec(ctx context.Context, serial, cmd string) ([]byte, error) {
	conn, err := c.openService(ctx, serial, "exec:"+cmd)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	defer bindContext(ctx, conn)()
	out, err := io.ReadAll(io.LimitReader(conn, maxOutput))
	return out, ctxErr(ctx, err)
}

// ExecStream runs cmd and returns its stdout as a stream. Closing the reader
// (or cancelling ctx) terminates the remote command.
func (c *Client) ExecStream(ctx context.Context, serial, cmd string) (io.ReadCloser, error) {
	conn, err := c.openService(ctx, serial, "exec:"+cmd)
	if err != nil {
		return nil, err
	}
	stop := bindContext(ctx, conn)
	return &streamConn{Conn: conn, stop: stop}, nil
}

type streamConn struct {
	net.Conn
	stop func() bool
}

func (s *streamConn) Close() error { s.stop(); return s.Conn.Close() }

// ShellResult is the outcome of a shell command.
type ShellResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// shell protocol v2 packet ids.
const (
	v2Stdout = 1
	v2Stderr = 2
	v2Exit   = 3
)

const rcMarker = "\n__ADBRIDGE_RC="

// Shell runs cmd through /system/bin/sh. It prefers the shell v2 protocol
// (separate stderr, real exit code) and falls back to legacy shell: on
// devices that predate it (Android < 7).
func (c *Client) Shell(ctx context.Context, serial, cmd string) (ShellResult, error) {
	conn, err := c.openService(ctx, serial, "shell,v2,raw:"+cmd)
	if IsServerError(err) {
		return c.legacyShell(ctx, serial, cmd)
	}
	if err != nil {
		return ShellResult{}, err
	}
	defer conn.Close()
	defer bindContext(ctx, conn)()

	var res ShellResult
	var stdout, stderr bytes.Buffer
	r := bufio.NewReader(conn)
	hdr := make([]byte, 5)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF {
				break
			}
			return res, ctxErr(ctx, err)
		}
		n := binary.LittleEndian.Uint32(hdr[1:])
		if stdout.Len()+stderr.Len()+int(n) > maxOutput {
			return res, fmt.Errorf("adb: shell output exceeds %d bytes", maxOutput)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return res, ctxErr(ctx, err)
		}
		switch hdr[0] {
		case v2Stdout:
			stdout.Write(data)
		case v2Stderr:
			stderr.Write(data)
		case v2Exit:
			if len(data) > 0 {
				res.ExitCode = int(data[0])
			}
			res.Stdout, res.Stderr = stdout.Bytes(), stderr.Bytes()
			return res, nil
		}
	}
	res.Stdout, res.Stderr = stdout.Bytes(), stderr.Bytes()
	return res, nil
}

func (c *Client) legacyShell(ctx context.Context, serial, cmd string) (ShellResult, error) {
	conn, err := c.openService(ctx, serial, "shell:("+cmd+"); printf '"+`\n`+strings.TrimPrefix(rcMarker, "\n")+"%d' $?")
	if err != nil {
		return ShellResult{}, err
	}
	defer conn.Close()
	defer bindContext(ctx, conn)()
	out, err := io.ReadAll(io.LimitReader(conn, maxOutput))
	if err != nil {
		return ShellResult{}, ctxErr(ctx, err)
	}
	// Legacy shell runs in a pty on old devices and translates \n to \r\n.
	out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
	res := ShellResult{Stdout: out, ExitCode: -1}
	if i := bytes.LastIndex(out, []byte(rcMarker)); i >= 0 {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out[i+len(rcMarker):]))); err == nil {
			res.ExitCode = v
		}
		res.Stdout = out[:i]
	}
	return res, nil
}

// Quote single-quotes s for /system/bin/sh.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
