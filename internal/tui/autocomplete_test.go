package tui

import (
	"strings"
	"testing"
)

func TestAutocompleteScrolling(t *testing.T) {
	items := make([]AutocompleteItem, 25)
	for i := range items {
		items[i] = AutocompleteItem{Command: "/cmd" + string(rune('a'+i)), Description: "Description for item"}
	}

	// When selected is 0: shows top 7 items.
	rendered0 := renderAutocompletePopup(items, 0, 80)
	lines0 := strings.Split(rendered0, "\n")
	if len(lines0) != maxVisibleAutocomplete {
		t.Fatalf("Expected %d lines, got %d", maxVisibleAutocomplete, len(lines0))
	}
	if !strings.Contains(lines0[0], "› /cmda") {
		t.Errorf("Expected first line to be selected /cmda, got: %s", lines0[0])
	}

	// When selected is 12 (middle): shows 7 items centered around 12.
	rendered12 := renderAutocompletePopup(items, 12, 80)
	lines12 := strings.Split(rendered12, "\n")
	if len(lines12) != maxVisibleAutocomplete {
		t.Fatalf("Expected %d lines, got %d", maxVisibleAutocomplete, len(lines12))
	}
	hasSelected12 := false
	for _, line := range lines12 {
		if strings.Contains(line, "› ") {
			hasSelected12 = true
		}
	}
	if !hasSelected12 {
		t.Errorf("Expected selected marker in lines12")
	}

	// When selected is 24 (last item): shows bottom 7 items.
	rendered24 := renderAutocompletePopup(items, 24, 80)
	lines24 := strings.Split(rendered24, "\n")
	if len(lines24) != maxVisibleAutocomplete {
		t.Fatalf("Expected %d lines, got %d", maxVisibleAutocomplete, len(lines24))
	}
	if !strings.Contains(lines24[len(lines24)-1], "› ") {
		t.Errorf("Expected last line to be selected, got: %s", lines24[len(lines24)-1])
	}
}
