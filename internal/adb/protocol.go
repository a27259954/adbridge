// Package adb is a thin client for the adb server.
//
// Hot paths (device tracking, shell, exec-out) speak the adb host protocol
// directly over tcp:5037, which avoids spawning processes and sidesteps
// "adb server version doesn't match this client" restarts when other tools
// (Android Studio, scrcpy) ship their own adb.exe. Rare operations (pair,
// connect, mdns) shell out to the user's adb.exe.
//
// Host protocol reference: AOSP packages/modules/adb/SERVICES.TXT, protocol.txt.
package adb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// DefaultAddr is where the adb server listens.
const DefaultAddr = "127.0.0.1:5037"

// ServerError is a FAIL reply from the adb server.
type ServerError struct{ Msg string }

func (e *ServerError) Error() string { return "adb: " + e.Msg }

// IsServerError reports whether err is a FAIL reply.
func IsServerError(err error) bool {
	var se *ServerError
	return errors.As(err, &se)
}

func writeRequest(w io.Writer, req string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(req), req)
	return err
}

// readStatus consumes OKAY or FAIL<len><msg>.
func readStatus(r io.Reader) error {
	var st [4]byte
	if _, err := io.ReadFull(r, st[:]); err != nil {
		return fmt.Errorf("adb: read status: %w", err)
	}
	switch string(st[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		msg, err := readLenPrefixed(r)
		if err != nil {
			return fmt.Errorf("adb: read failure message: %w", err)
		}
		return &ServerError{Msg: msg}
	default:
		return fmt.Errorf("adb: unexpected status %q", st[:])
	}
}

// readLenPrefixed reads a 4-hex-digit length followed by that many bytes.
func readLenPrefixed(r io.Reader) (string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(hdr[:]), 16, 32)
	if err != nil {
		return "", fmt.Errorf("adb: bad length %q", hdr[:])
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// bindContext closes conn when ctx is cancelled. Call the returned func
// to detach once the conn is no longer tied to ctx.
func bindContext(ctx context.Context, conn net.Conn) func() bool {
	return context.AfterFunc(ctx, func() { _ = conn.Close() })
}

// ctxErr prefers the context error over the "use of closed connection"
// error produced when bindContext closes the socket.
func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
