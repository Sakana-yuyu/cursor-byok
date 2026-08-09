package bridge

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"cursor/internal/backend/forwarder"
)

func setReaderMCPHomeForTest(t *testing.T, dir string) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", dir)
	default:
		t.Setenv("HOME", dir)
	}
}

func TestEnsureVisionReaderScriptCreatesValidSkillManifest(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)

	scriptPath, err := ensureVisionReaderScript()
	if err != nil {
		t.Fatalf("ensure vision reader script: %v", err)
	}
	wantScriptPath := filepath.Join(home, ".cursor", "skills", "image-see", "scripts", readerMCPBundledScriptName)
	if scriptPath != wantScriptPath {
		t.Fatalf("script path = %q, want %q", scriptPath, wantScriptPath)
	}
	if info, err := os.Stat(scriptPath); err != nil || info.IsDir() {
		t.Fatalf("reader script was not created as a file: info=%v err=%v", info, err)
	}

	forwarder.InvalidateSkillScanCache()
	t.Cleanup(forwarder.InvalidateSkillScanCache)
	items := forwarder.SnapshotSourcedSkills("")
	for _, item := range items {
		if item.Name != "image-see" {
			continue
		}
		if !item.Valid {
			t.Fatalf("deployed image-see manifest is invalid: %+v", item.Diagnostics)
		}
		if item.Source != string(forwarder.SkillSourceCursor) {
			t.Fatalf("image-see source = %q, want %q", item.Source, forwarder.SkillSourceCursor)
		}
		return
	}
	t.Fatalf("deployed image-see skill was not discovered: %+v", items)
}

func TestEnsureVisionReaderScriptRepairsManifestBesideExistingScript(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	scriptPath := filepath.Join(home, ".cursor", "skills", "image-see", "scripts", readerMCPBundledScriptName)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("create existing script directory: %v", err)
	}
	existingScript := []byte("existing reader script")
	if err := os.WriteFile(scriptPath, existingScript, 0o644); err != nil {
		t.Fatalf("write existing script: %v", err)
	}

	gotPath, err := ensureVisionReaderScript()
	if err != nil {
		t.Fatalf("repair existing image-see skill: %v", err)
	}
	if gotPath != scriptPath {
		t.Fatalf("script path = %q, want %q", gotPath, scriptPath)
	}
	gotScript, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read preserved script: %v", err)
	}
	if string(gotScript) != string(existingScript) {
		t.Fatalf("existing script was overwritten: got %q", gotScript)
	}
	manifestPath := filepath.Join(home, ".cursor", "skills", "image-see", readerMCPBundledManifestName)
	if info, err := os.Stat(manifestPath); err != nil || info.IsDir() {
		t.Fatalf("missing repaired manifest: info=%v err=%v", info, err)
	}
}

func TestEnsureVisionReaderScriptReplacesInvalidManifest(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	scriptPath := filepath.Join(home, ".cursor", "skills", "image-see", "scripts", readerMCPBundledScriptName)
	manifestPath := filepath.Join(home, ".cursor", "skills", "image-see", readerMCPBundledManifestName)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("create image-see directory: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("existing reader script"), 0o644); err != nil {
		t.Fatalf("write existing script: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("not a valid skill manifest"), 0o644); err != nil {
		t.Fatalf("write invalid manifest: %v", err)
	}

	if _, err := ensureVisionReaderScript(); err != nil {
		t.Fatalf("repair invalid manifest: %v", err)
	}
	forwarder.InvalidateSkillScanCache()
	t.Cleanup(forwarder.InvalidateSkillScanCache)
	for _, item := range forwarder.SnapshotSourcedSkills("") {
		if item.Name == "image-see" && item.Source == string(forwarder.SkillSourceCursor) {
			if !item.Valid {
				t.Fatalf("repaired image-see manifest is invalid: %+v", item.Diagnostics)
			}
			return
		}
	}
	t.Fatal("repaired image-see skill was not discovered")
}

func TestEnsureVisionReaderScriptRepairsEveryExistingInstallation(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	for _, toolDir := range []string{".claude", ".cursor"} {
		scriptPath := filepath.Join(home, toolDir, "skills", "image-see", "scripts", readerMCPBundledScriptName)
		if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
			t.Fatalf("create %s image-see directory: %v", toolDir, err)
		}
		if err := os.WriteFile(scriptPath, []byte(toolDir+" reader script"), 0o644); err != nil {
			t.Fatalf("write %s reader script: %v", toolDir, err)
		}
	}

	gotPath, err := ensureVisionReaderScript()
	if err != nil {
		t.Fatalf("repair existing installations: %v", err)
	}
	wantPath := filepath.Join(home, ".claude", "skills", "image-see", "scripts", readerMCPBundledScriptName)
	if gotPath != wantPath {
		t.Fatalf("selected script path = %q, want %q", gotPath, wantPath)
	}
	for _, toolDir := range []string{".claude", ".cursor"} {
		manifestPath := filepath.Join(home, toolDir, "skills", "image-see", readerMCPBundledManifestName)
		if info, err := os.Stat(manifestPath); err != nil || info.IsDir() {
			t.Fatalf("missing %s repaired manifest: info=%v err=%v", toolDir, info, err)
		}
	}
}

func TestEnsureVisionReaderScriptDoesNotLeaveScriptWhenManifestCannotBeCreated(t *testing.T) {
	home := t.TempDir()
	setReaderMCPHomeForTest(t, home)
	skillDir := filepath.Join(home, ".cursor", "skills", "image-see")
	manifestPath := filepath.Join(skillDir, readerMCPBundledManifestName)
	if err := os.MkdirAll(manifestPath, 0o755); err != nil {
		t.Fatalf("create blocking manifest directory: %v", err)
	}

	if _, err := ensureVisionReaderScript(); err == nil {
		t.Fatal("ensureVisionReaderScript succeeded with a directory at SKILL.md")
	}
	scriptPath := filepath.Join(skillDir, "scripts", readerMCPBundledScriptName)
	if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
		t.Fatalf("reader script was left behind after manifest failure: err=%v", err)
	}
}
