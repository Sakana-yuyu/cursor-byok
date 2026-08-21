package buildinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleasePackagesIncludeThirdPartyNotices(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))

	checks := map[string][]string{
		"THIRD_PARTY_NOTICES.md": {
			"can1357/oh-my-pi",
			"Copyright (c) 2025 Mario Zechner",
			"Copyright (c) 2025-2026 Can Bölük",
		},
		"build/windows/Taskfile.yml": {
			"Copy-Item -Force THIRD_PARTY_NOTICES.md bin/.zip-{{.ARCH}}/",
			"cp \"{{.OUTPUT}}\" THIRD_PARTY_NOTICES.md \"{{.STAGING_DIR}}/\"",
			"Copy-Item -Force '{{.PROJECT_ROOT}}/THIRD_PARTY_NOTICES.md' 'THIRD_PARTY_NOTICES.md'",
		},
		"build/windows/nsis/project.nsi": {
			"File \"/oname=THIRD_PARTY_NOTICES.md\" \"THIRD_PARTY_NOTICES.md\"",
		},
		"build/windows/nsis/project-386.nsi": {
			"File \"/oname=THIRD_PARTY_NOTICES.md\" \"THIRD_PARTY_NOTICES.md\"",
		},
		"build/darwin/Taskfile.yml": {
			"cp build/darwin/icons.icns THIRD_PARTY_NOTICES.md \"{{.BIN_DIR}}/{{.APP_BUNDLE}}/Contents/Resources\"",
		},
		"build/linux/Taskfile.yml": {
			"cp THIRD_PARTY_NOTICES.md \"{{.STAGING_DIR}}/THIRD_PARTY_NOTICES.md\"",
			"THIRD_PARTY_NOTICES.md",
		},
		"build/linux/nfpm/nfpm.yaml": {
			"./THIRD_PARTY_NOTICES.md",
			"/usr/share/doc/cursor-byok/THIRD_PARTY_NOTICES.md",
		},
		"frontend/public/THIRD_PARTY_NOTICES.md": {
			"can1357/oh-my-pi",
		},
		"frontend/scripts/assert-build-output.mjs": {
			"distRoot, \"THIRD_PARTY_NOTICES.md\"",
			"production assets do not contain the synchronized third-party notices",
		},
		"main.go": {
			"//go:embed all:frontend/dist",
		},
	}

	for relativePath, expected := range checks {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		text := string(body)
		for _, fragment := range expected {
			if !strings.Contains(text, fragment) {
				t.Errorf("%s does not include required fragment %q", relativePath, fragment)
			}
		}
	}

	rootNotice, err := os.ReadFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatalf("read root notice: %v", err)
	}
	publicNotice, err := os.ReadFile(filepath.Join(root, "frontend", "public", "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatalf("read frontend notice: %v", err)
	}
	if string(publicNotice) != string(rootNotice) {
		t.Error("frontend notice must remain byte-for-byte synchronized with the root notice")
	}
}
