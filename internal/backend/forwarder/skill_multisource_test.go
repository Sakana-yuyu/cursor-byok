package forwarder

import (
	"os"
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
		SkillSourceCursor:   filepath.Join(ws, ".cursor", "skills"),
		SkillSourceTrae:     filepath.Join(ws, ".trae", "skills"),
		SkillSourceWindsurf: filepath.Join(ws, ".windsurf", "skills"),
		SkillSourceClaude:   filepath.Join(ws, ".claude", "skills"),
		SkillSourceGemini:   filepath.Join(ws, ".gemini", "skills"),
		SkillSourceCline:    filepath.Join(ws, ".cline", "skills"),
		SkillSourceShared:   filepath.Join(ws, ".agents", "skills"),
		SkillSourceZCode:    filepath.Join(ws, ".zcode", "skills"),
	}
	wantUser[SkillSourceCursor] = filepath.Join(home, ".cursor", "skills")
	wantUser[SkillSourceClaude] = filepath.Join(home, ".claude", "skills")
	wantUser[SkillSourceShared] = filepath.Join(home, ".agents", "skills")
	wantUser[SkillSourceZCode] = filepath.Join(home, ".zcode", "skills")

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
		workspaceIndex := skillScanRootIndex(roots, want)
		userIndex := skillScanRootIndex(roots, wantUser[source])
		if workspaceIndex < 0 || userIndex < 0 || workspaceIndex >= userIndex {
			t.Errorf("source %v order workspace=%d user=%d, want workspace first", source, workspaceIndex, userIndex)
		}
	}
}

func skillScanRootIndex(roots []skillScanRoot, path string) int {
	for index, root := range roots {
		if root.Path == path {
			return index
		}
	}
	return -1
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

func TestSkillWorkspaceOverridesUserForSameSource(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()

	writeScannerTestSkill(t, filepath.Join(home, ".cursor", "skills"), "shared-name", "user definition")
	writeScannerTestSkill(t, filepath.Join(workspace, ".cursor", "skills"), "shared-name", "workspace definition")

	InvalidateSkillScanCache()
	t.Cleanup(InvalidateSkillScanCache)
	skilled := ScanAllSkills(workspace)
	for _, skill := range skilled {
		if skill.Name != "shared-name" {
			continue
		}
		if skill.Description != "workspace definition" {
			t.Fatalf("shared-name description = %q, want workspace definition", skill.Description)
		}
		wantPrefix := filepath.Join(workspace, ".cursor", "skills")
		if !strings.HasPrefix(skill.FullPath, wantPrefix) {
			t.Fatalf("shared-name path = %q, want workspace prefix %q", skill.FullPath, wantPrefix)
		}
		return
	}
	t.Fatal("shared-name skill was not discovered")
}

func TestGeminiWorkspaceSkillsAreDiscovered(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()
	writeScannerTestSkill(t, filepath.Join(workspace, ".gemini", "skills"), "gemini-project", "Gemini project skill")

	InvalidateSkillScanCache()
	t.Cleanup(InvalidateSkillScanCache)
	skilled := ScanAllSkills(workspace)
	wantSources := map[string]SkillSource{
		"gemini-project": SkillSourceGemini,
	}
	for _, skill := range skilled {
		wantSource, wanted := wantSources[skill.Name]
		if !wanted {
			continue
		}
		if skill.Source != wantSource {
			t.Errorf("skill %q source = %q, want %q", skill.Name, skill.Source, wantSource)
		}
		delete(wantSources, skill.Name)
	}
	if len(wantSources) != 0 {
		t.Fatalf("workspace skills not discovered: %v; skills=%+v", wantSources, skilled)
	}
}

func writeScannerTestSkill(t *testing.T, root, name, description string) {
	t.Helper()
	path := filepath.Join(root, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create skill directory: %v", err)
	}
	manifest := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# Test Skill\n"
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write skill manifest: %v", err)
	}
}

func TestCodexSystemSkillsAreDiscoveredWithoutContainerDiagnostic(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	manifestPath := filepath.Join(home, ".codex", "skills", ".system", "imagegen", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("create system skill directory: %v", err)
	}
	manifest := "---\nname: imagegen\ndescription: Generate raster images for tests.\n---\n\n# Image Generation\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write system skill manifest: %v", err)
	}

	InvalidateSkillScanCache()
	t.Cleanup(InvalidateSkillScanCache)
	skills, diagnostics := scanAllSkillRecords("")

	foundImagegen := false
	for _, skill := range skills {
		if skill.Name == "imagegen" {
			foundImagegen = true
			if skill.Source != SkillSourceCodex {
				t.Fatalf("imagegen source = %q, want %q", skill.Source, SkillSourceCodex)
			}
		}
	}
	if !foundImagegen {
		t.Fatalf("Codex system skill imagegen was not discovered: %+v", skills)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Name == ".system" {
			t.Fatalf("Codex system container was reported as an invalid skill: %+v", diagnostic)
		}
	}
}

func TestProjectGoalSkillIsDiscoverable(t *testing.T) {
	workspaceRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve workspace root: %v", err)
	}
	root := filepath.Join(workspaceRoot, ".agents", "skills")
	skills, diagnostics := scanOneSkillRootWithDiagnostics(root, SkillSourceShared)
	if len(diagnostics) > 0 {
		t.Fatalf("project skills contain validation diagnostics: %+v", diagnostics)
	}

	for _, skill := range skills {
		if skill.Name == "goal-loop" {
			if !strings.Contains(skill.Description, "/goal") {
				t.Fatalf("goal-loop description must expose /goal trigger, got %q", skill.Description)
			}
			return
		}
	}
	t.Fatalf("goal-loop skill missing from %q", root)
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
