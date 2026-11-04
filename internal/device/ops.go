package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/draw"

	"github.com/a27259954/adbridge/internal/adb"
)

// Shell runs cmd on the device's active transport.
func (m *Manager) Shell(ctx context.Context, id, cmd string) (Device, adb.ShellResult, error) {
	d, err := m.Online(id)
	if err != nil {
		return d, adb.ShellResult{}, err
	}
	res, err := m.ADB.Shell(ctx, d.Active, cmd)
	return d, res, err
}

// sh runs cmd and returns stdout, failing on a non-zero exit.
func (m *Manager) sh(ctx context.Context, serial, cmd string) (string, error) {
	res, err := m.ADB.Shell(ctx, serial, cmd)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = out
		}
		return out, fmt.Errorf("exit %d: %s", res.ExitCode, msg)
	}
	return out, nil
}

// Info is a detailed snapshot for AI consumption.
type Info struct {
	Device
	Resolution     string `json:"resolution"`
	Density        string `json:"density"`
	ForegroundApp  string `json:"foregroundApp"`
	ScreenOn       bool   `json:"screenOn"`
	ABI            string `json:"abi"`
	BuildFinger    string `json:"buildFingerprint"`
	SecurityPatch  string `json:"securityPatch"`
	WifiIP         string `json:"wifiIp"`
	DebuggableHint string `json:"debuggable"`
}

var (
	wmSizeRe    = regexp.MustCompile(`(?:Override|Physical) size: (\d+x\d+)`)
	wmDensityRe = regexp.MustCompile(`(?:Override|Physical) density: (\d+)`)
	resumedRe   = regexp.MustCompile(`(?:topResumedActivity|mResumedActivity|ResumedActivity)[:=].*?\s(\S+/\S+)`)
)

// Info gathers device details in a single shell round trip.
func (m *Manager) Info(ctx context.Context, id string) (Info, error) {
	d, err := m.Online(id)
	if err != nil {
		return Info{Device: d}, err
	}
	const sep = "\n__SEP__\n"
	script := strings.Join([]string{
		"wm size", "wm density",
		"dumpsys activity activities | grep -m2 -E 'topResumedActivity|mResumedActivity|ResumedActivity'",
		"dumpsys power | grep -m1 -E 'mWakefulness|Display Power'",
		"getprop ro.product.cpu.abi", "getprop ro.build.fingerprint",
		"getprop ro.build.version.security_patch", "ip -f inet addr show wlan0",
	}, "; printf '"+strings.ReplaceAll(sep, "\n", `\n`)+"'; ")
	res, err := m.ADB.Shell(ctx, d.Active, script)
	if err != nil {
		return Info{Device: d}, err
	}
	parts := strings.Split(string(res.Stdout), sep)
	for len(parts) < 8 {
		parts = append(parts, "")
	}
	info := Info{Device: d}
	if mt := wmSizeRe.FindAllStringSubmatch(parts[0], -1); mt != nil {
		info.Resolution = mt[len(mt)-1][1]
	}
	if mt := wmDensityRe.FindAllStringSubmatch(parts[1], -1); mt != nil {
		info.Density = mt[len(mt)-1][1]
	}
	if mt := resumedRe.FindStringSubmatch(parts[2]); mt != nil {
		info.ForegroundApp = strings.TrimSuffix(mt[1], "}")
	}
	info.ScreenOn = strings.Contains(parts[3], "Awake") || strings.Contains(parts[3], "=ON")
	info.ABI = strings.TrimSpace(parts[4])
	info.BuildFinger = strings.TrimSpace(parts[5])
	info.SecurityPatch = strings.TrimSpace(parts[6])
	if mt := inetRe.FindStringSubmatch(parts[7]); mt != nil {
		info.WifiIP = mt[1]
	}
	return info, nil
}

// Screenshot captures the screen. maxSide > 0 downsizes the longer edge;
// quality 1..100 re-encodes as JPEG (0 keeps PNG).
func (m *Manager) Screenshot(ctx context.Context, id string, maxSide, quality int) (data []byte, mime string, w, h int, err error) {
	d, err := m.Online(id)
	if err != nil {
		return nil, "", 0, 0, err
	}
	raw, err := m.ADB.Exec(ctx, d.Active, "screencap -p")
	if err != nil {
		return nil, "", 0, 0, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("decode screencap: %w (is the screen secure/locked?)", err)
	}
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	if maxSide > 0 && max(w, h) > maxSide {
		scale := float64(maxSide) / float64(max(w, h))
		nw, nh := int(float64(w)*scale), int(float64(h)*scale)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if quality > 0 {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: min(quality, 100)})
		mime = "image/jpeg"
	} else {
		err = png.Encode(&buf, img)
		mime = "image/png"
	}
	return buf.Bytes(), mime, w, h, err
}

// ---- input -------------------------------------------------------------------

func (m *Manager) Tap(ctx context.Context, id string, x, y int) error {
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	_, err = m.sh(ctx, d.Active, fmt.Sprintf("input tap %d %d", x, y))
	return err
}

func (m *Manager) Swipe(ctx context.Context, id string, x1, y1, x2, y2, ms int) error {
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	if ms <= 0 {
		ms = 300
	}
	_, err = m.sh(ctx, d.Active, fmt.Sprintf("input swipe %d %d %d %d %d", x1, y1, x2, y2, ms))
	return err
}

// ErrNonASCII is returned by InputText for text `input text` cannot type.
var ErrNonASCII = errors.New("`input text` only supports ASCII; for other text use the clipboard or an IME such as ADBKeyboard")

func (m *Manager) InputText(ctx context.Context, id, text string) error {
	for _, r := range text {
		if r > 0x7e || (r < 0x20 && r != '\n') {
			return ErrNonASCII
		}
	}
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	// `input text` treats %s as a space; real spaces split arguments.
	esc := strings.ReplaceAll(text, "%", `\%`)
	esc = strings.ReplaceAll(esc, " ", "%s")
	_, err = m.sh(ctx, d.Active, "input text "+adb.Quote(esc))
	return err
}

var keyRe = regexp.MustCompile(`^[A-Z0-9_]+$`)

// KeyEvent sends a key by name ("BACK", "KEYCODE_HOME") or number.
func (m *Manager) KeyEvent(ctx context.Context, id, key string) error {
	k := strings.ToUpper(strings.TrimSpace(key))
	if _, err := strconv.Atoi(k); err != nil {
		if !keyRe.MatchString(k) {
			return fmt.Errorf("invalid key %q", key)
		}
		if !strings.HasPrefix(k, "KEYCODE_") {
			k = "KEYCODE_" + k
		}
	}
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	_, err = m.sh(ctx, d.Active, "input keyevent "+k)
	return err
}

// ---- apps --------------------------------------------------------------------

var pkgRe = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
var activityRe = regexp.MustCompile(`^[A-Za-z0-9_.$/]+$`)

func checkPkg(p string) error {
	if !pkgRe.MatchString(p) {
		return fmt.Errorf("invalid package name %q", p)
	}
	return nil
}

// StartApp launches a package's launcher activity, or a specific activity.
func (m *Manager) StartApp(ctx context.Context, id, pkg, activity string) (string, error) {
	if err := checkPkg(pkg); err != nil {
		return "", err
	}
	d, err := m.Online(id)
	if err != nil {
		return "", err
	}
	if activity != "" {
		if !activityRe.MatchString(activity) {
			return "", fmt.Errorf("invalid activity %q", activity)
		}
		if !strings.Contains(activity, "/") {
			activity = pkg + "/" + activity
		}
		return m.sh(ctx, d.Active, "am start -W -n "+activity)
	}
	return m.sh(ctx, d.Active, "monkey -p "+pkg+" -c android.intent.category.LAUNCHER 1")
}

func (m *Manager) StopApp(ctx context.Context, id, pkg string) error {
	if err := checkPkg(pkg); err != nil {
		return err
	}
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	_, err = m.sh(ctx, d.Active, "am force-stop "+pkg)
	return err
}

func (m *Manager) ClearApp(ctx context.Context, id, pkg string) error {
	if err := checkPkg(pkg); err != nil {
		return err
	}
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	_, err = m.sh(ctx, d.Active, "pm clear "+pkg)
	return err
}

// Packages lists installed packages, optionally only third-party ones.
func (m *Manager) Packages(ctx context.Context, id, filter string, thirdParty bool) ([]string, error) {
	d, err := m.Online(id)
	if err != nil {
		return nil, err
	}
	cmd := "pm list packages"
	if thirdParty {
		cmd += " -3"
	}
	out, err := m.sh(ctx, d.Active, cmd)
	if err != nil {
		return nil, err
	}
	var pkgs []string
	f := strings.ToLower(filter)
	for _, line := range strings.Split(out, "\n") {
		p := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "package:"))
		if p != "" && (f == "" || strings.Contains(strings.ToLower(p), f)) {
			pkgs = append(pkgs, p)
		}
	}
	return pkgs, nil
}

// Install installs a local APK on the device.
func (m *Manager) Install(ctx context.Context, id, apkPath string) (string, error) {
	d, err := m.Online(id)
	if err != nil {
		return "", err
	}
	return m.ADB.Install(ctx, d.Active, apkPath)
}
