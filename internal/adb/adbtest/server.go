// Package adbtest provides an in-process fake adb server for tests.
//
// It implements the subset of the host protocol adbridge uses:
// host:version, host:devices-l, host:track-devices-l and, after
// host:transport:<serial>, the shell,v2 / shell / exec services.
package adbtest

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ShellFunc answers a command for serial with stdout, stderr and an exit code.
type ShellFunc func(serial, cmd string) (stdout, stderr string, code int)

type Server struct {
	ln net.Listener

	mu       sync.Mutex
	devices  string
	trackers []chan string
	shell    ShellFunc
	noV2     bool
	files    map[string][]byte         // device filesystem for sync:
	sockets  map[string]func(net.Conn) // device sockets for tcp:/localabstract:
}

// SetFile stores a file on the fake device.
func (s *Server) SetFile(path string, data []byte) {
	s.mu.Lock()
	s.files[path] = append([]byte(nil), data...)
	s.mu.Unlock()
}

// File returns a file pushed to the fake device.
func (s *Server) File(path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[path]
	return b, ok
}

// OnSocket serves a device-side socket spec such as "tcp:8080".
func (s *Server) OnSocket(spec string, fn func(net.Conn)) {
	s.mu.Lock()
	s.sockets[spec] = fn
	s.mu.Unlock()
}

// New starts a fake server on a random loopback port; it is closed via t.Cleanup.
func New(t testing.TB) *Server {
	t.Helper()
	s, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// Start starts a fake server outside tests (e.g. the demo build). Call Close when done.
func Start() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		ln:      ln,
		shell:   func(string, string) (string, string, int) { return "", "", 0 },
		files:   map[string][]byte{},
		sockets: map[string]func(net.Conn){},
	}
	go s.serve()
	return s, nil
}

// Close stops accepting connections.
func (s *Server) Close() { s.ln.Close() }

func (s *Server) Addr() string { return s.ln.Addr().String() }

// SetDevices replaces the devices-l payload and notifies trackers.
func (s *Server) SetDevices(payload string) {
	s.mu.Lock()
	s.devices = payload
	trackers := append([]chan string{}, s.trackers...)
	s.mu.Unlock()
	for _, ch := range trackers {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (s *Server) OnShell(fn ShellFunc) { s.mu.Lock(); s.shell = fn; s.mu.Unlock() }

// DisableShellV2 makes shell,v2 fail like on Android < 7.
func (s *Server) DisableShellV2() { s.mu.Lock(); s.noV2 = true; s.mu.Unlock() }

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func readReq(r io.Reader) (string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(hdr[:]), 16, 32)
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(r, buf)
	return string(buf), err
}

func okay(w io.Writer)                { io.WriteString(w, "OKAY") }
func fail(w io.Writer, msg string)    { fmt.Fprintf(w, "FAIL%04x%s", len(msg), msg) }
func payload(w io.Writer, msg string) { fmt.Fprintf(w, "%04x%s", len(msg), msg) }

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	req, err := readReq(r)
	if err != nil {
		return
	}
	switch {
	case req == "host:version":
		okay(c)
		payload(c, "0029")
	case req == "host:devices-l":
		s.mu.Lock()
		d := s.devices
		s.mu.Unlock()
		okay(c)
		payload(c, d)
	case req == "host:track-devices-l":
		ch := make(chan string, 4)
		s.mu.Lock()
		s.trackers = append(s.trackers, ch)
		d := s.devices
		s.mu.Unlock()
		okay(c)
		payload(c, d)
		for p := range ch {
			if _, err := fmt.Fprintf(c, "%04x%s", len(p), p); err != nil {
				return
			}
		}
	case strings.HasPrefix(req, "host:transport:"):
		serial := strings.TrimPrefix(req, "host:transport:")
		okay(c)
		svc, err := readReq(r)
		if err != nil {
			return
		}
		s.service(c, r, serial, svc)
	default:
		fail(c, "unknown host service")
	}
}

func (s *Server) service(c net.Conn, r *bufio.Reader, serial, svc string) {
	s.mu.Lock()
	shell, noV2 := s.shell, s.noV2
	s.mu.Unlock()
	switch {
	case strings.HasPrefix(svc, "shell,v2,raw:"):
		if noV2 {
			fail(c, "closed")
			return
		}
		okay(c)
		out, errOut, code := shell(serial, strings.TrimPrefix(svc, "shell,v2,raw:"))
		writePacket(c, 1, []byte(out))
		writePacket(c, 2, []byte(errOut))
		writePacket(c, 3, []byte{byte(code)})
	case strings.HasPrefix(svc, "shell:"):
		okay(c)
		cmd := strings.TrimPrefix(svc, "shell:")
		// Strip the exit-code trailer the client appends and emulate it.
		inner := cmd
		if i := strings.LastIndex(cmd, "); printf"); i >= 0 && strings.HasPrefix(cmd, "(") {
			inner = cmd[1:i]
		}
		out, _, code := shell(serial, inner)
		io.WriteString(c, strings.ReplaceAll(out, "\n", "\r\n"))
		fmt.Fprintf(c, "\r\n__ADBRIDGE_RC=%d", code)
	case strings.HasPrefix(svc, "exec:"):
		okay(c)
		out, _, _ := shell(serial, strings.TrimPrefix(svc, "exec:"))
		io.WriteString(c, out)
	case svc == "sync:":
		okay(c)
		s.sync(c, r)
	default:
		s.mu.Lock()
		sock := s.sockets[svc]
		s.mu.Unlock()
		if sock == nil {
			fail(c, "unknown service")
			return
		}
		okay(c)
		sock(&bufConn{Conn: c, r: r})
	}
}

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func syncMsg(w io.Writer, id string, n uint32, payload []byte) {
	hdr := make([]byte, 8)
	copy(hdr, id)
	binary.LittleEndian.PutUint32(hdr[4:], n)
	w.Write(hdr)
	w.Write(payload)
}

func syncFail(w io.Writer, msg string) { syncMsg(w, "FAIL", uint32(len(msg)), []byte(msg)) }

// sync emulates STAT / SEND / RECV against the in-memory file map.
func (s *Server) sync(c net.Conn, r *bufio.Reader) {
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		id, n := string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:])
		if id == "QUIT" {
			return
		}
		arg := make([]byte, n)
		if _, err := io.ReadFull(r, arg); err != nil {
			return
		}
		switch id {
		case "STAT":
			s.mu.Lock()
			data, ok := s.files[string(arg)]
			s.mu.Unlock()
			resp := make([]byte, 12)
			if ok {
				binary.LittleEndian.PutUint32(resp[0:], 0o100644)
				binary.LittleEndian.PutUint32(resp[4:], uint32(len(data)))
				binary.LittleEndian.PutUint32(resp[8:], 1700000000)
			}
			syncMsg(c, "STAT", binary.LittleEndian.Uint32(resp[0:4]), resp[4:])
		case "RECV":
			s.mu.Lock()
			data, ok := s.files[string(arg)]
			s.mu.Unlock()
			if !ok {
				syncFail(c, "No such file or directory")
				continue
			}
			for len(data) > 0 {
				chunk := data[:min(len(data), 64*1024)]
				data = data[len(chunk):]
				syncMsg(c, "DATA", uint32(len(chunk)), chunk)
			}
			syncMsg(c, "DONE", 0, nil)
		case "SEND":
			path, _, _ := strings.Cut(string(arg), ",")
			var buf []byte
			for {
				if _, err := io.ReadFull(r, hdr[:]); err != nil {
					return
				}
				id, n := string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:])
				if id == "DONE" {
					break
				}
				chunk := make([]byte, n)
				if _, err := io.ReadFull(r, chunk); err != nil {
					return
				}
				buf = append(buf, chunk...)
			}
			if strings.HasPrefix(path, "/readonly/") {
				syncFail(c, "Read-only file system")
				continue
			}
			s.SetFile(path, buf)
			syncMsg(c, "OKAY", 0, nil)
		default:
			syncFail(c, "unknown sync request")
		}
	}
}

func writePacket(w io.Writer, id byte, data []byte) {
	hdr := make([]byte, 5)
	hdr[0] = id
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(data)))
	w.Write(hdr)
	w.Write(data)
}
