package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cursor/internal/cursoraccount"
)

func newTestProxyServiceWithAccount(t *testing.T, authID, email string) *ProxyService {
	t.Helper()
	root := t.TempDir()
	manager := cursoraccount.NewManager(root, filepath.Join(root, "cursor-account.json"), &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req == nil || req.URL == nil {
				return nil, errors.New("unexpected http request in test")
			}
			return nil, errors.New("unexpected http request in test: " + req.URL.String())
		}),
		Timeout: 2 * time.Second,
	})
	if _, err := manager.SeedAccountForTest(authID, email, true); err != nil {
		t.Fatal(err)
	}
	return &ProxyService{cursorAccount: manager}
}

func TestClientAccountListNeverContainsTokens(t *testing.T) {
	service := newTestProxyServiceWithAccount(t, "auth-a", "a@example.test")
	got, err := service.ListCursorAccounts()
	if err != nil || len(got) != 1 {
		t.Fatalf("got %#v, %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("test-access")) {
		t.Fatal("token leaked through client DTO")
	}
}

func TestClientImportCursorAccountRejectsOversizedInputs(t *testing.T) {
	service := newTestProxyServiceWithAccount(t, "auth-a", "a@example.test")
	if _, err := service.ImportCursorAccount(cursoraccount.CursorAccountImportRequest{
		Mode:  "token",
		Token: strings.Repeat("a", 8*1024+1),
	}); err == nil {
		t.Fatal("expected oversized token rejection")
	}
	if _, err := service.ImportCursorAccount(cursoraccount.CursorAccountImportRequest{
		Mode:        "recovery_json",
		JSONContent: strings.Repeat("a", 1<<20+1),
	}); err == nil {
		t.Fatal("expected oversized json rejection")
	}
}

func TestClientSetCurrentCursorAccountRejectsEmptyID(t *testing.T) {
	service := newTestProxyServiceWithAccount(t, "auth-a", "a@example.test")
	if _, err := service.SetCurrentCursorAccount("  "); err == nil {
		t.Fatal("expected empty id rejection")
	}
}

func TestClientRecoveryExportResultOmitsTokens(t *testing.T) {
	service := newTestProxyServiceWithAccount(t, "auth-a", "a@example.test")
	accounts, err := service.ListCursorAccounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("got %#v, %v", accounts, err)
	}
	prepared, err := service.PrepareCursorAccountRecoveryExport(cursoraccount.CursorAccountRecoveryExportRequest{
		AccountIDs: []string{accounts[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ConfirmationToken == "" {
		t.Fatal("expected non-empty confirmation token")
	}
	encodedPrep, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedPrep, []byte("test-access")) || bytes.Contains(encodedPrep, []byte("test-refresh")) {
		t.Fatal("prepare DTO leaked account tokens")
	}
	dest := filepath.Join(t.TempDir(), "recovery.json")
	result, err := service.ExecuteCursorAccountRecoveryExport(prepared.ConfirmationToken, dest)
	if err != nil {
		t.Fatal(err)
	}
	encodedRes, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedRes, []byte("test-access")) || bytes.Contains(encodedRes, []byte("test-refresh")) {
		t.Fatal("export result leaked account tokens")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("expected recovery file: %v", err)
	}
}
