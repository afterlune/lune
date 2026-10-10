# TUI verification screenshots

These twenty SVG snapshots are generated from deterministic Bubble Tea view fixtures by the session-workspace and transcript snapshot tests.

The session-workspace matrix covers dark and light themes across empty, short output, long output, Activity/Usage drawer scroll top/bottom, and narrow layouts. The transcript matrix covers short messages, long collapsed and expanded messages, and narrow layouts in both themes. SVGs preserve the rendered foreground colors and bold text, using the current ink/copper and warm-paper theme palettes for backgrounds and default text.

The short/long workspace pair keeps the left session rail fixed while the transcript grows. Tests also verify that drawer scrolling leaves the timeline and rail anchored, and that clicking a session restores it. Transcript assertions lock role-gutter alignment, label styling, plain message text, and long-message collapse thresholds.

Refresh snapshots intentionally with:

`go test ./internal/tui -run 'TestTUI(SessionWorkspace|Conversation)SVGsMatchDeterministicFixtures' -args -update-tui-screenshots`
