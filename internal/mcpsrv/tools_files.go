package mcpsrv

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/a27259954/adbridge/internal/config"
)

const (
	// MaxChunkBytes is the largest decoded upload chunk. Base64 adds a third,
	// which stays below the 8 MiB request body limit.
	MaxChunkBytes = 4 << 20
	maxInlinePush = 1 << 20
	maxPullBytes  = 64 << 20
	maxPullChunk  = 4 << 20
	maxRecording  = 16 << 20
	keepRecs      = 20
)

type UploadIn struct {
	UploadID   string `json:"upload_id,omitempty" jsonschema:"Continue this upload. Omit to start a new one."`
	Name       string `json:"name,omitempty" jsonschema:"File name for a new upload, e.g. app-debug.apk"`
	DataBase64 string `json:"data_base64" jsonschema:"Base64 chunk, at most 4 MiB decoded"`
	Final      bool   `json:"final,omitempty" jsonschema:"true on the last chunk; completes the upload and returns its SHA-256"`
}

type PushIn struct {
	Device     string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	RemotePath string `json:"remote_path" jsonschema:"Absolute destination path on the device, e.g. /sdcard/Download/x.json or /data/local/tmp/x"`
	UploadID   string `json:"upload_id,omitempty" jsonschema:"Completed upload from upload_file"`
	DataBase64 string `json:"data_base64,omitempty" jsonschema:"Small inline content (at most 1 MiB decoded) instead of upload_id"`
	Mode       string `json:"mode,omitempty" jsonschema:"Octal permissions, default 644 (use 755 for executables)"`
}

type PullIn struct {
	Device     string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	RemotePath string `json:"remote_path" jsonschema:"Absolute path of the file on the device"`
	Offset     int64  `json:"offset,omitempty" jsonschema:"Byte offset to start from (for paging large files)"`
	Length     int64  `json:"length,omitempty" jsonschema:"Bytes to return (default: max output for text, 2 MiB for binary; max 4 MiB)"`
}

type ForwardIn struct {
	Device string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Action string `json:"action" jsonschema:"list | forward | reverse | remove_forward | remove_reverse"`
	Local  string `json:"local,omitempty" jsonschema:"PC side socket, e.g. tcp:9222 (tcp:0 = pick a free port for forward)"`
	Remote string `json:"remote,omitempty" jsonschema:"Device side socket, e.g. tcp:8081, localabstract:chrome_devtools_remote, jdwp:PID"`
}

type HTTPIn struct {
	Device  string            `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Socket  string            `json:"socket" jsonschema:"Device socket: tcp:PORT or localabstract:NAME (e.g. chrome_devtools_remote, webview_devtools_remote_PID)"`
	Method  string            `json:"method,omitempty" jsonschema:"HTTP method, default GET"`
	Path    string            `json:"path,omitempty" jsonschema:"Request path, default /"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"Extra request headers"`
	Body    string            `json:"body,omitempty" jsonschema:"Request body (text)"`
}

type RecordIn struct {
	Device  string `json:"device,omitempty" jsonschema:"Device id, alias or serial. Optional when exactly one AI-enabled device is online."`
	Seconds int    `json:"seconds,omitempty" jsonschema:"Duration, default 5, max 180"`
	Size    string `json:"size,omitempty" jsonschema:"Video size WIDTHxHEIGHT, e.g. 720x1600 (default: device resolution)"`
	Bitrate int    `json:"bitrate,omitempty" jsonschema:"Bits per second, default 2000000"`
}

func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+len(";base64,"):]
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		if b2, err2 := base64.RawStdEncoding.DecodeString(s); err2 == nil {
			return b2, nil
		}
		return nil, fmt.Errorf("data_base64 is not valid base64: %w", err)
	}
	return b, nil
}

func isText(b []byte) bool { return utf8.Valid(b) && !bytes.ContainsRune(b, 0) }

func (s *Server) registerFileTools() {
	m := s.d.Devices

	add(s, toolSpec{name: "upload_file", title: "Upload file to PC staging", level: config.LevelFull, noDevice: true, timeout: 2 * time.Minute,
		desc: "Upload a file in base64 chunks (at most 4 MiB decoded each) to adbridge's staging area on the PC. Start without upload_id, then keep sending chunks with the returned upload_id; set final=true on the last one. Use the upload_id with push_file or install_apk. Uploads expire after 1 hour."},
		func(ctx context.Context, c *call, in UploadIn) (*mcp.CallToolResult, error) {
			if s.d.Staging == nil {
				return nil, errors.New("staging is not available")
			}
			data, err := decodeB64(in.DataBase64)
			if err != nil {
				return nil, err
			}
			if len(data) > MaxChunkBytes {
				return nil, fmt.Errorf("chunk is %d bytes; split into chunks of at most %d", len(data), MaxChunkBytes)
			}
			it, err := s.d.Staging.Append(in.UploadID, in.Name, data, in.Final)
			if err != nil {
				return nil, err
			}
			return jsonResult(it), nil
		})

	add(s, toolSpec{name: "push_file", title: "Push file to device", level: config.LevelFull, destructive: true, timeout: 5 * time.Minute,
		desc: "Write a file to the device: a completed upload (upload_id) or small inline base64 content. Overwrites existing files."},
		func(ctx context.Context, c *call, in PushIn) (*mcp.CallToolResult, error) {
			perm := os.FileMode(0o644)
			if in.Mode != "" {
				v, err := strconv.ParseUint(in.Mode, 8, 32)
				if err != nil || v > 0o777 {
					return nil, fmt.Errorf("invalid mode %q", in.Mode)
				}
				perm = os.FileMode(v)
			}
			var n int64
			var err error
			switch {
			case in.UploadID != "":
				if s.d.Staging == nil {
					return nil, errors.New("staging is not available")
				}
				_, p, gerr := s.d.Staging.Get(in.UploadID)
				if gerr != nil {
					return nil, gerr
				}
				n, err = m.PushFile(ctx, c.dev.ID, p, in.RemotePath, perm)
			case in.DataBase64 != "":
				data, derr := decodeB64(in.DataBase64)
				if derr != nil {
					return nil, derr
				}
				if len(data) > maxInlinePush {
					return nil, errors.New("inline data is limited to 1 MiB; use upload_file and upload_id")
				}
				n, err = m.PushReader(ctx, c.dev.ID, bytes.NewReader(data), in.RemotePath, perm)
			default:
				return nil, errors.New("pass upload_id or data_base64")
			}
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprintf("pushed %d bytes to %s:%s (mode %o)", n, c.dev.Name, in.RemotePath, perm)), nil
		})

	add(s, toolSpec{name: "pull_file", title: "Pull file from device", level: config.LevelFull, readOnly: true, timeout: 2 * time.Minute,
		desc: "Read a file from the device (up to 64 MiB). UTF-8 text comes back as text; binary data comes back as an embedded base64 resource. Use offset/length to page through large files; the result states the next offset."},
		func(ctx context.Context, c *call, in PullIn) (*mcp.CallToolResult, error) {
			st, err := m.StatFile(ctx, c.dev.ID, in.RemotePath)
			if err != nil {
				return nil, err
			}
			if !st.Exists() {
				return nil, fmt.Errorf("%s does not exist (or is not readable by the shell user; try `run-as <pkg> cat` via shell)", in.RemotePath)
			}
			if st.Mode.IsDir() || st.Mode&0o170000 == 0o040000 {
				return nil, fmt.Errorf("%s is a directory; list it with shell `ls -la`", in.RemotePath)
			}
			if st.Size > maxPullBytes {
				return nil, fmt.Errorf("%s is %d bytes; files above %d bytes are not supported (split it on the device first)", in.RemotePath, st.Size, maxPullBytes)
			}
			data, err := m.PullFile(ctx, c.dev.ID, in.RemotePath, maxPullBytes)
			if err != nil {
				return nil, err
			}
			total := int64(len(data))
			off := min(max(in.Offset, 0), total)
			textMode := isText(data)
			length := in.Length
			if length <= 0 {
				length = 2 << 20
				if textMode {
					length = int64(c.max - 200)
				}
			}
			length = min(length, maxPullChunk, total-off)
			chunk := data[off : off+length]
			if textMode {
				// Do not split a UTF-8 sequence at the end of the page.
				for len(chunk) > 0 && !utf8.Valid(chunk) {
					chunk = chunk[:len(chunk)-1]
				}
			}
			next := off + int64(len(chunk))
			head := fmt.Sprintf("%s:%s %d bytes, returned [%d,%d)", c.dev.Name, in.RemotePath, total, off, next)
			if next < total {
				head += fmt.Sprintf(", next offset %d", next)
			}
			if textMode {
				return text(head + "\n" + string(chunk)), nil
			}
			mt := mime.TypeByExtension(path.Ext(in.RemotePath))
			if mt == "" {
				mt = "application/octet-stream"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: head + " (binary, " + mt + ")"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "adbridge://" + c.dev.ID + in.RemotePath, MIMEType: mt, Blob: chunk}},
			}}, nil
		})

	add(s, toolSpec{name: "port_forward", title: "Port forwarding", level: config.LevelFull, destructive: true,
		desc: "Manage adb port forwarding. forward: PC local → device remote (for tools running on the PC). reverse: device remote → PC local (lets the app reach a dev server on the PC, e.g. Metro on tcp:8081). list shows both. Remote MCP clients cannot reach PC ports; use device_http to talk to device sockets instead."},
		func(ctx context.Context, c *call, in ForwardIn) (*mcp.CallToolResult, error) {
			switch in.Action {
			case "list":
				fwd, rev, err := m.Forwards(ctx, c.dev.ID)
				if err != nil {
					return nil, err
				}
				return jsonResult(map[string]any{"forward": fwd, "reverse": rev}), nil
			case "forward":
				local := in.Local
				if local == "" {
					local = "tcp:0"
				}
				got, err := m.AddForward(ctx, c.dev.ID, local, in.Remote)
				if err != nil {
					return nil, err
				}
				return text(fmt.Sprintf("forward PC %s → %s %s", got, c.dev.Name, in.Remote)), nil
			case "reverse":
				if err := m.AddReverse(ctx, c.dev.ID, in.Remote, in.Local); err != nil {
					return nil, err
				}
				return text(fmt.Sprintf("reverse %s %s → PC %s", c.dev.Name, in.Remote, in.Local)), nil
			case "remove_forward":
				return text("removed forward " + in.Local), m.RemoveForward(ctx, c.dev.ID, in.Local)
			case "remove_reverse":
				return text("removed reverse " + in.Remote), m.RemoveReverse(ctx, c.dev.ID, in.Remote)
			}
			return nil, fmt.Errorf("unknown action %q", in.Action)
		})

	add(s, toolSpec{name: "device_http", title: "HTTP to device socket", level: config.LevelInteract,
		desc: "Send an HTTP request to a socket on the device through adb, without opening any PC port. Works for remote clients. Examples: socket localabstract:chrome_devtools_remote path /json lists debuggable Chrome tabs; localabstract:webview_devtools_remote_<pid> for WebViews; tcp:<port> for an app's debug server."},
		func(ctx context.Context, c *call, in HTTPIn) (*mcp.CallToolResult, error) {
			p := in.Path
			if p == "" {
				p = "/"
			}
			resp, err := m.DeviceHTTP(ctx, c.dev.ID, in.Socket, in.Method, p, in.Headers, []byte(in.Body), int64(c.max)*4)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "HTTP %d %s\n", resp.Status, http.StatusText(resp.Status))
			keys := make([]string, 0, len(resp.Headers))
			for k := range resp.Headers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(resp.Headers[k], ", "))
			}
			b.WriteByte('\n')
			if isText(resp.Body) {
				b.Write(resp.Body)
			} else {
				fmt.Fprintf(&b, "[%d bytes of binary body]\n%s", len(resp.Body), base64.StdEncoding.EncodeToString(resp.Body))
			}
			return text(b.String()), nil
		})

	add(s, toolSpec{name: "screenrecord", title: "Screen recording", level: config.LevelRead, readOnly: true, timeout: 4 * time.Minute,
		desc: "Record the screen to MP4 (default 5 s). The file is saved on the PC and, when at most 16 MiB, also returned as an embedded video/mp4 resource. Models that cannot watch video should use screenshot with count/interval_ms instead."},
		func(ctx context.Context, c *call, in RecordIn) (*mcp.CallToolResult, error) {
			data, err := m.ScreenRecord(ctx, c.dev.ID, in.Seconds, in.Size, in.Bitrate)
			if err != nil {
				return nil, err
			}
			saved := ""
			if s.d.RecordDir != "" {
				name := fmt.Sprintf("%s-%s.mp4", sanitize(c.dev.Name), time.Now().Format("20060102-150405"))
				p := filepath.Join(s.d.RecordDir, name)
				if os.WriteFile(p, data, 0o600) == nil {
					saved = p
					pruneRecordings(s.d.RecordDir)
				}
			}
			note := fmt.Sprintf("recorded %d KB from %s", len(data)/1024, c.dev.Name)
			if saved != "" {
				note += "; saved on the PC at " + saved
			}
			res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: note}}}
			if len(data) <= maxRecording {
				res.Content = append(res.Content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
					URI: "adbridge://" + c.dev.ID + "/screenrecord.mp4", MIMEType: "video/mp4", Blob: data,
				}})
			}
			return res, nil
		})
}

func sanitize(s string) string {
	s = nameSafe.Replace(s)
	if s == "" {
		return "device"
	}
	return s
}

var nameSafe = strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")

// pruneRecordings keeps the newest keepRecs files.
func pruneRecordings(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keepRecs {
		return
	}
	type fi struct {
		name string
		mod  time.Time
	}
	var files []fi
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			files = append(files, fi{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[min(keepRecs, len(files)):] {
		os.Remove(filepath.Join(dir, f.name))
	}
}
