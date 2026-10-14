# TUI verification screenshots

These SVG files are deterministic Bubble Tea view fixtures for the native-scrollback TUI. The live screen uses the terminal's scrollback for stable user, assistant, tool, and notice entries; only the input, active stream, and selected activity details are redrawn.

The fixtures cover dark and light themes, text and image input, live and interrupted streams, usage details, MCP interactions, context settings, and compaction status. Historical messages can be listed and reprinted with `/details`; the TUI does not fold transcript rows.

Refresh the fixtures with:

```powershell
go test ./internal/tui -args -update-tui-screenshots
```
