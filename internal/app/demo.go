//go:build demo

// Demo mode for screenshots and UI work without real phones:
//
//	wails3 task build EXTRA_TAGS=demo
//
// It runs adbridge against an in-process fake adb server with three phones
// (one on USB + Wi-Fi, one on Wi-Fi, one found over mDNS), a live logcat
// stream and a recorded AI session in the activity log. All state lives in a
// temporary folder; real settings, devices and logs are never touched.
// ADBRIDGE_LANG picks the UI language (default en).
package app

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a27259954/adbridge/internal/adb/adbtest"
	"github.com/a27259954/adbridge/internal/audit"
	"github.com/a27259954/adbridge/internal/config"
)

func init() { demo = &demoPhones{} }

type demoPhone struct {
	id, model, maker, android, sdk string
	battery                        int
	alias                          string
}

var phones = map[string]demoPhone{
	"192.168.1.23:41235":                         {"3A281FDJH00ABC", "Pixel 8 Pro", "Google", "15", "35", 82, "qa-pixel"},
	"R5CW30ABCD":                                 {"R5CW30ABCD", "SM-S918B", "samsung", "14", "34", 64, "s23-main"},
	"192.168.1.42:37185":                         {"R5CW30ABCD", "SM-S918B", "samsung", "14", "34", 64, "s23-main"},
	"adb-7b3c9d21-Xk2Lq9._adb-tls-connect._tcp": {"7b3c9d21", "Xiaomi 14", "Xiaomi", "14", "34", 17, ""},
}

const demoDevices = "R5CW30ABCD             device usb:1-4 product:dm3qzhx model:SM_S918B device:dm3q transport_id:1\n" +
	"192.168.1.42:37185     device product:dm3qzhx model:SM_S918B device:dm3q transport_id:2\n" +
	"192.168.1.23:41235     device product:husky model:Pixel_8_Pro device:husky transport_id:3\n" +
	"adb-7b3c9d21-Xk2Lq9._adb-tls-connect._tcp device product:houji model:23127PN0CG device:houji transport_id:4\n"

type demoPhones struct {
	srv *adbtest.Server

	mu   sync.Mutex
	logs map[string]*demoLog // by device id
}

func (d *demoPhones) setup() (string, storage, error) {
	dir, err := os.MkdirTemp("", "adbridge-demo-*")
	if err != nil {
		return "", storage{}, err
	}
	cfg := config.Default()
	cfg.MCP.RemoteLevel = config.LevelFull // the recorded session uploads and installs a build
	cfg.Language = os.Getenv("ADBRIDGE_LANG")
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		return "", storage{}, err
	}
	if d.srv, err = adbtest.Start(); err != nil {
		return "", storage{}, err
	}
	d.logs = map[string]*demoLog{}
	d.srv.OnShell(d.shell)
	d.srv.SetDevices(demoDevices)
	return d.srv.Addr(), storage{
		config:  filepath.Join(dir, "config.json"),
		uploads: filepath.Join(dir, "uploads"),
		webview: filepath.Join(dir, "webview"),
	}, nil
}

func (d *demoPhones) shell(serial, cmd string) (string, string, int) {
	p, ok := phones[serial]
	if !ok {
		return "", "device not found", 1
	}
	switch {
	case strings.HasPrefix(cmd, "getprop"):
		return fmt.Sprintf("[ro.serialno]: [%s]\n[ro.product.model]: [%s]\n[ro.product.manufacturer]: [%s]\n"+
			"[ro.build.version.release]: [%s]\n[ro.build.version.sdk]: [%s]\n__BATT__\n  level: %d\n",
			p.id, p.model, p.maker, p.android, p.sdk, p.battery), "", 0
	case strings.HasPrefix(cmd, "dumpsys battery"):
		return fmt.Sprintf("  level: %d\n", p.battery), "", 0
	case strings.HasPrefix(cmd, "logcat"):
		return d.logFor(p.id).next(), "", 0
	}
	return "", "", 0
}

func (d *demoPhones) logFor(id string) *demoLog {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := d.logs[id]
	if l == nil {
		l = &demoLog{rnd: rand.New(rand.NewSource(int64(len(id)) * 7919)), crash: id == "3A281FDJH00ABC"}
		d.logs[id] = l
	}
	return l
}

// start names the phones and replays an AI debugging session into the log.
func (d *demoPhones) start(c *Core) {
	for i := 0; i < 100; i++ {
		ready := 0
		for _, dev := range c.Devices.List() {
			if dev.Model != "" && dev.SDK > 0 {
				ready++
			}
		}
		if ready >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, p := range phones {
		if p.alias != "" {
			_ = c.Devices.SetAlias(p.id, p.alias)
		}
	}
	_ = c.Devices.SetAIEnabled("7b3c9d21", false) // show the per-device AI switch at work
	now := time.Now()
	at := func(sec int) time.Time { return now.Add(time.Duration(-sec) * time.Second) }
	remote := func(sec int, tool, args, summary string, ms int64) audit.Entry {
		return audit.Entry{Time: at(sec), Origin: "remote", RemoteAddr: "34.82.17.204", Client: "claude-ai", Tool: tool, Device: "qa-pixel", Args: args, DurationMs: ms, OK: true, Summary: summary}
	}
	for _, e := range []audit.Entry{
		{Time: at(212), Origin: "local", RemoteAddr: "127.0.0.1", Client: "claude-code", Tool: "list_devices", Args: "{}", DurationMs: 2, OK: true, Summary: "3 devices online"},
		remote(190, "logcat_clear", `{"device":"qa-pixel"}`, "logcat cleared on qa-pixel", 140),
		remote(176, "upload_file", `{"name":"app-debug.apk","data_base64":"<5592408 chars>"}`, `{"upload_id":"up_4f1c9e2a7b30d5e1","size":4194304}`, 610),
		remote(171, "upload_file", `{"upload_id":"up_4f1c9e2a7b30d5e1","data_base64":"<2961240 chars>","final":true}`, `{"complete":true,"size":6415234}`, 402),
		remote(164, "install_apk", `{"upload_id":"up_4f1c9e2a7b30d5e1"}`, "Performing Streamed Install", 5230),
		remote(150, "app_start", `{"package":"com.example.shop"}`, "started com.example.shop", 820),
		remote(141, "ui_tree", "{}", "41 elements on qa-pixel", 960),
		remote(133, "tap", `{"element":17}`, `tapped [17] TextView "Profile" at (918,2205)`, 210),
		remote(129, "screenshot", `{"count":3,"interval_ms":300}`, "qa-pixel screen 1344x2992 px. Image scaled by 1/2.337", 2140),
		{Time: at(118), Origin: "remote", RemoteAddr: "34.82.17.204", Client: "claude-ai", Tool: "device_info", Args: `{"device":"Xiaomi 14"}`, DurationMs: 0,
			Error: `device "Xiaomi 14" is not enabled for AI access in adbridge`},
		remote(97, "crash_log", "{}", "--------- beginning of crash", 180),
		remote(88, "logcat", `{"package":"com.example.shop","level":"W"}`, "9 lines from qa-pixel (oldest first)", 4),
		remote(61, "pull_file", `{"remote_path":"/data/local/tmp/anr.txt"}`, "qa-pixel:/data/local/tmp/anr.txt 18342 bytes, returned [0,18342)", 330),
		{Time: at(40), Origin: "local", RemoteAddr: "127.0.0.1", Client: "claude-code", Tool: "device_info", Device: "s23-main", Args: `{"device":"s23-main"}`, DurationMs: 260, OK: true, Summary: `{"id":"R5CW30ABCD","model":"SM-S918B","android":"14"}`},
		remote(12, "screenrecord", `{"seconds":5}`, "recorded 1840 KB from qa-pixel", 6420),
	} {
		c.Audit.Add(e)
	}
}

// demoLog produces a plausible threadtime stream: a backlog on the first
// call, then a few new lines per poll.
type demoLog struct {
	mu    sync.Mutex
	rnd   *rand.Rand
	crash bool
	last  time.Time
}

type logLine struct{ lvl, tag, msg string }

var background = []logLine{
	{"I", "ActivityManager", "Start proc 23841:com.google.android.gms.persistent/u0a172 for service {com.google.android.gms/.chimera.PersistentDirectBootAwareApiService}"},
	{"D", "WifiStateMachine", "RSSI changed: -54 dBm, link speed 866 Mbps"},
	{"D", "BufferQueueProducer", "[SurfaceView - com.example.shop/com.example.shop.MainActivity#0](id:5d2b00000012) queueBuffer: slot 2"},
	{"V", "InputDispatcher", "Delivering touch to (pid 12345): action: 0x1, window: com.example.shop/MainActivity"},
	{"I", "chatty", "uid=1000(system) Binder:1543_4 identical 3 lines"},
	{"D", "OkHttp", "--> GET https://api.example.com/v2/products?page=2"},
	{"D", "OkHttp", "<-- 200 OK https://api.example.com/v2/products?page=2 (182ms, 24.1kB body)"},
	{"I", "Choreographer", "Skipped 31 frames!  The application may be doing too much work on its main thread."},
	{"W", "System.err", "java.net.SocketTimeoutException: timeout"},
	{"D", "ConnectivityService", "NetworkAgentInfo [WIFI () - 101] validation passed"},
	{"I", "adbd", "adb wifi: authenticated connection from 192.168.1.10"},
	{"D", "PowerManagerService", "Acquiring suspend blocker \"PowerManagerService.Display\""},
	{"W", "ResourcesManager", "Asset path '/product/overlay/GmsConfigOverlay.apk' does not exist or contains no resources"},
	{"I", "WindowManager", "Relayout Window{8c1f2a7 u0 com.example.shop/com.example.shop.MainActivity}: viewVisibility=0"},
	{"D", "ShopRepository", "cart synced: 3 items, total=129.90"},
}

var crash = []logLine{
	{"I", "ActivityManager", "Displayed com.example.shop/.ui.ProfileActivity: +412ms"},
	{"D", "ProfileViewModel", "loadUser(id=null) from cache"},
	{"E", "AndroidRuntime", "FATAL EXCEPTION: main"},
	{"E", "AndroidRuntime", "Process: com.example.shop, PID: 12345"},
	{"E", "AndroidRuntime", "java.lang.NullPointerException: Attempt to invoke virtual method 'java.lang.String com.example.shop.model.User.getName()' on a null object reference"},
	{"E", "AndroidRuntime", "\tat com.example.shop.ui.ProfileFragment.onViewCreated(ProfileFragment.kt:58)"},
	{"E", "AndroidRuntime", "\tat androidx.fragment.app.Fragment.performViewCreated(Fragment.java:3128)"},
	{"F", "libc", "Fatal signal 9 (SIGKILL), code 0 (SI_USER) in tid 12345 (com.example.shop)"},
	{"I", "ActivityManager", "Process com.example.shop (pid 12345) has died: fg  TOP"},
	{"W", "ActivityTaskManager", "Force finishing activity com.example.shop/.ui.ProfileActivity"},
}

func (l *demoLog) next() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	var b strings.Builder
	write := func(t time.Time, ln logLine) {
		pid := 12345
		if ln.tag == "ActivityManager" || ln.tag == "WindowManager" || ln.tag == "PowerManagerService" {
			pid = 1543
		}
		fmt.Fprintf(&b, "%s %5d %5d %s %-8s: %s\n", t.Format("01-02 15:04:05.000"), pid, pid+l.rnd.Intn(40), ln.lvl, ln.tag, ln.msg)
	}
	if l.last.IsZero() {
		start := now.Add(-90 * time.Second)
		for i := 0; i < 260; i++ {
			write(start.Add(time.Duration(i)*330*time.Millisecond), background[l.rnd.Intn(len(background))])
		}
		if l.crash {
			t := now.Add(-4 * time.Second)
			for i, ln := range crash {
				write(t.Add(time.Duration(i)*3*time.Millisecond), ln)
			}
		}
		l.last = now
		return b.String()
	}
	for i := 0; i < l.rnd.Intn(3); i++ {
		write(now.Add(time.Duration(i)*time.Millisecond), background[l.rnd.Intn(len(background))])
	}
	l.last = now
	return b.String()
}
