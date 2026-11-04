package device

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/adb/adbtest"
)

func TestParseLogLine(t *testing.T) {
	e, ok := ParseLogLine("11-04 14:02:11.203  1234  1250 E MyApp   : java.lang.NullPointerException: boom")
	if !ok || e.PID != 1234 || e.TID != 1250 || e.Level != "E" || e.Tag != "MyApp" || e.Msg != "java.lang.NullPointerException: boom" {
		t.Fatalf("got %+v ok=%v", e, ok)
	}
	if _, ok := ParseLogLine("--------- beginning of main"); ok {
		t.Fatal("banner should not parse")
	}
}

func TestRingWrapAndSince(t *testing.T) {
	r := NewRing(3)
	for i := 0; i < 5; i++ {
		r.Add(LogEntry{Msg: string(rune('a' + i))})
	}
	all := r.Since(0, 0)
	if len(all) != 3 || all[0].Msg != "c" || all[2].Msg != "e" || all[2].Seq != 5 {
		t.Fatalf("got %+v", all)
	}
	if got := r.Since(4, 0); len(got) != 1 || got[0].Msg != "e" {
		t.Fatalf("since 4: %+v", got)
	}
	if got := r.Since(5, 0); got != nil {
		t.Fatalf("since 5: %+v", got)
	}
}

func TestRingQuery(t *testing.T) {
	r := NewRing(100)
	r.Add(LogEntry{Level: "D", Tag: "Net", Msg: "ok"})
	r.Add(LogEntry{Level: "E", Tag: "MyApp", Msg: "crash here", PID: 7})
	r.Add(LogEntry{Level: "W", Tag: "MyApp", Msg: "slow"})
	got := r.Query(LogFilter{MinLevel: "W", Tag: "myapp"})
	if len(got) != 2 || got[0].Msg != "crash here" {
		t.Fatalf("got %+v", got)
	}
	got = r.Query(LogFilter{Grep: regexp.MustCompile(`(?i)CRASH`)})
	if len(got) != 1 || got[0].PID != 7 {
		t.Fatalf("grep got %+v", got)
	}
}

func TestDeviceID(t *testing.T) {
	if id := DeviceID(map[string]string{"ro.serialno": "R5CW"}, "1.2.3.4:5555"); id != "R5CW" {
		t.Fatal(id)
	}
	if id := DeviceID(map[string]string{"ro.serialno": "unknown"}, "1.2.3.4:5555"); id != "1.2.3.4:5555" {
		t.Fatal(id)
	}
}

func TestParseUITree(t *testing.T) {
	dump := `UI hierchary dumped to: /dev/tty<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0"><node index="0" text="" class="android.widget.FrameLayout" bounds="[0,0][1080,2400]">
<node text="Log in" resource-id="com.x:id/login" class="android.widget.Button" clickable="true" enabled="true" bounds="[100,1800][980,1900]"/>
<node text="" class="android.view.View" bounds="[0,0][10,10]"/>
</node></hierarchy>`
	nodes, err := ParseUITree(dump)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Text != "Log in" || nodes[0].X != 540 || nodes[0].Y != 1850 {
		t.Fatalf("got %+v", nodes)
	}
	if s := FormatUITree(nodes); !strings.Contains(s, `[0] Button "Log in" id=login (540,1850) click`) {
		t.Fatalf("format: %s", s)
	}
}

// TestMergeTransports checks that one phone on USB and Wi-Fi becomes one
// device that prefers Wi-Fi.
func TestMergeTransports(t *testing.T) {
	srv := adbtest.New(t)
	srv.OnShell(func(serial, cmd string) (string, string, int) {
		if strings.HasPrefix(cmd, "getprop") {
			return "[ro.serialno]: [R5CW30ABCD]\n[ro.product.model]: [SM-S918B]\n[ro.build.version.sdk]: [34]\n__BATT__\n  level: 77\n", "", 0
		}
		return "", "", 0
	})
	srv.SetDevices("R5CW30ABCD device usb:1-4 model:SM_S918B transport_id:1\n192.168.1.23:5555 device model:SM_S918B transport_id:2\n")

	m, err := New(&adb.Client{Addr: srv.Addr()}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ds := m.List()
		if len(ds) == 1 && ds[0].ID == "R5CW30ABCD" && len(ds[0].Transports) == 2 {
			d := ds[0]
			if d.ActiveKind != "wifi" || d.Active != "192.168.1.23:5555" || d.Battery != 77 || d.SDK != 34 {
				t.Fatalf("unexpected merge: %+v", d)
			}
			if !d.Wireless || d.LastAddr != "192.168.1.23:5555" {
				t.Fatalf("wireless not remembered: %+v", d)
			}
			got, err := m.Resolve("192.168.1.23:5555")
			if err != nil || got.ID != d.ID {
				t.Fatalf("resolve by serial: %v %v", got.ID, err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("devices never merged: %+v", m.List())
}
