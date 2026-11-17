package forwarder

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	promptengine "cursor/internal/backend/agent/prompt"
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

// TestOrderedSkillScanRootsCoversCursorBuiltinSkills 验证 Cursor 客户端内置技能目录
// ~/.cursor/skills-cursor（canvas 技能所在）进入扫描根，且排在用户自建的
// ~/.cursor/skills 之后，同名时让用户自建版本覆盖官方内置版本。
func TestOrderedSkillScanRootsCoversCursorBuiltinSkills(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)

	roots := orderedSkillScanRoots("")

	builtinPath := filepath.Join(home, ".cursor", "skills-cursor")
	builtinIndex := skillScanRootIndex(roots, builtinPath)
	if builtinIndex < 0 {
		t.Fatalf("cursor builtin root %q missing, roots=%v", builtinPath, rootPaths(roots))
	}
	if roots[builtinIndex].Source != SkillSourceCursorBuiltin {
		t.Errorf("cursor builtin root source = %q, want %q", roots[builtinIndex].Source, SkillSourceCursorBuiltin)
	}
	userIndex := skillScanRootIndex(roots, filepath.Join(home, ".cursor", "skills"))
	if userIndex < 0 || userIndex >= builtinIndex {
		t.Errorf("root order user=%d builtin=%d, want user first", userIndex, builtinIndex)
	}
	for _, root := range roots {
		if root.Source == SkillSourceCursorBuiltin && root.Path != builtinPath {
			t.Errorf("unexpected extra cursor builtin root %q; only the exact skills-cursor dir may be scanned", root.Path)
		}
	}
}

// TestScanAllSkillsPicksUpCursorBuiltinSkill 验证内置目录里的技能真的被扫出来并带对来源。
func TestScanAllSkillsPicksUpCursorBuiltinSkill(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	InvalidateSkillScanCache()
	t.Cleanup(InvalidateSkillScanCache)

	canvasDir := filepath.Join(home, ".cursor", "skills-cursor", "canvas")
	if err := os.MkdirAll(canvasDir, 0o755); err != nil {
		t.Fatalf("mkdir canvas skill: %v", err)
	}
	manifest := "---\nname: canvas\ndescription: A Cursor Canvas is a live React app.\n---\n\n# Canvas\n"
	if err := os.WriteFile(filepath.Join(canvasDir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write canvas skill: %v", err)
	}

	skills := ScanAllSkills("")
	found := false
	for _, skill := range skills {
		if !strings.EqualFold(skill.Name, "canvas") {
			continue
		}
		found = true
		if skill.Source != SkillSourceCursorBuiltin {
			t.Errorf("canvas skill source = %q, want %q", skill.Source, SkillSourceCursorBuiltin)
		}
	}
	if !found {
		t.Fatal("canvas skill was not discovered under ~/.cursor/skills-cursor")
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

func TestSkillWorkspaceOverridesUserAcrossSources(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	workspace := t.TempDir()

	writeScannerTestSkill(t, filepath.Join(home, ".cursor", "skills"), "shared-name", "user Cursor definition")
	writeScannerTestSkill(t, filepath.Join(workspace, ".gemini", "skills"), "shared-name", "workspace Gemini definition")

	InvalidateSkillScanCache()
	t.Cleanup(InvalidateSkillScanCache)
	skilled := ScanAllSkills(workspace)
	for _, skill := range skilled {
		if skill.Name != "shared-name" {
			continue
		}
		if skill.Source != SkillSourceGemini {
			t.Fatalf("shared-name source = %q, want %q", skill.Source, SkillSourceGemini)
		}
		if skill.Description != "workspace Gemini definition" {
			t.Fatalf("shared-name description = %q, want workspace Gemini definition", skill.Description)
		}
		wantPrefix := filepath.Join(workspace, ".gemini", "skills")
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

func TestSkillMentionOffsetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		skill string
		want  int
	}{
		{"bare", "spec-design", "spec-design", 0},
		{"slash Chinese suffix", "/spec-design中文", "spec-design", 1},
		{"dollar", "$spec-design", "spec-design", 1},
		{"Chinese adjacent", "请用spec-design中文", "spec-design", len("请用")},
		{"caller lowercases", "/SPEC-DESIGN中文", "Spec-Design", 1},
		{"ASCII prefix", "prefixspec-design", "spec-design", -1},
		{"ASCII suffix", "spec-designer", "spec-design", -1},
		{"hyphen prefix", "my-spec-design", "spec-design", -1},
		{"hyphen suffix", "spec-design-extra", "spec-design", -1},
		{"underscore prefix", "my_spec-design", "spec-design", -1},
		{"underscore suffix", "spec-design_extra", "spec-design", -1},
		{"dot prefix", "my.spec-design", "spec-design", -1},
		{"dot suffix", "spec-design.md", "spec-design", -1},
		{"digit prefix", "1spec-design", "spec-design", -1},
		{"digit suffix", "spec-design2", "spec-design", -1},
		{"skip invalid first match", "spec-designer /spec-design", "spec-design", len("spec-designer /")},
		{"first valid match", "$spec-design /spec-design", "spec-design", 1},
		{"empty name", "/spec-design", "", -1},
		{"empty query", "", "spec-design", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := skillMentionOffset(strings.ToLower(tc.query), strings.ToLower(tc.skill)); got != tc.want {
				t.Fatalf("skillMentionOffset(%q, %q) = %d, want %d", tc.query, tc.skill, got, tc.want)
			}
		})
	}
}

// All scan roots and legacy data are temporary; never inspect the real user HOME.
func newExplicitSkillTestStore(t *testing.T) (*SkillStore, map[string]bool, string) {
	t.Helper()
	home := t.TempDir()
	setHomeForTest(t, home)
	root := filepath.Join(home, ".claude", "skills")
	enabled := make(map[string]bool)
	for _, name := range []string{"spec-design", "review-notes", "release-guide", "trace-check", "build-plan", "find-skills"} {
		writeScannerTestSkill(t, root, name, `quartz & <notes> "quoted"`)
		enabled[name] = true
	}
	for _, name := range []string{"noise-alpha", "noise-beta", "noise-gamma", "noise-delta"} {
		writeScannerTestSkill(t, root, name, "spec design compiler regression concurrency database browser networking latency profiling memory storage serialization migration deployment")
		enabled[name] = true
	}
	InvalidateSkillScanCache()
	t.Cleanup(InvalidateSkillScanCache)
	store := NewSkillStore(t.TempDir())
	store.SetScanSettings(true, map[string]bool{string(SkillSourceClaude): true}, enabled)
	return store, enabled, filepath.Join(root, "spec-design", "SKILL.md")
}

func activatedTestSkillNames(skills []GlobalSkill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

func TestExplicitSkillMentionOutranksHigherScores(t *testing.T) {
	store, _, _ := newExplicitSkillTestStore(t)
	query := "compiler regression concurrency database browser networking latency profiling memory storage serialization migration deployment /SPEC-DESIGN中文"
	skills, err := store.ScanForWorkspace("")
	if err != nil {
		t.Fatal(err)
	}
	idx := store.activator.ensureIndex(skills)
	terms := tokenize(query)
	targetScore := bm25Score(idx, idx.nameToDoc["spec-design"], terms)
	for _, name := range []string{"noise-alpha", "noise-beta", "noise-gamma", "noise-delta"} {
		if score := bm25Score(idx, idx.nameToDoc[name], terms); score <= targetScore {
			t.Fatalf("fixture %s score %g must exceed spec-design score %g", name, score, targetScore)
		}
	}
	got := activatedTestSkillNames(store.activator.Activate(query, nil))
	if len(got) != 1+activatedSkillsTopK || got[0] != "find-skills" || got[1] != "spec-design" {
		t.Fatalf("activation = %v, want meta then explicit skill ahead of higher-score candidates within normal budget", got)
	}
}

func TestExplicitSkillMentionsExceedTopKAndCompileStably(t *testing.T) {
	store, enabled, _ := newExplicitSkillTestStore(t)
	query := "/TRACE-CHECK中文 $spec-design /release-guide /build-plan $review-notes /trace-check compiler regression"
	want := []string{"find-skills", "trace-check", "spec-design", "release-guide", "build-plan", "review-notes"}
	var firstPrompt string
	for attempt := 0; attempt < 5; attempt++ {
		// Rebuild both scanner and activator state, as well as compiling repeatedly.
		InvalidateSkillScanCache()
		rebuilt := NewSkillStore(store.root)
		rebuilt.SetScanSettings(true, map[string]bool{string(SkillSourceClaude): true}, enabled)
		got := activatedTestSkillNames(rebuilt.activator.Activate(query, nil))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attempt %d activation = %v, want %v (all explicit skills, in mention order)", attempt, got, want)
		}
		for compile := 0; compile < 2; compile++ {
			prompt, count, err := rebuilt.BuildActivatedSkillsPromptSection(query, nil)
			if err != nil || count != len(want) {
				t.Fatalf("attempt %d compile %d: count=%d err=%v, want %d", attempt, compile, count, err, len(want))
			}
			if attempt == 0 && compile == 0 {
				firstPrompt = prompt
			} else if prompt != firstPrompt {
				t.Fatalf("attempt %d compile %d changed prompt", attempt, compile)
			}
		}
	}
}

func TestExplicitSkillMentionRespectsScanSettingsAndExclusions(t *testing.T) {
	store, enabled, targetPath := newExplicitSkillTestStore(t)
	for _, fallback := range []bool{false, true} {
		mode := "activated"
		if fallback {
			mode = "full-list fallback"
		}
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				scan      bool
				sources   map[string]bool
				disabled  bool
				excluded  bool
				wantSkill bool
				wantEmpty bool
			}{
				{name: "enabled baseline", scan: true, wantSkill: true},
				{name: "skill disabled", scan: true, disabled: true},
				{name: "source disabled", scan: true, sources: map[string]bool{string(SkillSourceClaude): false}, wantEmpty: true},
				{name: "scan disabled", scan: false, wantEmpty: true},
				{name: "client path excluded", scan: true, excluded: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					candidate := NewSkillStore(store.root)
					if fallback {
						candidate.activator = nil
					}
					settings := cloneSkillSettingMap(enabled)
					settings["spec-design"] = !tc.disabled
					candidate.SetScanSettings(tc.scan, tc.sources, settings)
					var excluded map[string]struct{}
					if tc.excluded {
						excluded = map[string]struct{}{skillPathKey(targetPath): {}}
					}
					prompt, count, err := candidate.buildActivatedSkillsPromptSectionForWorkspaceExcluding("", "/spec-design中文 $spec-design compiler regression", nil, excluded)
					if err != nil {
						t.Fatal(err)
					}
					if got := strings.Contains(prompt, targetPath); got != tc.wantSkill {
						t.Fatalf("target present=%v, want %v; count=%d prompt=%s", got, tc.wantSkill, count, prompt)
					}
					if tc.wantEmpty && (prompt != "" || count != 0) {
						t.Fatalf("disabled scan/source returned count=%d prompt=%s", count, prompt)
					}
					if !tc.wantEmpty && count == 0 {
						t.Fatal("fixture must retain other enabled skills")
					}
					if tc.wantSkill && strings.Count(prompt, targetPath) != 1 {
						t.Fatal("repeated explicit mentions duplicated the skill")
					}
				})
			}
		})
	}
}

func TestScannedSkillPromptUsesSharedInstructionsAndCatalog(t *testing.T) {
	store, _, _ := newExplicitSkillTestStore(t)
	skills, err := store.ScanForWorkspace("")
	if err != nil {
		t.Fatal(err)
	}
	prompt, count, err := store.BuildAgentSkillsPromptSectionForWorkspace("")
	if err != nil || count != len(skills) || count == 0 {
		t.Fatalf("scanned prompt count=%d skills=%d err=%v", count, len(skills), err)
	}
	want, _ := buildAgentSkillsSectionFromList(skills)
	if prompt != want {
		t.Fatal("workspace scan did not reuse the shared list renderer")
	}
	assertTestSkillCatalog(t, prompt, skills)
}

func TestSkillPromptCatalogEscapesXML(t *testing.T) {
	skills := []GlobalSkill{
		{Name: "xml", FullPath: `/tmp/a&b/<skill>"'/SKILL.md`, Description: `Use <notes> & "quotes" and 'apostrophes'.`},
		{Name: `fallback<&>"'`, FullPath: "/tmp/fallback/SKILL.md", Description: "  "},
	}
	prompt, count := buildAgentSkillsSectionFromList(skills)
	if count != len(skills) {
		t.Fatalf("count=%d, want %d", count, len(skills))
	}
	assertTestSkillCatalog(t, prompt, skills)
	if prompt, count := buildAgentSkillsSectionFromList(nil); prompt != "" || count != 0 {
		t.Fatalf("empty list produced count=%d prompt=%q", count, prompt)
	}
}

func assertTestSkillCatalog(t *testing.T, prompt string, skills []GlobalSkill) {
	t.Helper()
	prefix := "<agent_skills>\n" + promptengine.SkillUsageInstructions + "\n"
	if !strings.HasPrefix(prompt, prefix) || !strings.HasSuffix(prompt, "\n</available_skills>\n</agent_skills>") {
		t.Fatalf("missing shared instructions or nested catalog structure: %s", prompt)
	}
	if strings.Count(prompt, promptengine.SkillUsageInstructions) != 1 || strings.Contains(prompt, "Only use skills listed below") {
		t.Fatal("instructions duplicated or obsolete exclusive allowlist retained")
	}
	// The prose intentionally mentions literal XML tag names; decode only the actual catalog.
	catalogXML := strings.TrimSuffix(strings.TrimPrefix(prompt, prefix), "\n</agent_skills>")
	var catalog struct {
		XMLName     xml.Name `xml:"available_skills"`
		Description string   `xml:"description,attr"`
		Skills      []struct {
			FullPath    string `xml:"fullPath,attr"`
			Description string `xml:",chardata"`
		} `xml:"agent_skill"`
	}
	if err := xml.Unmarshal([]byte(catalogXML), &catalog); err != nil {
		t.Fatalf("catalog XML is not escaped correctly: %v\n%s", err, catalogXML)
	}
	if catalog.Description == "" || len(catalog.Skills) != len(skills) {
		t.Fatalf("catalog description=%q entries=%d, want %d", catalog.Description, len(catalog.Skills), len(skills))
	}
	for i, skill := range skills {
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = skill.Name
		}
		if catalog.Skills[i].FullPath != skill.FullPath || catalog.Skills[i].Description != description {
			t.Fatalf("catalog entry %d = %+v, want path=%q description=%q", i, catalog.Skills[i], skill.FullPath, description)
		}
	}
}
