package forwarder

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setHomeForTest 让 os.UserHomeDir 指向临时目录（Windows 读 USERPROFILE，Unix 读 HOME）。
func setHomeForTest(t *testing.T, dir string) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", dir)
	default:
		t.Setenv("HOME", dir)
	}
}

// TestOrderedSkillScanRootsCoversNewSources 验证新增工具（Trae/Windsurf/Gemini/Copilot/Cline）
// 的技能目录进入扫描根，且 Cursor 优先级最高。
func TestOrderedSkillScanRootsCoversNewSources(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	ws := t.TempDir()

	roots := orderedSkillScanRoots(ws)

	// Cursor 必须排在最前（去重时原生优先）。
	if len(roots) == 0 || roots[0].Source != SkillSourceCursor {
		t.Fatalf("first scan root source = %v, want cursor", roots[0].Source)
	}

	wantUser := map[SkillSource]string{
		SkillSourceTrae:     filepath.Join(home, ".trae", "skills"),
		SkillSourceWindsurf: filepath.Join(home, ".codeium", "windsurf", "skills"),
		SkillSourceGemini:   filepath.Join(home, ".gemini", "skills"),
		SkillSourceCopilot:  filepath.Join(home, ".copilot", "skills"),
		SkillSourceCline:    filepath.Join(home, ".cline", "skills"),
	}
	wantWorkspace := map[SkillSource]string{
		SkillSourceTrae:     filepath.Join(ws, ".trae", "skills"),
		SkillSourceWindsurf: filepath.Join(ws, ".windsurf", "skills"),
		SkillSourceCline:    filepath.Join(ws, ".cline", "skills"),
	}

	found := map[string]bool{}
	for _, root := range roots {
		found[root.Path] = true
	}
	for source, want := range wantUser {
		if !found[want] {
			t.Errorf("user-level root %v missing %q, roots=%v", source, want, rootPaths(roots))
		}
	}
	for source, want := range wantWorkspace {
		if !found[want] {
			t.Errorf("workspace-level root %v missing %q", source, want)
		}
	}
}

func rootPaths(roots []skillScanRoot) []string {
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.Path)
	}
	return paths
}

// TestOrderedSkillScanRootsEmptyWorkspace 验证 workspaceRoot 为空时只扫用户级目录。
func TestOrderedSkillScanRootsEmptyWorkspace(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)

	roots := orderedSkillScanRoots("")
	for _, root := range roots {
		if !strings.HasPrefix(root.Path, home) {
			t.Errorf("root %q does not start with home %q (workspace leaked with empty workspaceRoot)", root.Path, home)
		}
	}
}

// TestParseVSCodeMCPJSON 验证 VS Code 原生 MCP 的 servers 键解析。
func TestParseVSCodeMCPJSON(t *testing.T) {
	data := []byte(`{"servers": {"local-tools": {"command": "node", "args": ["server.js"]}}}`)
	servers, err := parseVSCodeMCPJSON(data)
	if err != nil {
		t.Fatalf("parseVSCodeMCPJSON: %v", err)
	}
	if _, ok := servers["local-tools"]; !ok {
		t.Errorf("servers missing key local-tools, got %v", servers)
	}
}

// TestParseMCPJSONRegression 验证 mcpServers 键解析未被泛化改坏（Cursor/Claude 格式）。
func TestParseMCPJSONRegression(t *testing.T) {
	data := []byte(`{"mcpServers": {"filesystem": {"command": "npx"}}}`)
	servers, err := parseMCPJSON(data)
	if err != nil {
		t.Fatalf("parseMCPJSON: %v", err)
	}
	if _, ok := servers["filesystem"]; !ok {
		t.Errorf("servers missing key filesystem, got %v", servers)
	}
}