package adb_test

import (
	"context"
	"testing"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/adb/adbtest"
)

const twoDevices = "R5CW30ABCD             device usb:1-4 product:dm3qzhx model:SM_S918B device:dm3q transport_id:3\n" +
	"192.168.1.23:41235     device product:husky model:Pixel_8_Pro device:husky transport_id:5\n"

func newClient(t *testing.T) (*adb.Client, *adbtest.Server) {
	srv := adbtest.New(t)
	return &adb.Client{Addr: srv.Addr()}, srv
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestServerVersion(t *testing.T) {
	c, _ := newClient(t)
	v, err := c.ServerVersion(ctx(t))
	if err != nil || v != 41 {
		t.Fatalf("got %d, %v; want 41", v, err)
	}
}

func TestDevices(t *testing.T) {
	c, srv := newClient(t)
	srv.SetDevices(twoDevices)
	ds, err := c.Devices(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 {
		t.Fatalf("want 2 devices, got %d", len(ds))
	}
	if ds[0].Model != "SM_S918B" || ds[0].Wireless() || ds[0].USB != "1-4" {
		t.Errorf("usb device parsed wrong: %+v", ds[0])
	}
	if !ds[1].Wireless() || !ds[1].Online() || ds[1].TransportID != "5" {
		t.Errorf("wifi device parsed wrong: %+v", ds[1])
	}
}

func TestTrackDevices(t *testing.T) {
	c, srv := newClient(t)
	ch, err := c.TrackDevices(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if first := <-ch; len(first) != 0 {
		t.Fatalf("want empty initial list, got %v", first)
	}
	srv.SetDevices(twoDevices)
	if next := <-ch; len(next) != 2 {
		t.Fatalf("want 2 devices after update, got %v", next)
	}
}

func TestShellV2(t *testing.T) {
	c, srv := newClient(t)
	srv.OnShell(func(serial, cmd string) (string, string, int) {
		if serial != "abc" || cmd != "getprop ro.build.version.sdk" {
			return "", "bad", 1
		}
		return "35\n", "", 0
	})
	res, err := c.Shell(ctx(t), "abc", "getprop ro.build.version.sdk")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "35\n" || res.ExitCode != 0 {
		t.Fatalf("got %q rc=%d", res.Stdout, res.ExitCode)
	}
}

func TestShellExitCodeAndStderr(t *testing.T) {
	c, srv := newClient(t)
	srv.OnShell(func(string, string) (string, string, int) { return "", "not found\n", 127 })
	res, err := c.Shell(ctx(t), "abc", "nope")
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 127 || string(res.Stderr) != "not found\n" {
		t.Fatalf("got rc=%d stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestLegacyShellFallback(t *testing.T) {
	c, srv := newClient(t)
	srv.DisableShellV2()
	srv.OnShell(func(_, cmd string) (string, string, int) { return "hello\nworld", "", 3 })
	res, err := c.Shell(ctx(t), "abc", "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "hello\nworld" || res.ExitCode != 3 {
		t.Fatalf("got %q rc=%d", res.Stdout, res.ExitCode)
	}
}

func TestExec(t *testing.T) {
	c, srv := newClient(t)
	srv.OnShell(func(_, cmd string) (string, string, int) { return "\x89PNG\r\n", "", 0 })
	out, err := c.Exec(ctx(t), "abc", "screencap -p")
	if err != nil || string(out) != "\x89PNG\r\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestTransportFailure(t *testing.T) {
	c, _ := newClient(t)
	_, err := c.Host(ctx(t), "host:nope")
	if !adb.IsServerError(err) {
		t.Fatalf("want ServerError, got %v", err)
	}
}

func TestParseMDNS(t *testing.T) {
	out := "List of discovered mdns services\n" +
		"adb-R5CW30ABCD-AbCdEf\t_adb-tls-connect._tcp\t192.168.1.23:41235\n" +
		"studio-x1y2\t_adb-tls-pairing._tcp.\t192.168.1.23:37011\n"
	got := adb.ParseMDNS(out)
	if len(got) != 2 || got[1].Type != adb.MDNSPairing || got[0].Addr != "192.168.1.23:41235" {
		t.Fatalf("got %+v", got)
	}
}

func TestQuote(t *testing.T) {
	if got := adb.Quote("it's"); got != `'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}
