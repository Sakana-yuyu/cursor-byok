package certs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewPersistentManagerGeneratesAndReusesLocalCA(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "data", "ca.crt")
	keyPath := filepath.Join(root, "data", "ca.key")

	first, err := NewPersistentManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("first manager: %v", err)
	}
	firstCert := first.CACertPEM()
	if len(firstCert) == 0 {
		t.Fatal("first manager returned an empty CA certificate")
	}

	for _, path := range []string{certPath, keyPath} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("expected generated file %s: %v", path, statErr)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("generated CA material %s is too permissive: %o", path, info.Mode().Perm())
		}
	}

	second, err := NewPersistentManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("second manager: %v", err)
	}
	if string(second.CACertPEM()) != string(firstCert) {
		t.Fatal("second manager did not reuse the existing CA certificate")
	}
}
