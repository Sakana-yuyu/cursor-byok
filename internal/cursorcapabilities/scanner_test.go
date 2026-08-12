package cursorcapabilities

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanReportsMarkersWithoutExposingRoot(t *testing.T) {
	root := t.TempDir()
	writeFixtureCursorInstall(t, root, map[string]string{
		"cursor-browser-automation": `cursor-ide-browser browser_tabs browser_lock`,
		"cursor-agent-exec":         `computer_use force_background_subagent subagent_await`,
	})

	report, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if report.InstallRootHash == "" || strings.Contains(string(encoded), root) {
		t.Fatal("report must hash, not expose, the installation root")
	}
	if !contains(report.BrowserToolMarkers, "browser_tabs") {
		t.Fatal("expected browser_tabs marker")
	}
	if !contains(report.ProtocolMarkers, "subagent_await") {
		t.Fatal("expected subagent_await marker")
	}
}

func TestScanWarnsWhenOptionalExtensionIsMissing(t *testing.T) {
	root := t.TempDir()
	writeFixtureCursorInstall(t, root, map[string]string{
		"cursor-browser-automation": `cursor-ide-browser browser_tabs browser_lock`,
	})

	report, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Warnings) == 0 {
		t.Fatal("expected warning for missing optional extension")
	}
}

func writeFixtureCursorInstall(t *testing.T, root string, bundles map[string]string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "Cursor.exe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for extensionID, bundle := range bundles {
		extensionDir := filepath.Join(root, "resources", "app", "extensions", extensionID)
		if err := os.MkdirAll(filepath.Join(extensionDir, "dist"), 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := []byte(`{"name":"` + extensionID + `","version":"0.0.1"}`)
		if err := os.WriteFile(filepath.Join(extensionDir, "package.json"), manifest, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(extensionDir, "dist", "main.js"), []byte(bundle), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
