package settings

import (
	"path/filepath"
	"testing"
)

func TestWebUIPreferencesPersistAndStayInSyncWithLegacyPickers(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	initial, err := store.WebUIPreferences()
	if err != nil || initial != DefaultUIPreferences() {
		t.Fatalf("initial preferences = %#v, %v", initial, err)
	}
	want := UIPreferences{
		Theme: "dark", Language: "zh", BodyFontSize: 16, CodeFontSize: 14,
		RunningEnter: "steer", TimelineDetailsExpanded: true, UsageDensity: "compact",
	}
	if err := store.SaveWebUIPreferences(want); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWebLanguage("en"); err != nil {
		t.Fatal(err)
	}
	got, err := store.WebUIPreferences()
	if err != nil {
		t.Fatal(err)
	}
	want.Language = "en"
	if got != want {
		t.Fatalf("preferences = %#v, want %#v", got, want)
	}
	if err := store.SaveWebTheme("light"); err != nil {
		t.Fatal(err)
	}
	got, err = store.WebUIPreferences()
	if err != nil {
		t.Fatal(err)
	}
	want.Theme = "light"
	if got != want {
		t.Fatalf("theme picker did not update shared preferences: %#v", got)
	}
}

func TestWebUIPreferencesRejectInvalidValues(t *testing.T) {
	if err := (UIPreferences{Theme: "blue", Language: "en", BodyFontSize: 14, CodeFontSize: 12, RunningEnter: "queue", UsageDensity: "comfortable"}).Validate(); err == nil {
		t.Fatal("expected invalid theme to be rejected")
	}
}
