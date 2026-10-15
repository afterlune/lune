package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUIPreferencesLoadSaveAndFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	prefs, err := loadUIPreferences(path)
	if err != nil || !reflect.DeepEqual(prefs, defaultUIPreferences()) {
		t.Fatalf("missing preferences = %#v, %v; want defaults", prefs, err)
	}
	want := defaultUIPreferences()
	want.Theme = themeLight
	want.CopyOnSelect = "always"
	want.ClearSelectionOnCopy = true
	if err := saveUIPreferences(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadUIPreferences(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded preferences = %#v, %v; want %#v", got, err, want)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = loadUIPreferences(path)
	if err == nil || !reflect.DeepEqual(got, defaultUIPreferences()) {
		t.Fatalf("invalid preferences = %#v, %v; want defaults and an error", got, err)
	}
}

func TestUIPreferencesDefaultsLegacyKeymapAndFallsBackOnInvalidBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	if err := os.WriteFile(path, []byte(`{"theme":"light","anime_layout":"studio"}`), 0600); err != nil {
		t.Fatal(err)
	}
	prefs, err := loadUIPreferences(path)
	if err != nil || prefs.Theme != themeLight || !reflect.DeepEqual(prefs.Keymap, defaultKeymap()) {
		t.Fatalf("legacy preferences = %#v, %v; want supported theme and default keymap", prefs, err)
	}

	if err := os.WriteFile(path, []byte(`{"theme":"light","keymap":{"input_select_all":"ctrl+x"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	prefs, err = loadUIPreferences(path)
	if err == nil || prefs.Theme != themeLight || !reflect.DeepEqual(prefs.Keymap, defaultKeymap()) {
		t.Fatalf("invalid keymap fallback = %#v, %v; want default bindings and preserved theme", prefs, err)
	}
}
