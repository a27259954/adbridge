# Security

adbridge lets software (AI agents) control phones, and the tunnel can expose that control to the internet. This document describes the threat model and the safeguards that are on by default.

## Defaults

| Area | Default |
|---|---|
| Local endpoint | Binds to `127.0.0.1:8765` only, never `0.0.0.0` |
| Local requests | DNS-rebinding protection (the Host header must be loopback). Browser `Origin` headers other than localhost are rejected. A token is optional (*Settings → Require the token for local clients*). |
| Remote requests (tunnel) | Token required, either as `Authorization: Bearer <token>` or as the secret path `/mcp/<token>`, compared in constant time. The tunnel cannot start without a token. |
| Permission level | Local `full`, remote `read`. Raising remote access to `full` asks for confirmation. |
| Device scope | Each device has an **AI** checkbox. Devices with it unchecked do not appear in `list_devices` and are refused by every tool. |
| Tunnel lifetime | Closes after 30 minutes without remote traffic (configurable). |
| Audit | Every tool call (origin, peer IP, client, tool, device, arguments, result) appears in the UI and is appended to `%LOCALAPPDATA%\adbridge\logs\audit-*.jsonl`. Secrets in arguments are masked. Long values such as base64 chunks and HTTP bodies are logged as their length only. |
| Size limits | Request bodies are capped at 8 MiB and upload chunks at 4 MiB. Each staged upload can be up to 1 GiB, with 4 GiB in total. Uploads expire after 1 hour and are wiped on start. `pull_file` reads at most 64 MiB, and text output per call is capped (60,000 characters by default). APK downloads are capped at 1 GB. |
| Input validation | Socket specs (`tcp:`, `localabstract:`, …), remote paths, package names, key names, `dumpsys` arguments and recording sizes are validated before they reach adb. File transfer uses the sync protocol, not shell strings. |

The origin of a request (local or remote) comes from the listener that accepted it. The middleware overwrites the internal origin header on every request, so a client cannot claim to be local. `internal/mcpsrv/server_test.go` tests this.

## Permission levels

- **read**: `list_devices`, `device_info`, `logcat`, `crash_log`, `screenshot`, `screenrecord`, `ui_tree`, `list_packages`, `dumpsys`
- **interact**: read, plus input events, start/stop apps, clear logcat, `device_http` (HTTP to sockets on the device)
- **full**: interact, plus arbitrary `shell`, `upload_file`, `push_file`, `pull_file`, `port_forward`, `install_apk`, `app_clear`

`full` amounts to running arbitrary commands as the `shell` user on the phone. It also allows reading any file that user can read, including photos and downloads on shared storage, and writing files to the device. Give it to remote clients only while you are actively using them.

`port_forward` with `reverse` lets the phone reach ports on the PC's loopback interface, and `forward` opens a port on the PC's loopback interface. Neither one exposes anything to the network, but a remote agent with `full` could point the phone at local services on your PC. `device_http` never opens a PC port: it tunnels the request through the adb transport.

## Secrets at rest

The MCP token and the ngrok authtoken are stored **in plain text** in `%APPDATA%\adbridge\config.json`. This is a deliberate trade-off. The file sits in your Windows profile, which only your account (and administrators) can read, so it is as safe as an environment variable or another CLI's config file. If someone can read your profile, they can already use `adb` directly. You can rotate the MCP token from the MCP tab.

## Tunnels

- With **ngrok** and **cloudflared**, TLS terminates at the provider's edge, so the provider can see the traffic. Do not send secrets through tools while the tunnel is open.
- adbridge downloads **cloudflared** only from `github.com/cloudflare/cloudflared` releases. It verifies the binary against the SHA-256 digest that GitHub publishes for the asset and refuses to run it if the digest is missing or does not match.

## Reporting a vulnerability

Please report security issues privately through GitHub's **Security → Report a vulnerability** on this repository, not in public issues.
