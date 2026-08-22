package configprofile

import (
	"encoding/json"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestImportReturnsStoredProfileForPreview(t *testing.T) {
	store := New(t.TempDir())
	profileID := mustSaveProfile(t, store)
	profile, err := store.load(profileID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.Import(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Profile.ID == "" {
		t.Fatalf("preview = %#v", preview)
	}
	full, err := store.Preview(preview.Profile.ID, serverconfig.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if full.Profile.ID != preview.Profile.ID {
		t.Fatalf("full = %#v", full)
	}
}

func mustSaveProfile(t *testing.T, store *Store) string {
	t.Helper()
	summary, err := store.SaveCurrent("demo", "desc", []string{"routing"}, serverconfig.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return summary.ID
}
