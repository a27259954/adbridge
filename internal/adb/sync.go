package adb

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

// File transfer over the adb sync protocol ("sync:" service).
// Every message is a 4-byte id plus a little-endian uint32 length/argument.
// Reference: AOSP packages/modules/adb/SYNC.TXT.

const syncMaxChunk = 64 * 1024

// FileInfo is the result of Stat.
type FileInfo struct {
	Mode  os.FileMode
	Size  int64
	MTime time.Time
}

// Exists reports whether Stat found the path.
func (f FileInfo) Exists() bool { return f.Mode != 0 }

type syncConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *Client) openSync(ctx context.Context, serial string) (*syncConn, func(), error) {
	conn, err := c.openService(ctx, serial, "sync:")
	if err != nil {
		return nil, nil, err
	}
	stop := bindContext(ctx, conn)
	sc := &syncConn{Conn: conn, r: bufio.NewReader(conn)}
	return sc, func() {
		_ = sc.send("QUIT", 0, nil)
		stop()
		conn.Close()
	}, nil
}

func (s *syncConn) send(id string, n uint32, payload []byte) error {
	hdr := make([]byte, 8, 8+len(payload))
	copy(hdr, id)
	binary.LittleEndian.PutUint32(hdr[4:], n)
	_, err := s.Write(append(hdr, payload...))
	return err
}

func (s *syncConn) sendPath(id, path string) error {
	return s.send(id, uint32(len(path)), []byte(path))
}

func (s *syncConn) readHeader() (string, uint32, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
		return "", 0, err
	}
	return string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:]), nil
}

func (s *syncConn) readFail(n uint32) error {
	msg := make([]byte, n)
	if _, err := io.ReadFull(s.r, msg); err != nil {
		return err
	}
	return &ServerError{Msg: string(msg)}
}

// Stat returns mode, size and mtime of a remote path (Mode 0 = missing).
// Sizes above 4 GiB are truncated by the legacy STAT message.
func (c *Client) Stat(ctx context.Context, serial, path string) (FileInfo, error) {
	s, done, err := c.openSync(ctx, serial)
	if err != nil {
		return FileInfo{}, err
	}
	defer done()
	if err := s.sendPath("STAT", path); err != nil {
		return FileInfo{}, ctxErr(ctx, err)
	}
	var resp [16]byte
	if _, err := io.ReadFull(s.r, resp[:]); err != nil {
		return FileInfo{}, ctxErr(ctx, err)
	}
	if string(resp[:4]) != "STAT" {
		return FileInfo{}, fmt.Errorf("adb sync: unexpected %q", resp[:4])
	}
	return FileInfo{
		Mode:  os.FileMode(binary.LittleEndian.Uint32(resp[4:8])),
		Size:  int64(binary.LittleEndian.Uint32(resp[8:12])),
		MTime: time.Unix(int64(binary.LittleEndian.Uint32(resp[12:16])), 0),
	}, nil
}

// Push writes r to path on the device with the given permission bits.
func (c *Client) Push(ctx context.Context, serial string, r io.Reader, path string, perm os.FileMode) (int64, error) {
	s, done, err := c.openSync(ctx, serial)
	if err != nil {
		return 0, err
	}
	defer done()
	if perm == 0 {
		perm = 0o644
	}
	// Mode is the full st_mode: regular file | permission bits.
	spec := path + "," + strconv.FormatUint(uint64(0o100000|perm.Perm()), 10)
	if err := s.sendPath("SEND", spec); err != nil {
		return 0, ctxErr(ctx, err)
	}
	buf := make([]byte, syncMaxChunk)
	var total int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if err := s.send("DATA", uint32(n), buf[:n]); err != nil {
				return total, ctxErr(ctx, err)
			}
			total += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return total, rerr
		}
	}
	if err := s.send("DONE", uint32(time.Now().Unix()), nil); err != nil {
		return total, ctxErr(ctx, err)
	}
	id, n, err := s.readHeader()
	if err != nil {
		return total, ctxErr(ctx, err)
	}
	switch id {
	case "OKAY":
		return total, nil
	case "FAIL":
		return total, s.readFail(n)
	}
	return total, fmt.Errorf("adb sync: unexpected %q", id)
}

// ErrTooLarge is returned by Pull when the file exceeds the caller's limit.
var ErrTooLarge = errors.New("file exceeds size limit")

// Pull streams path from the device into w, reading at most limit bytes
// (limit <= 0 means no limit).
func (c *Client) Pull(ctx context.Context, serial, path string, w io.Writer, limit int64) (int64, error) {
	s, done, err := c.openSync(ctx, serial)
	if err != nil {
		return 0, err
	}
	defer done()
	if err := s.sendPath("RECV", path); err != nil {
		return 0, ctxErr(ctx, err)
	}
	var total int64
	for {
		id, n, err := s.readHeader()
		if err != nil {
			return total, ctxErr(ctx, err)
		}
		switch id {
		case "DATA":
			if limit > 0 && total+int64(n) > limit {
				return total, ErrTooLarge
			}
			m, err := io.CopyN(w, s.r, int64(n))
			total += m
			if err != nil {
				return total, ctxErr(ctx, err)
			}
		case "DONE":
			return total, nil
		case "FAIL":
			return total, s.readFail(n)
		default:
			return total, fmt.Errorf("adb sync: unexpected %q", id)
		}
	}
}
