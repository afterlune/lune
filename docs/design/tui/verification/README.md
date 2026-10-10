# TUI verification screenshots

These twenty SVG snapshots are generated from deterministic Bubble Tea view fixtures by the sidebar and transcript snapshot tests.

The sidebar matrix covers dark and light themes across empty, short output, long output, Activity scroll top/bottom, and narrow layouts. The transcript matrix covers short messages, long collapsed and expanded messages, and narrow layouts in both themes. SVGs preserve the rendered foreground colors and bold text, using the current theme palette for the background and default text.

The long/short sidebar pair keeps right-side data identical so a moved sidebar or Usage anchor fails the layout assertions. Transcript assertions lock user alignment, marker styling, plain message text, and the long-message collapse thresholds.

Refresh snapshots intentionally with:

`go test ./internal/tui -run 'TestTUI(Sidebar|Conversation)SVGsMatchDeterministicFixtures' -args -update-tui-screenshots`
