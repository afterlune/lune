# TUI snapshot fixtures

These SVG files are deterministic Bubble Tea view snapshots used by TUI regression tests. They are test data, not design proposals or UI specifications.

The fixtures cover dark and light themes, text and image input, live and interrupted streams, usage details, MCP interactions, context settings, compaction status, clarification panels, execution previews, and Unicode tables. Historical user messages can be listed and shown again in the transcript with `/details`; each tool record has its own expandable output preview.

Refresh the fixtures with:

```powershell
go test ./internal/tui -args -update-tui-screenshots
```
