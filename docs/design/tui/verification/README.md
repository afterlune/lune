# TUI verification screenshots

These SVG files are deterministic Bubble Tea view fixtures for the alternate-screen TUI. The live screen owns a fixed transcript viewport, composer, command popup, and status area; conversation history remains in the session when the TUI exits.

The fixtures cover dark and light themes, text and image input, live and interrupted streams, usage details, MCP interactions, context settings, and compaction status. Historical user messages can be listed and shown again in the transcript with `/details`; the TUI does not fold transcript rows.

Refresh the fixtures with:

```powershell
go test ./internal/tui -args -update-tui-screenshots
```
