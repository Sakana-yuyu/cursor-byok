package appdata

import (
	"testing"
)

func TestDesktopSettingsDefaultsAndRoundTrip(t *testing.T) {
	t.Setenv(RootDirEnvVar, t.TempDir())

	got, err := LoadDesktopSettings()
	if err != nil {
		t.Fatalf("LoadDesktopSettings() error = %v", err)
	}
	if got.SilentStart {
		t.Fatal("default SilentStart = true, want false")
	}

	want := DesktopSettings{SilentStart: true}
	if err := SaveDesktopSettings(want); err != nil {
		t.Fatalf("SaveDesktopSettings() error = %v", err)
	}
	got, err = LoadDesktopSettings()
	if err != nil {
		t.Fatalf("LoadDesktopSettings() after save error = %v", err)
	}
	if got != want {
		t.Fatalf("loaded settings = %#v, want %#v", got, want)
	}
}
