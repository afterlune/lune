package tui

import (
	"strings"

	"github.com/atotto/clipboard"
)

func sanitizeClipboardText(text string) string {
	return strings.ReplaceAll(text, "\x00", "")
}

func writeClipboardText(text string) (string, error) {
	text = sanitizeClipboardText(text)
	return text, clipboard.WriteAll(text)
}
