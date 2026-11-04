package adb_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/a27259954/adbridge/internal/adb"
)

func TestPushPullStat(t *testing.T) {
	c, srv := newClient(t)
	data := bytes.Repeat([]byte("0123456789abcdef"), 10_000) // 160 KB: several DATA chunks

	n, err := c.Push(ctx(t), "abc", bytes.NewReader(data), "/sdcard/x.bin", 0o644)
	if err != nil || n != int64(len(data)) {
		t.Fatalf("push: n=%d err=%v", n, err)
	}
	if got, _ := srv.File("/sdcard/x.bin"); !bytes.Equal(got, data) {
		t.Fatalf("device got %d bytes, want %d", len(got), len(data))
	}

	st, err := c.Stat(ctx(t), "abc", "/sdcard/x.bin")
	if err != nil || !st.Exists() || st.Size != int64(len(data)) {
		t.Fatalf("stat: %+v %v", st, err)
	}
	if st, _ := c.Stat(ctx(t), "abc", "/nope"); st.Exists() {
		t.Fatal("stat of missing file reports it exists")
	}

	var out bytes.Buffer
	if _, err := c.Pull(ctx(t), "abc", "/sdcard/x.bin", &out, 0); err != nil || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("pull: %d bytes, %v", out.Len(), err)
	}
	if _, err := c.Pull(ctx(t), "abc", "/sdcard/x.bin", io.Discard, 1000); !errors.Is(err, adb.ErrTooLarge) {
		t.Fatalf("pull limit: want ErrTooLarge, got %v", err)
	}
	if _, err := c.Pull(ctx(t), "abc", "/nope", io.Discard, 0); !adb.IsServerError(err) {
		t.Fatalf("pull missing: want ServerError, got %v", err)
	}
}

func TestPushFailure(t *testing.T) {
	c, _ := newClient(t)
	_, err := c.Push(ctx(t), "abc", strings.NewReader("x"), "/readonly/f", 0)
	if !adb.IsServerError(err) || !strings.Contains(err.Error(), "Read-only") {
		t.Fatalf("want read-only failure, got %v", err)
	}
}

func TestDialDeviceSocket(t *testing.T) {
	c, srv := newClient(t)
	srv.OnSocket("tcp:8080", func(conn net.Conn) {
		line, _ := bufio.NewReader(conn).ReadString('\n')
		io.WriteString(conn, "echo:"+line)
		conn.Close()
	})
	conn, err := c.Dial(ctx(t), "abc", "tcp:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "hi\n")
	got, _ := io.ReadAll(conn)
	if string(got) != "echo:hi\n" {
		t.Fatalf("got %q", got)
	}
	if _, err := c.Dial(ctx(t), "abc", "tcp:80; rm -rf /"); err == nil {
		t.Fatal("invalid spec accepted")
	}
}

func TestSocketSpec(t *testing.T) {
	for _, ok := range []string{"tcp:8081", "localabstract:chrome_devtools_remote", "jdwp:1234", "localabstract:webview_devtools_remote_123"} {
		if !adb.ValidSocketSpec(ok) {
			t.Errorf("%s should be valid", ok)
		}
	}
	for _, bad := range []string{"tcp:", "tcp:80 x", "udp:53", "localabstract:a;b"} {
		if adb.ValidSocketSpec(bad) {
			t.Errorf("%s should be invalid", bad)
		}
	}
}
