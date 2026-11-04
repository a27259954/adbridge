# Remote access

Cloud agents (hosted chat assistants, CI runners, remote dev containers) often cannot install anything locally. They can only call an HTTPS URL. adbridge publishes its MCP endpoint through a tunnel built into the app.

## Providers

| | ngrok | cloudflared Quick Tunnel |
|---|---|---|
| Account | Free account, authtoken pasted once | None |
| Packaging | Go SDK compiled into the exe | Downloaded on first use to `%LOCALAPPDATA%\adbridge\bin` and verified by SHA-256 |
| URL | Fixed if you set your free static domain | Random on every start |
| Notes | The free plan has monthly traffic limits | No SLA. Meant for temporary use. |

A fixed URL matters: you register a remote MCP server with a cloud client once, and with a random URL you would have to register it again after every restart.

## Setup (ngrok)

1. Sign up at <https://dashboard.ngrok.com> and copy your **Authtoken**.
2. Optional: under **Domains**, claim the free static domain (for example `your-name.ngrok-free.app`).
3. In adbridge, open **Settings → Tunnel**, choose ngrok, paste the token (and the domain), then save.
4. In the **MCP** tab, click **Start tunnel**.

## Connecting a client

The JSON button copies a config like this:

```json
{
  "mcpServers": {
    "adbridge": {
      "type": "http",
      "url": "https://your-name.ngrok-free.app/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

Some clients cannot set headers. For those, use **Secret URL** (`https://…/mcp/<token>`). Treat that URL like a password.

## What a remote agent can and cannot do

With the remote level set to `full`, a cloud agent has the same 24 tools as a local one. It can run the whole loop: build an APK in its own environment, send it with `upload_file` (4 MiB chunks), `install_apk {upload_id}`, reproduce the bug through `ui_tree`/`tap`, then read `logcat`, `crash_log`, `pull_file` and `screenshot`/`screenrecord`.

The difference from an agent running on the PC: a remote agent cannot reach ports on the PC. Use `device_http` to talk to sockets on the device (Chrome DevTools, WebView debugging, an app's debug server). `port_forward` only helps tools that run on the PC itself.

Things that still need a person at the phone:

- Unlocking the screen. Turning off the lock screen on a test device helps.
- Accepting the RSA prompt the first time a PC connects.
- Turning Wireless debugging back on after the phone changes Wi-Fi networks or reboots on some vendors.
- Screens with `FLAG_SECURE` (banking apps, DRM video), which capture as black.

## Permissions while the tunnel is up

- Remote calls use the **remote** level, which starts at `read`. Local calls are unaffected.
- Uncheck **AI** for any device the agent should not touch.
- The tunnel closes after the configured idle time (30 minutes by default). Each authenticated remote request resets the timer.
- The **Stop** button in the MCP tab is a kill switch. It shuts down the local endpoint and the tunnel.
