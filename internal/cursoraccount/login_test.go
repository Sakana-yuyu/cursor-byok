package cursoraccount

import (
	"path/filepath"
	"testing"
)

func TestBeginLoginReturnsSessionWithoutTokens(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, filepath.Join(root, "cursor-account.json"), nil)
	manager.openLoginURL = func(string) error { return nil }
	session, err := manager.BeginLogin()
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	if session.SessionID == "" || session.State != StateWaiting {
		t.Fatalf("session = %#v", session)
	}
	status, err := manager.LoginStatus(session.SessionID)
	if err != nil {
		t.Fatalf("LoginStatus() error = %v", err)
	}
	if status.State != StateWaiting {
		t.Fatalf("status = %#v", status)
	}
	if _, err := manager.LoginStatus("missing"); err == nil {
		t.Fatal("expected invalid session error")
	}
}
