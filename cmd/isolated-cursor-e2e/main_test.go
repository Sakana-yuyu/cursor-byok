package main

import (
	"net"
	"path/filepath"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestBuildIsolatedConfigUsesProvidedLoopbackListeners(t *testing.T) {
	input := serverconfig.DefaultConfig()
	input.BackendListenAddr = "127.0.0.1:18090"
	input.ProxyListenAddr = "127.0.0.1:18080"

	got, err := buildIsolatedConfig(input, "127.0.0.1:28190", "127.0.0.1:28180")
	if err != nil {
		t.Fatalf("buildIsolatedConfig() error = %v", err)
	}
	if got.BackendListenAddr != "127.0.0.1:28190" || got.ProxyListenAddr != "127.0.0.1:28180" {
		t.Fatalf("listeners = backend:%q proxy:%q", got.BackendListenAddr, got.ProxyListenAddr)
	}
	if !got.Log {
		t.Fatal("isolated config must enable debug evidence logs")
	}
}

func TestBuildIsolatedConfigRejectsNonLoopbackOrDuplicateListeners(t *testing.T) {
	input := serverconfig.DefaultConfig()
	for _, test := range []struct {
		name    string
		backend string
		proxy   string
	}{
		{name: "non loopback backend", backend: "0.0.0.0:28190", proxy: "127.0.0.1:28180"},
		{name: "same listener", backend: "127.0.0.1:28190", proxy: "127.0.0.1:28190"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := buildIsolatedConfig(input, test.backend, test.proxy); err == nil {
				t.Fatal("buildIsolatedConfig() succeeded, want error")
			}
		})
	}
}

func TestBuildCursorChildEnvironmentReplacesIsolationVariables(t *testing.T) {
	root := t.TempDir()
	dirs := isolatedDirectories{
		home:         filepath.Join(root, "home"),
		appData:      filepath.Join(root, "appdata", "roaming"),
		localAppData: filepath.Join(root, "appdata", "local"),
	}
	caPath := filepath.Join(root, "assistant", "data", "ca.crt")
	env := buildCursorChildEnvironment([]string{
		"USERPROFILE=C:\\Users\\Administrator",
		"HOME=C:\\Users\\Administrator",
		"APPDATA=C:\\Users\\Administrator\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\Administrator\\AppData\\Local",
		"NODE_EXTRA_CA_CERTS=C:\\real\\ca.crt",
		"PATH=C:\\Windows",
	}, dirs, caPath)
	got := envValues(env)
	if got["USERPROFILE"] != dirs.home || got["HOME"] != dirs.home {
		t.Fatalf("home env = %#v", got)
	}
	if got["APPDATA"] != dirs.appData || got["LOCALAPPDATA"] != dirs.localAppData {
		t.Fatalf("appdata env = %#v", got)
	}
	if got["NODE_EXTRA_CA_CERTS"] != caPath {
		t.Fatalf("NODE_EXTRA_CA_CERTS = %q, want %q", got["NODE_EXTRA_CA_CERTS"], caPath)
	}
	if got["PATH"] != "C:\\Windows" {
		t.Fatalf("PATH = %q, want preserved", got["PATH"])
	}
	for _, item := range env {
		if item == "USERPROFILE=C:\\Users\\Administrator" ||
			item == "HOME=C:\\Users\\Administrator" ||
			item == "APPDATA=C:\\Users\\Administrator\\AppData\\Roaming" ||
			item == "LOCALAPPDATA=C:\\Users\\Administrator\\AppData\\Local" ||
			item == "NODE_EXTRA_CA_CERTS=C:\\real\\ca.crt" {
			t.Fatalf("child environment retained real profile value %q", item)
		}
	}
}

func TestValidateLoopbackListener(t *testing.T) {
	if err := validateLoopbackListener("127.0.0.1:28190"); err != nil {
		t.Fatalf("validateLoopbackListener(loopback) error = %v", err)
	}
	if err := validateLoopbackListener("localhost:28190"); err == nil {
		t.Fatal("validateLoopbackListener(localhost) succeeded, want strict loopback error")
	}
	if _, _, err := net.SplitHostPort("127.0.0.1:28190"); err != nil {
		t.Fatalf("test listener is malformed: %v", err)
	}
}

func TestAllocateLoopbackListenerReturnsUsableNonDefaultAddress(t *testing.T) {
	address, err := allocateLoopbackListener()
	if err != nil {
		t.Fatalf("allocateLoopbackListener() error = %v", err)
	}
	if err := validateLoopbackListener(address); err != nil {
		t.Fatalf("allocated listener %q is invalid: %v", address, err)
	}
	if address == "127.0.0.1:18080" || address == "127.0.0.1:18090" {
		t.Fatalf("allocated listener = %q, must not reuse production defaults", address)
	}
}
