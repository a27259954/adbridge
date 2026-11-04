package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/audit"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/device"
)

const deviceDesc = "Device id, alias or serial. Optional when exactly one AI-enabled device is online."

// call carries per-invocation context into tool bodies.
type call struct {
	dev    device.Device
	origin string
	max    int
}

type toolSpec struct {
	name, title, desc string
	level             config.Level
	readOnly          bool
	destructive       bool
	timeout           time.Duration
	noDevice          bool
}

// add registers a tool behind permission checks, device resolution,
// timeouts, output limits and auditing.
func add[In any](s *Server, spec toolSpec, fn func(ctx context.Context, c *call, in In) (*mcp.CallToolResult, error)) {
	destructive := spec.destructive
	t := &mcp.Tool{
		Name:        spec.name,
		Title:       spec.title,
		Description: fmt.Sprintf("%s\n\nPermission level: %s.", spec.desc, spec.level),
		Annotations: &mcp.ToolAnnotations{
			Title:           spec.title,
			ReadOnlyHint:    spec.readOnly,
			DestructiveHint: &destructive,
		},
	}
	if spec.timeout == 0 {
		spec.timeout = 30 * time.Second
	}
	mcp.AddTool(s.mcp, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		origin, peerAddr := OriginLocal, ""
		if req.Extra != nil && req.Extra.Header != nil {
			if o := req.Extra.Header.Get(hdrOrigin); o != "" {
				origin = o
			}
			peerAddr = req.Extra.Header.Get(hdrPeer)
		}
		argsJSON, _ := json.Marshal(in)
		var devArg struct {
			Device string `json:"device"`
		}
		_ = json.Unmarshal(argsJSON, &devArg)

		entry := audit.Entry{Origin: origin, RemoteAddr: peerAddr, Tool: spec.name, Args: auditArgs(argsJSON), Client: clientName(req)}
		finish := func(res *mcp.CallToolResult, err error, denied bool) (*mcp.CallToolResult, any, error) {
			entry.DurationMs = time.Since(start).Milliseconds()
			entry.Denied = denied
			if err != nil {
				entry.Error = err.Error()
				res = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}}}
			} else {
				entry.OK = true
				entry.Summary = summarize(res)
			}
			s.d.Audit.Add(entry)
			return res, nil, nil
		}

		cfg := s.d.Config.Get().MCP
		level := cfg.LocalLevel
		if origin == OriginRemote {
			level = cfg.RemoteLevel
		}
		if level.Rank() < spec.level.Rank() {
			return finish(nil, fmt.Errorf("%s requires permission level %q but the %s level is %q. Ask the user to raise it in adbridge (MCP panel)", spec.name, spec.level, origin, level), true)
		}

		c := &call{origin: origin, max: cfg.MaxOutputChars}
		if !spec.noDevice {
			d, err := s.resolve(devArg.Device)
			if err != nil {
				return finish(nil, err, false)
			}
			c.dev = d
			entry.Device = d.Name
		}
		ctx, cancel := context.WithTimeout(ctx, spec.timeout)
		defer cancel()
		res, err := fn(ctx, c, in)
		if res != nil {
			clip(res, c.max)
		}
		return finish(res, err, false)
	})
}

// auditArgs keeps the audit log readable: long string values (base64
// chunks, request bodies) are replaced by their length.
func auditArgs(raw []byte) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	for k, v := range m {
		if s, ok := v.(string); ok && len(s) > 256 {
			m[k] = fmt.Sprintf("<%d chars>", len(s))
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func clientName(req *mcp.CallToolRequest) string {
	if req.Session == nil {
		return ""
	}
	if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
		return p.ClientInfo.Name
	}
	return ""
}

// resolve picks a device the AI is allowed to use.
func (s *Server) resolve(q string) (device.Device, error) {
	if strings.TrimSpace(q) == "" {
		var ok []device.Device
		for _, d := range s.d.Devices.List() {
			if d.AIEnabled && d.State == device.StateOnline {
				ok = append(ok, d)
			}
		}
		switch len(ok) {
		case 1:
			return ok[0], nil
		case 0:
			return device.Device{}, errors.New("no AI-enabled device is online; call list_devices")
		default:
			names := make([]string, len(ok))
			for i, d := range ok {
				names[i] = d.Name + " (" + d.ID + ")"
			}
			return device.Device{}, fmt.Errorf("several devices are online, pass device: %s", strings.Join(names, ", "))
		}
	}
	d, err := s.d.Devices.Resolve(q)
	if err != nil {
		return d, err
	}
	if !d.AIEnabled {
		return device.Device{}, fmt.Errorf("device %q is not enabled for AI access in adbridge", q)
	}
	if d.State != device.StateOnline {
		return d, fmt.Errorf("device %s is %s", d.Name, d.State)
	}
	return d, nil
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func jsonResult(v any) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(v, "", " ")
	return text(string(b))
}

func clip(res *mcp.CallToolResult, max int) {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok && len(t.Text) > max {
			t.Text = t.Text[len(t.Text)-max:]
			t.Text = fmt.Sprintf("[output truncated to the last %d chars]\n", max) + t.Text
		}
	}
}

func summarize(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			line, _, _ := strings.Cut(strings.TrimSpace(v.Text), "\n")
			return line
		case *mcp.ImageContent:
			return fmt.Sprintf("%s, %d KB", v.MIMEType, len(v.Data)/1024)
		}
	}
	return ""
}

// ---- tool inputs --------------------------------------------------------------

type DeviceIn struct {
	Device string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
}

type LogcatIn struct {
	Device       string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Level        string `json:"level,omitempty" jsonschema:"Minimum level: V, D, I, W, E or F"`
	Tag          string `json:"tag,omitempty" jsonschema:"Case-insensitive substring of the log tag"`
	Package      string `json:"package,omitempty" jsonschema:"Only lines from this app's running processes"`
	Grep         string `json:"grep,omitempty" jsonschema:"Case-insensitive regular expression matched against tag and message"`
	SinceSeconds int    `json:"since_seconds,omitempty" jsonschema:"Only lines received in the last N seconds"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Max lines returned, newest kept (default 200, max 2000)"`
}

type CrashIn struct {
	Device string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Lines  int    `json:"lines,omitempty" jsonschema:"Lines from the crash buffer (default 300)"`
}

type ScreenshotIn struct {
	Device     string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	MaxSize    int    `json:"max_size,omitempty" jsonschema:"Longest edge in pixels after scaling (default 1280, 0 = keep the default)"`
	Format     string `json:"format,omitempty" jsonschema:"jpeg (default, smaller) or png"`
	Count      int    `json:"count,omitempty" jsonschema:"Number of frames for a burst (1-8, default 1) to observe animations or flicker"`
	IntervalMs int    `json:"interval_ms,omitempty" jsonschema:"Delay between burst frames in ms (default 500); each capture also takes a few hundred ms"`
}

type TapIn struct {
	Device  string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Element *int   `json:"element,omitempty" jsonschema:"Element index from the last ui_tree call (preferred)"`
	X       *int   `json:"x,omitempty" jsonschema:"X in device pixels (when element is not given)"`
	Y       *int   `json:"y,omitempty" jsonschema:"Y in device pixels (when element is not given)"`
}

type SwipeIn struct {
	Device     string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	X1         int    `json:"x1"`
	Y1         int    `json:"y1"`
	X2         int    `json:"x2"`
	Y2         int    `json:"y2"`
	DurationMs int    `json:"duration_ms,omitempty" jsonschema:"Default 300"`
}

type TextIn struct {
	Device string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Text   string `json:"text" jsonschema:"ASCII text to type into the focused field"`
}

type KeyIn struct {
	Device string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Key    string `json:"key" jsonschema:"Key name (BACK, HOME, ENTER, APP_SWITCH, DEL, POWER, VOLUME_UP...) or keycode number"`
}

type AppIn struct {
	Device   string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Package  string `json:"package" jsonschema:"Application id, e.g. com.example.app"`
	Activity string `json:"activity,omitempty" jsonschema:"Optional activity (.MainActivity or full component) for app_start"`
}

type PackagesIn struct {
	Device     string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Filter     string `json:"filter,omitempty" jsonschema:"Substring filter"`
	ThirdParty bool   `json:"third_party,omitempty" jsonschema:"Only user-installed apps"`
}

type InstallIn struct {
	Device   string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	UploadID string `json:"upload_id,omitempty" jsonschema:"Completed upload from upload_file (the usual way for remote clients)"`
	URL      string `json:"url,omitempty" jsonschema:"http(s) URL of an APK to download and install"`
	Path     string `json:"path,omitempty" jsonschema:"Path of an APK on the PC running adbridge"`
}

type DumpsysIn struct {
	Device  string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Service string `json:"service" jsonschema:"dumpsys service, e.g. activity, meminfo, battery, window, package"`
	Args    string `json:"args,omitempty" jsonschema:"Extra arguments, space separated (e.g. a package name for meminfo)"`
}

type ShellIn struct {
	Device         string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Command        string `json:"command" jsonschema:"Command for /system/bin/sh on the device"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"Default 30, max 300"`
}

type NoIn struct{}

// ---- tools --------------------------------------------------------------------

var (
	svcRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	argRe = regexp.MustCompile(`^[A-Za-z0-9_.,:=/\-]+$`)
)

func (s *Server) registerTools() {
	m := s.d.Devices

	add(s, toolSpec{name: "list_devices", title: "List devices", level: config.LevelRead, readOnly: true, noDevice: true,
		desc: "List Android devices adbridge knows about that the user has enabled for AI access, with state, connection type, Android version and battery."},
		func(ctx context.Context, c *call, _ NoIn) (*mcp.CallToolResult, error) {
			type row struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				Model      string `json:"model"`
				Android    string `json:"android"`
				SDK        int    `json:"sdk"`
				State      string `json:"state"`
				Connection string `json:"connection"`
				Battery    int    `json:"battery"`
			}
			rows := []row{}
			for _, d := range m.List() {
				if d.AIEnabled {
					rows = append(rows, row{d.ID, d.Name, d.Model, d.Android, d.SDK, d.State, d.ActiveKind, d.Battery})
				}
			}
			return jsonResult(rows), nil
		})

	add(s, toolSpec{name: "device_info", title: "Device info", level: config.LevelRead, readOnly: true,
		desc: "Detailed info: model, Android/SDK, resolution, density, foreground activity, screen state, ABI, Wi-Fi IP, build fingerprint."},
		func(ctx context.Context, c *call, _ DeviceIn) (*mcp.CallToolResult, error) {
			info, err := m.Info(ctx, c.dev.ID)
			if err != nil {
				return nil, err
			}
			return jsonResult(info), nil
		})

	add(s, toolSpec{name: "logcat", title: "Query logcat", level: config.LevelRead, readOnly: true,
		desc: "Query the device's logcat. adbridge streams logcat continuously into a buffer, so this returns instantly. Filter by level, tag, package, regex and time window."},
		func(ctx context.Context, c *call, in LogcatIn) (*mcp.CallToolResult, error) {
			ring := m.Logcat(c.dev.ID)
			if ring == nil {
				return text("No log lines captured yet for " + c.dev.Name), nil
			}
			f := device.LogFilter{MinLevel: in.Level, Tag: in.Tag, Limit: min(max(in.Limit, 0), 2000)}
			if in.Grep != "" {
				re, err := regexp.Compile("(?i)" + in.Grep)
				if err != nil {
					return nil, fmt.Errorf("bad grep regex: %w", err)
				}
				f.Grep = re
			}
			if in.SinceSeconds > 0 {
				f.Since = time.Now().Add(-time.Duration(in.SinceSeconds) * time.Second)
			}
			if in.Package != "" {
				if !svcRe.MatchString(in.Package) {
					return nil, fmt.Errorf("invalid package %q", in.Package)
				}
				_, res, err := m.Shell(ctx, c.dev.ID, "pidof "+in.Package)
				if err != nil {
					return nil, err
				}
				for _, p := range strings.Fields(string(res.Stdout)) {
					if n, err := strconv.Atoi(p); err == nil {
						f.PIDs = append(f.PIDs, n)
					}
				}
				if len(f.PIDs) == 0 {
					return text(in.Package + " is not running (no pid). Start it with app_start, or query without package."), nil
				}
			}
			entries := ring.Query(f)
			return text(fmt.Sprintf("%d lines from %s (oldest first)\n%s", len(entries), c.dev.Name, device.Format(entries))), nil
		})

	add(s, toolSpec{name: "logcat_clear", title: "Clear logcat", level: config.LevelInteract,
		desc: "Clear the device's logcat buffers (useful before reproducing a bug)."},
		func(ctx context.Context, c *call, _ DeviceIn) (*mcp.CallToolResult, error) {
			if err := m.ClearLogcat(ctx, c.dev.ID); err != nil {
				return nil, err
			}
			return text("logcat cleared on " + c.dev.Name), nil
		})

	add(s, toolSpec{name: "crash_log", title: "Crash log", level: config.LevelRead, readOnly: true,
		desc: "Read the logcat crash buffer (Java/native crash stack traces)."},
		func(ctx context.Context, c *call, in CrashIn) (*mcp.CallToolResult, error) {
			n := in.Lines
			if n <= 0 {
				n = 300
			}
			_, res, err := m.Shell(ctx, c.dev.ID, fmt.Sprintf("logcat -b crash -d -v threadtime -t %d", min(n, 5000)))
			if err != nil {
				return nil, err
			}
			out := strings.TrimSpace(string(res.Stdout))
			if out == "" {
				out = "crash buffer is empty"
			}
			return text(out), nil
		})

	add(s, toolSpec{name: "screenshot", title: "Screenshot", level: config.LevelRead, readOnly: true, timeout: 90 * time.Second,
		desc: "Capture the screen. Returned scaled down (JPEG by default) to save tokens; the text part gives the factor to convert image coordinates back to device pixels for tap/swipe. Set count (up to 8) and interval_ms for a burst of frames to observe animations, transitions or flicker."},
		func(ctx context.Context, c *call, in ScreenshotIn) (*mcp.CallToolResult, error) {
			size := in.MaxSize
			if size <= 0 {
				size = 1280
			}
			q := 70
			if strings.EqualFold(in.Format, "png") {
				q = 0
			}
			count := min(max(in.Count, 1), 8)
			interval := time.Duration(in.IntervalMs) * time.Millisecond
			if in.IntervalMs <= 0 {
				interval = 500 * time.Millisecond
			}
			res := &mcp.CallToolResult{}
			start := time.Now()
			var w, h int
			for i := 0; i < count; i++ {
				if i > 0 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(interval):
					}
				}
				data, mime, fw, fh, err := m.Screenshot(ctx, c.dev.ID, size, q)
				if err != nil {
					return nil, err
				}
				w, h = fw, fh
				if count > 1 {
					res.Content = append(res.Content, &mcp.TextContent{Text: fmt.Sprintf("frame %d at +%dms", i+1, time.Since(start).Milliseconds())})
				}
				res.Content = append(res.Content, &mcp.ImageContent{Data: data, MIMEType: mime})
			}
			scale := 1.0
			if max(w, h) > size {
				scale = float64(max(w, h)) / float64(size)
			}
			note := fmt.Sprintf("%s screen %dx%d px. Image scaled by 1/%.3f: multiply image coordinates by %.3f to get device pixels.", c.dev.Name, w, h, scale, scale)
			res.Content = append([]mcp.Content{&mcp.TextContent{Text: note}}, res.Content...)
			return res, nil
		})

	add(s, toolSpec{name: "ui_tree", title: "UI tree", level: config.LevelRead, readOnly: true,
		desc: "Dump the current screen's UI hierarchy as a numbered list of meaningful elements (text, description, resource id, center point, flags). Use the number with tap {element}."},
		func(ctx context.Context, c *call, _ DeviceIn) (*mcp.CallToolResult, error) {
			nodes, err := m.UITree(ctx, c.dev.ID)
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("%d elements on %s\n%s", len(nodes), c.dev.Name, device.FormatUITree(nodes))), nil
		})

	add(s, toolSpec{name: "tap", title: "Tap", level: config.LevelInteract,
		desc: "Tap an element from the last ui_tree (element) or a point in device pixels (x, y)."},
		func(ctx context.Context, c *call, in TapIn) (*mcp.CallToolResult, error) {
			if in.Element != nil {
				n, err := m.TapElement(ctx, c.dev.ID, *in.Element)
				if err != nil {
					return nil, err
				}
				return text(fmt.Sprintf("tapped [%d] %s %q at (%d,%d)", n.Index, n.Class, n.Text+n.Desc, n.X, n.Y)), nil
			}
			if in.X == nil || in.Y == nil {
				return nil, errors.New("pass element, or both x and y")
			}
			if err := m.Tap(ctx, c.dev.ID, *in.X, *in.Y); err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("tapped (%d,%d)", *in.X, *in.Y)), nil
		})

	add(s, toolSpec{name: "swipe", title: "Swipe", level: config.LevelInteract,
		desc: "Swipe between two points in device pixels (also used for scrolling and long-press when both points are equal with a long duration)."},
		func(ctx context.Context, c *call, in SwipeIn) (*mcp.CallToolResult, error) {
			if err := m.Swipe(ctx, c.dev.ID, in.X1, in.Y1, in.X2, in.Y2, in.DurationMs); err != nil {
				return nil, err
			}
			return text("swiped"), nil
		})

	add(s, toolSpec{name: "input_text", title: "Type text", level: config.LevelInteract,
		desc: "Type ASCII text into the focused input field."},
		func(ctx context.Context, c *call, in TextIn) (*mcp.CallToolResult, error) {
			if err := m.InputText(ctx, c.dev.ID, in.Text); err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("typed %d chars", len(in.Text))), nil
		})

	add(s, toolSpec{name: "key", title: "Key event", level: config.LevelInteract,
		desc: "Send a key event such as BACK, HOME, ENTER, APP_SWITCH or DEL."},
		func(ctx context.Context, c *call, in KeyIn) (*mcp.CallToolResult, error) {
			if err := m.KeyEvent(ctx, c.dev.ID, in.Key); err != nil {
				return nil, err
			}
			return text("sent " + in.Key), nil
		})

	add(s, toolSpec{name: "app_start", title: "Start app", level: config.LevelInteract,
		desc: "Launch an app by package (launcher activity) or a specific activity."},
		func(ctx context.Context, c *call, in AppIn) (*mcp.CallToolResult, error) {
			out, err := m.StartApp(ctx, c.dev.ID, in.Package, in.Activity)
			if err != nil {
				return nil, err
			}
			return text("started " + in.Package + "\n" + out), nil
		})

	add(s, toolSpec{name: "app_stop", title: "Force-stop app", level: config.LevelInteract,
		desc: "Force-stop an app."},
		func(ctx context.Context, c *call, in AppIn) (*mcp.CallToolResult, error) {
			if err := m.StopApp(ctx, c.dev.ID, in.Package); err != nil {
				return nil, err
			}
			return text("stopped " + in.Package), nil
		})

	add(s, toolSpec{name: "app_clear", title: "Clear app data", level: config.LevelFull, destructive: true,
		desc: "Clear all data of an app (pm clear). Irreversible."},
		func(ctx context.Context, c *call, in AppIn) (*mcp.CallToolResult, error) {
			if err := m.ClearApp(ctx, c.dev.ID, in.Package); err != nil {
				return nil, err
			}
			return text("cleared data of " + in.Package), nil
		})

	add(s, toolSpec{name: "list_packages", title: "List packages", level: config.LevelRead, readOnly: true,
		desc: "List installed packages, optionally only third-party apps or matching a filter."},
		func(ctx context.Context, c *call, in PackagesIn) (*mcp.CallToolResult, error) {
			pkgs, err := m.Packages(ctx, c.dev.ID, in.Filter, in.ThirdParty)
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("%d packages\n%s", len(pkgs), strings.Join(pkgs, "\n"))), nil
		})

	add(s, toolSpec{name: "dumpsys", title: "dumpsys", level: config.LevelRead, readOnly: true,
		desc: "Run dumpsys for one service (activity, meminfo <pkg>, gfxinfo <pkg>, battery, window, package <pkg>...)."},
		func(ctx context.Context, c *call, in DumpsysIn) (*mcp.CallToolResult, error) {
			if !svcRe.MatchString(in.Service) {
				return nil, fmt.Errorf("invalid service %q", in.Service)
			}
			cmd := "dumpsys " + in.Service
			for _, a := range strings.Fields(in.Args) {
				if !argRe.MatchString(a) {
					return nil, fmt.Errorf("invalid argument %q", a)
				}
				cmd += " " + adb.Quote(a)
			}
			_, res, err := m.Shell(ctx, c.dev.ID, cmd)
			if err != nil {
				return nil, err
			}
			return text(string(res.Stdout) + string(res.Stderr)), nil
		})

	add(s, toolSpec{name: "install_apk", title: "Install APK", level: config.LevelFull, destructive: true, timeout: 5 * time.Minute,
		desc: "Install (or update, keeping data) an APK from an upload (upload_file → upload_id), a URL, or a path on the PC."},
		func(ctx context.Context, c *call, in InstallIn) (*mcp.CallToolResult, error) {
			path := in.Path
			if in.UploadID != "" {
				if s.d.Staging == nil {
					return nil, errors.New("staging is not available")
				}
				_, p, err := s.d.Staging.Get(in.UploadID)
				if err != nil {
					return nil, err
				}
				path = p
			} else if in.URL != "" {
				p, cleanup, err := download(ctx, in.URL)
				if err != nil {
					return nil, err
				}
				defer cleanup()
				path = p
			}
			if path == "" {
				return nil, errors.New("pass url or path")
			}
			out, err := m.Install(ctx, c.dev.ID, path)
			if err != nil {
				return nil, err
			}
			return text(out), nil
		})

	add(s, toolSpec{name: "shell", title: "Shell", level: config.LevelFull, destructive: true, timeout: 5 * time.Minute,
		desc: "Run an arbitrary shell command on the device. Returns stdout, stderr and exit code."},
		func(ctx context.Context, c *call, in ShellIn) (*mcp.CallToolResult, error) {
			t := in.TimeoutSeconds
			if t <= 0 {
				t = 30
			}
			ctx, cancel := context.WithTimeout(ctx, time.Duration(min(t, 300))*time.Second)
			defer cancel()
			_, res, err := m.Shell(ctx, c.dev.ID, in.Command)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "exit %d\n", res.ExitCode)
			b.Write(res.Stdout)
			if len(res.Stderr) > 0 {
				b.WriteString("\n[stderr]\n")
				b.Write(res.Stderr)
			}
			return text(b.String()), nil
		})
}

const maxAPK = 1 << 30

func download(ctx context.Context, url string) (string, func(), error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", nil, errors.New("url must be http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp("", "adbridge-*.apk")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxAPK+1))
	f.Close()
	if err == nil && n > maxAPK {
		err = errors.New("APK larger than 1 GB")
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return filepath.Clean(f.Name()), cleanup, nil
}
