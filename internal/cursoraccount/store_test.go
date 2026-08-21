package cursoraccount

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testCredential(authID, email string) credentials {
	return credentials{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		AuthID:       authID,
		Email:        email,
	}
}

func assertSummariesOmitSecrets(t *testing.T, summaries []CursorAccountSummary, secrets ...string) {
	t.Helper()
	encoded, err := json.Marshal(summaries)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(string(encoded), secret) {
			t.Fatalf("account summary leaked secret %q: %s", secret, encoded)
		}
	}
}

func TestAccountStoreMigratesLegacyCredentialsOnce(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "cursor-account.json")
	writeTestJSON(t, legacy, map[string]string{
		"accessToken":  "test-access",
		"refreshToken": "test-refresh",
		"authId":       "auth-a",
		"email":        "a@example.test",
	})
	store := NewAccountStore(root, legacy)
	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Email != "a@example.test" || !summaries[0].IsCurrent {
		t.Fatalf("unexpected migrated summaries: %#v", summaries)
	}
	if summaries[0].AuthIDHint != "auth-a" {
		t.Fatalf("unexpected authId hint: %#v", summaries[0])
	}
	assertSummariesOmitSecrets(t, summaries, "test-access", "test-refresh")
	if _, err := os.Stat(filepath.Join(root, "legacy", "cursor-account.json.bak")); err != nil {
		t.Fatalf("expected legacy backup: %v", err)
	}

	again, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 {
		t.Fatalf("expected migration to run once, got %#v", again)
	}
}

func TestAccountStoreRepairsMissingIndexFromAccountFiles(t *testing.T) {
	store := NewAccountStore(t.TempDir(), "")
	_, err := store.Upsert(testCredential("auth-a", "a@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.IndexPathForTest()); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.List()
	if err != nil || len(summaries) != 1 {
		t.Fatalf("got %#v, %v", summaries, err)
	}
	if summaries[0].Email != "a@example.test" {
		t.Fatalf("unexpected repaired summary: %#v", summaries[0])
	}
	assertSummariesOmitSecrets(t, summaries, "test-access", "test-refresh")
}
