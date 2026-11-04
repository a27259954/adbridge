## What changes

<!-- The user-visible change, and why. -->

## How it was tested

<!-- Tests added or run, and manual checks on a real device if relevant. -->

## Checklist

- [ ] `go vet ./...`, `go test ./internal/...` and `npm --prefix frontend run check` pass
- [ ] New UI strings are in every file under `frontend/src/lib/i18n/`
- [ ] New MCP tools use the lowest safe permission level and validate their input
- [ ] Docs updated (`docs/mcp-tools.md`, README) if behaviour changed
- [ ] No secrets, tokens or local AI instruction files are included
