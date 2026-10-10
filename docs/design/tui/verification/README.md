# TUI verification screenshots

These twelve SVG snapshots are generated from deterministic Bubble Tea view fixtures by `TestTUISidebarSVGsMatchDeterministicFixtures`.

The matrix covers dark and light themes across empty, short output, long output, Activity scroll top/bottom, and narrow layouts. The long/short pair keeps right-side data identical so a moved sidebar or Usage anchor fails the layout assertions.

Refresh snapshots intentionally with:

`go test ./internal/tui -run TestTUISidebarSVGsMatchDeterministicFixtures -args -update-tui-screenshots`
