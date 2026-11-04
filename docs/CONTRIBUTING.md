# Contributing

Thanks for your interest! adbridge is meant to stay small and focused: it connects phones reliably and gives AI agents a safe debugging surface. Before you start a larger feature, please open an issue so we can agree on the scope.

## Setup

- Go 1.25+
- Node 20+
- Wails v3 CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28`
- Android SDK Platform-Tools, for manual testing

```bash
wails3 dev
```

```bash
wails3 task check
```

## Layout rules

- Only `internal/app` (and `main.go`) may import Wails. Core packages (`adb`, `device`, `mcpsrv`, `tunnel`, `pairing`, `audit`, `config`) must stay framework-free and testable.
- Test new adb protocol behaviour against the fake server in `internal/adb/adbtest`. Tests must not need a real device.
- New MCP tools go in `internal/mcpsrv/tools.go` through `add(...)` with the lowest permission level that is safe. Validate every value that ends up in a shell command, or quote it with `adb.Quote`.
- Frontend bindings in `frontend/bindings` are generated. Regenerate them with `wails3 generate bindings -clean=true -ts -i` and never edit them by hand.
- UI strings live in `frontend/src/lib/i18n/`. `en.ts` is the source. Every other dictionary is typed `Dict`, so `npm run check` fails if a key is missing. To add a language, add a file, register it in `LANGUAGES` and `dicts` in `index.svelte.ts`, add it to `config.Languages` and to `ResolveLanguage` and the tray labels in `internal/app/lang.go`. MCP tool names and descriptions stay in English.
- UI: keep it compact and functional. Use the CSS tokens in `frontend/src/app.css`. No UI kits, hero banners or marketing copy.

## Commits and PRs

- Keep PRs focused. Describe the user-visible change and how you tested it.
- `go vet`, `go test ./internal/...` and `npm run check` must pass. CI runs all three.

## Releases

Releases are automatic. Every push to `main` that touches code runs `.github/workflows/release.yml`, which tests the commit, builds `adbridge.exe` and publishes a GitHub release with a SHA-256 checksum.

- **Version:** `v<VERSION>.<n>`. `VERSION` holds the minor line (for example `0.1`), and `n` counts up from the existing tags (`v0.1.0`, `v0.1.1`, …). Edit `VERSION` to start a new line (`0.2` → `v0.2.0`).
- **Skipped automatically:** pushes that only change Markdown, `docs/`, `LICENSE` or issue templates.
- **Skip by hand:** put `[skip release]` in the commit message.
- **Release notes** are generated from the merged PRs and commits since the previous tag.

## Keep these out of the repo

`.gitignore` already covers them. Do not force-add them:

- Tokens, keys and certificates (`.env*`, `*.pem`, `*.pfx`, `*.key`, adb keys)
- Local runtime data (`config.json`, `devices.json`, audit logs, uploads, recordings)
- AI assistant instruction and state files (`AGENT.md`, `AGENTS.md`, `CLAUDE.md`, `.claude/`, `.cursor/`, `GEMINI.md`, `.github/copilot-instructions.md`, …)
