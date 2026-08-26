//go:build linux

package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartDesktopEntryEscapesExecutablePath(t *testing.T) {
	executable := "/tmp/Cursor Assistant/$work\\client\".bin"
	entry, err := BuildDesktopEntry(executable)
	if err != nil {
		t.Fatalf("BuildDesktopEntry() error = %v", err)
	}
	if !strings.Contains(entry, `Exec="/tmp/Cursor Assistant/\$work\\client\".bin"`) {
		t.Fatalf("entry = %q, path was not safely escaped", entry)
	}
	if !strings.Contains(entry, "Terminal=false") {
		t.Fatalf("entry = %q, Terminal=false missing", entry)
	}
}

func TestAutostartCreateUpdateDeleteAndQuery(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, "bin", "cursor-one")
	second := filepath.Join(home, "bin", "cursor-two")
	if err := WriteAutostart(home, first); err != nil {
		t.Fatalf("WriteAutostart(first) error = %v", err)
	}
	enabled, err := IsAutostartEnabled(home)
	if err != nil || !enabled {
		t.Fatalf("IsAutostartEnabled() = %v, %v, want true", enabled, err)
	}
	path := AutostartPath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Exec="`+first+`"`) {
		t.Fatalf("desktop entry = %q, first executable missing", data)
	}
	if err := WriteAutostart(home, second); err != nil {
		t.Fatalf("WriteAutostart(second) error = %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), first) || !strings.Contains(string(data), second) {
		t.Fatalf("updated desktop entry = %q", data)
	}
	if err := RemoveAutostart(home); err != nil {
		t.Fatalf("RemoveAutostart() error = %v", err)
	}
	enabled, err = IsAutostartEnabled(home)
	if err != nil || enabled {
		t.Fatalf("IsAutostartEnabled() after delete = %v, %v, want false", enabled, err)
	}
}
