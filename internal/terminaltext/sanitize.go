// Package terminaltext prepares untrusted text for terminal display.
package terminaltext

import (
	"strings"
	"unicode"
)

// Sanitize removes terminal control characters while preserving line breaks.
// Tabs are expanded and carriage returns are removed so text cannot overwrite
// previously rendered terminal content.
func Sanitize(value string) string {
	var cleaned strings.Builder
	cleaned.Grow(len(value))
	for _, char := range value {
		switch char {
		case '\n':
			cleaned.WriteRune(char)
		case '\t':
			cleaned.WriteString("    ")
		case '\r':
			// Carriage returns can rewrite the current terminal line.
		default:
			if !unicode.IsControl(char) {
				cleaned.WriteRune(char)
			}
		}
	}
	return cleaned.String()
}
