# MCP tools

Endpoint: `POST /mcp` (Streamable HTTP, stateless, JSON responses). Requests can be up to 8 MiB.

Every tool that targets a device accepts an optional `device` argument: a device id (`ro.serialno`), an alias, or an adb serial. You can leave it out when exactly one AI-enabled device is online.

Tool errors come back as `isError: true` results with a readable message. A permission denial names the level the call needs. Unknown arguments are rejected by schema validation.

## read

| Tool | Arguments | Returns |
|---|---|---|
| `list_devices` | none | JSON list: id, name, model, android, sdk, state, connection (`wifi`/`usb`), battery |
| `device_info` | `device` | Resolution, density, foreground activity, screen state, ABI, Wi-Fi IP, security patch, fingerprint, transports |
| `logcat` | `device`, `level` (V–F), `tag`, `package`, `grep` (regex, case-insensitive), `since_seconds`, `limit` (≤2000, default 200) | Newest matching lines, oldest first, read from adbridge's 20,000-line ring buffer |
| `crash_log` | `device`, `lines` | `logcat -b crash` |
| `screenshot` | `device`, `max_size` (default 1280), `format` (`jpeg`/`png`), `count` (1–8), `interval_ms` (default 500) | Text note with the scale factor, plus one image per frame. Use `count` > 1 to watch animations, transitions or flicker. |
| `screenrecord` | `device`, `seconds` (default 5, ≤180), `size` (`WxH`), `bitrate` (default 2,000,000) | MP4 saved on the PC under `%LOCALAPPDATA%\adbridge\recordings` (the newest 20 are kept). When the file is 16 MiB or smaller, it is also returned as an embedded `video/mp4` resource. |
| `ui_tree` | `device` | Numbered elements: `[12] Button "Log in" id=login (540,1850) click` |
| `list_packages` | `device`, `filter`, `third_party` | Package names |
| `dumpsys` | `device`, `service`, `args` | Raw dumpsys output. The service name and arguments are validated against a safe character set. |

## interact

| Tool | Arguments | Notes |
|---|---|---|
| `tap` | `device`, `element` **or** `x`+`y` | `element` refers to the last `ui_tree` result for that device |
| `swipe` | `device`, `x1`, `y1`, `x2`, `y2`, `duration_ms` | Use equal points with a long duration for a long-press |
| `input_text` | `device`, `text` | ASCII only (an `input text` limitation) |
| `key` | `device`, `key` | `BACK`, `HOME`, `ENTER`, `APP_SWITCH`, `DEL`, … or a keycode |
| `app_start` | `device`, `package`, `activity` | Launcher activity by default. `activity` may be `.Main` or `pkg/.Main`. |
| `app_stop` | `device`, `package` | `am force-stop` |
| `logcat_clear` | `device` | Clears both the device buffer and adbridge's ring buffer |
| `device_http` | `device`, `socket`, `method`, `path`, `headers`, `body` | Sends an HTTP request to a socket on the device through adb. No PC port is opened, so remote clients can use it too. `socket` is `tcp:PORT` or `localabstract:NAME`. |

`device_http` examples:

- `{socket: "localabstract:chrome_devtools_remote", path: "/json"}` lists the Chrome tabs you can debug.
- `{socket: "localabstract:webview_devtools_remote_<pid>", path: "/json"}` lists the debuggable WebViews of an app. Find the pid with `pidof <package>` or the `logcat` tool.
- `{socket: "tcp:8080", path: "/health"}` reaches an app's embedded debug server.

## full

| Tool | Arguments | Notes |
|---|---|---|
| `shell` | `device`, `command`, `timeout_seconds` (≤300) | Returns the exit code, stdout and stderr (shell protocol v2) |
| `upload_file` | `upload_id`, `name`, `data_base64`, `final` | Stages a file on the PC in chunks of at most 4 MiB (decoded). Leave out `upload_id` to start a new upload. Send `final: true` with the last chunk, which returns the size and SHA-256. Uploads expire after 1 hour. Limits: 1 GiB per file, 4 GiB in total. |
| `push_file` | `device`, `remote_path`, `upload_id` **or** `data_base64` (≤1 MiB), `mode` (octal, default `644`) | Writes a file to the device over the adb sync protocol |
| `pull_file` | `device`, `remote_path`, `offset`, `length` | Reads files up to 64 MiB. UTF-8 text comes back as text. Binary data comes back as an embedded base64 resource of at most 4 MiB per call. The result gives the next offset for paging. |
| `port_forward` | `device`, `action` (`list`, `forward`, `reverse`, `remove_forward`, `remove_reverse`), `local`, `remote` | `adb forward` / `adb reverse`. `forward` with `local: "tcp:0"` picks a free PC port. |
| `install_apk` | `device`, `upload_id` **or** `url` **or** `path` | `adb install -r -t`. URL downloads are capped at 1 GB. |
| `app_clear` | `device`, `package` | `pm clear` (irreversible) |

Socket specs accepted by `port_forward` and `device_http`: `tcp:PORT`, `localabstract:NAME`, `localreserved:NAME`, `localfilesystem:PATH`, `jdwp:PID`.

## Suggested workflows

**Reproduce a bug**

1. Call `list_devices`, then `device_info` to see the foreground app.
2. Call `logcat_clear`. Reproduce the bug with `app_start`, then `ui_tree` → `tap`.
3. Read the results with `logcat {package, level: "W"}`, `crash_log`, and `screenshot` (or `screenshot {count: 5, interval_ms: 300}` for transient UI).

**Ship a fix from a cloud environment**

1. Build the APK in your own environment.
2. Send it with `upload_file`: repeat it with 4 MiB chunks and set `final: true` on the last one, which returns an `upload_id`.
3. Call `install_apk {upload_id}`, then `app_start`, then verify with `logcat` and `screenshot`.

**Inspect app data** (debuggable apps)

- Run `shell {command: "run-as com.example cat shared_prefs/prefs.xml"}`.
- Or copy a file somewhere the shell user can read first: `shell {command: "run-as com.example cat databases/app.db > /data/local/tmp/app.db"}`, then `pull_file {remote_path: "/data/local/tmp/app.db"}`. The redirect runs as the shell user, which can write to `/data/local/tmp`. The app's user cannot.
