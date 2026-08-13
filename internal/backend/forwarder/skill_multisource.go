// skill_multisource.go 实现跨工具的多源技能扫描。
//
// 原生 Cursor 只扫描 ~/.cursor/skills 和 RequestContext 带来的 descriptor；BYOK 场景下
// 客户端往往不填 descriptor，导致系统提示里 <agent_skills> 为空，模型无从得知可用技能。
// 本扫描器汇总主流编码工具（Cursor 用户目录与客户端内置目录 / Trae / Windsurf /
// Claude Code / Codex / Gemini / Copilot / Cline / ZCode / 共享 .agents / 旧 BYOK）的
// 技能目录，按 name 去重，作为 RequestContext.SkillOptions 的补充来源注入。
//
// 扫描结果仍交给 SkillStore 的稀疏激活（BM25 Top-K）筛选，不全量注入，遵守 prefix-cache-stability。
package forwarder

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// SkillSource 标识一个技能来自哪个工具/位置，便于管理界面分类展示。
type SkillSource string

const (
	SkillSourceCursor SkillSource = "cursor" // ~/.cursor/skills 或 <ws>/.cursor/skills
	// SkillSourceCursorBuiltin 是 Cursor 随客户端下发的内置技能（canvas / automate / sdk 等）。
	// 只登记 ~/.cursor/skills-cursor 这一个确切目录，不做 ~/.cursor/skills* 通配，
	// 避免 Cursor 升级新增目录时系统提示漂移、破坏前缀缓存。
	SkillSourceCursorBuiltin SkillSource = "cursor-builtin" // ~/.cursor/skills-cursor
	SkillSourceTrae          SkillSource = "trae"           // ~/.trae/skills 或 <ws>/.trae/skills
	SkillSourceWindsurf      SkillSource = "windsurf"       // ~/.codeium/windsurf/skills 或 <ws>/.windsurf/skills
	SkillSourceClaude        SkillSource = "claude"         // ~/.claude/skills 或 <ws>/.claude/skills
	SkillSourceCodex         SkillSource = "codex"          // ~/.codex/skills
	SkillSourceGemini        SkillSource = "gemini"         // ~/.gemini/skills 或 <ws>/.gemini/skills
	SkillSourceCopilot       SkillSource = "copilot"        // ~/.copilot/skills
	SkillSourceCline         SkillSource = "cline"          // ~/.cline/skills 或 <ws>/.cline/skills
	SkillSourceShared        SkillSource = "shared"         // ~/.agents/skills 或 <ws>/.agents/skills（跨工具共享标准）
	SkillSourceZCode         SkillSource = "zcode"          // ~/.zcode/skills 或 <ws>/.zcode/skills
	SkillSourceZCodePlugin   SkillSource = "zcode-plugin"   // ~/.zcode/cli/plugins/cache/*/*/skills
	SkillSourceBYOK          SkillSource = "byok"           // ~/.cursor-local-assistant-v2/skills（旧 BYOK，保持兼容）
)

// SourcedGlobalSkill 是带来源标签的 GlobalSkill。
type SourcedGlobalSkill struct {
	GlobalSkill
	Source SkillSource
}

// skillScanCache 缓存一次扫描结果，避免每个请求都扫盘（prefix-cache 要求注入稳定）。
// 用 mtime 失效：任一被扫描目录的 mtime 变化即视为过期。
var (
	skillScanCacheMu          sync.RWMutex
	skillScanCache            []SourcedGlobalSkill
	skillScanCacheDiagnostics []SourcedGlobalSkill
	skillScanCacheFp          string // 缓存对应的指纹（各目录 mtime 拼接）
)

// ScanAllSkills 扫描所有工具的技能目录，按 name（小写）去重，先到先得（优先级高的在前）。
// workspaceRoot 为空时仅扫描用户级目录。结果按 name 排序，保证稳定。
func ScanAllSkills(workspaceRoot string) []SourcedGlobalSkill {
	skills, _ := scanAllSkillRecords(workspaceRoot)
	return skills
}

// ScanAllSkillDiagnostics returns invalid skill records and their validation diagnostics.
func ScanAllSkillDiagnostics(workspaceRoot string) []SourcedGlobalSkill {
	_, diagnostics := scanAllSkillRecords(workspaceRoot)
	return diagnostics
}

func scanAllSkillRecords(workspaceRoot string) ([]SourcedGlobalSkill, []SourcedGlobalSkill) {
	roots := orderedSkillScanRoots(workspaceRoot)
	fingerprint := skillScanFingerprint(roots)
	if cached, diagnostics, ok := loadCachedSkillScan(fingerprint); ok {
		return cached, diagnostics
	}

	seen := make(map[string]struct{}, 64)
	merged := make([]SourcedGlobalSkill, 0, 64)
	diagnostics := make([]SourcedGlobalSkill, 0)
	for _, root := range roots {
		valid, invalid := scanOneSkillRootWithDiagnostics(root.Path, root.Source)
		diagnostics = append(diagnostics, invalid...)
		for _, sk := range valid {
			key := strings.ToLower(strings.TrimSpace(sk.Name))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, sk)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Name < merged[j].Name
	})

	skillScanCacheMu.Lock()
	skillScanCache = merged
	skillScanCacheDiagnostics = diagnostics
	skillScanCacheFp = fingerprint
	skillScanCacheMu.Unlock()
	return merged, diagnostics
}

// skillScanRoot 是一个待扫描目录及其来源标签。
type skillScanRoot struct {
	Path   string
	Source SkillSource
}

// orderedSkillScanRoots 按优先级返回所有待扫描目录（高优先级在前，去重时先到先得）。
// 所有工作区根先于用户根；每个作用域内保持 Cursor → Cursor 内置 → Trae → Windsurf →
// Claude → Codex → Gemini → Copilot → Cline → 共享 .agents → ZCode → ZCode 插件 → 旧 BYOK。
func orderedSkillScanRoots(workspaceRoot string) []skillScanRoot {
	home, _ := os.UserHomeDir()
	home = strings.TrimSpace(home)
	ws := strings.TrimSpace(workspaceRoot)

	var workspaceRoots []skillScanRoot
	var roots []skillScanRoot
	add := func(path string, source SkillSource) {
		if strings.TrimSpace(path) == "" {
			return
		}
		roots = append(roots, skillScanRoot{Path: path, Source: source})
	}
	addWorkspace := func(path string, source SkillSource) {
		if strings.TrimSpace(path) == "" {
			return
		}
		workspaceRoots = append(workspaceRoots, skillScanRoot{Path: path, Source: source})
	}

	// Cursor（原生优先级最高）
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".cursor", "skills"), SkillSourceCursor)
	}
	if home != "" {
		add(filepath.Join(home, ".cursor", "skills"), SkillSourceCursor)
		// Cursor 客户端内置技能目录，与 ~/.cursor/skills 同级但不同名；canvas 技能就在这里。
		// 排在用户自建的 ~/.cursor/skills 之后，同名时让用户自建版本覆盖官方内置版本。
		add(filepath.Join(home, ".cursor", "skills-cursor"), SkillSourceCursorBuiltin)
	}
	// Trae（IDE，技能放项目 .trae/skills，全局目录兼容）
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".trae", "skills"), SkillSourceTrae)
	}
	if home != "" {
		add(filepath.Join(home, ".trae", "skills"), SkillSourceTrae)
	}
	// Windsurf（IDE，全局在 ~/.codeium/windsurf/skills，项目在 .windsurf/skills）
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".windsurf", "skills"), SkillSourceWindsurf)
	}
	if home != "" {
		add(filepath.Join(home, ".codeium", "windsurf", "skills"), SkillSourceWindsurf)
	}
	// Claude Code
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".claude", "skills"), SkillSourceClaude)
	}
	if home != "" {
		add(filepath.Join(home, ".claude", "skills"), SkillSourceClaude)
	}
	// Codex / GPT
	if home != "" {
		add(filepath.Join(home, ".codex", "skills"), SkillSourceCodex)
		add(filepath.Join(home, ".codex", "skills", ".system"), SkillSourceCodex)
	}
	// Gemini CLI
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".gemini", "skills"), SkillSourceGemini)
	}
	if home != "" {
		add(filepath.Join(home, ".gemini", "skills"), SkillSourceGemini)
	}
	// GitHub Copilot / VS Code
	if home != "" {
		add(filepath.Join(home, ".copilot", "skills"), SkillSourceCopilot)
	}
	// Cline
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".cline", "skills"), SkillSourceCline)
	}
	if home != "" {
		add(filepath.Join(home, ".cline", "skills"), SkillSourceCline)
	}
	// 共享标准 .agents（跨工具）
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".agents", "skills"), SkillSourceShared)
	}
	if home != "" {
		add(filepath.Join(home, ".agents", "skills"), SkillSourceShared)
	}
	// ZCode
	if ws != "" {
		addWorkspace(filepath.Join(ws, ".zcode", "skills"), SkillSourceZCode)
	}
	if home != "" {
		add(filepath.Join(home, ".zcode", "skills"), SkillSourceZCode)
	}
	// ZCode 插件缓存（glob 一层：~/.zcode/cli/plugins/cache/<marketplace>/<plugin>/skills）
	if home != "" {
		for _, pluginSkills := range globZCodePluginSkillDirs(home) {
			add(pluginSkills, SkillSourceZCodePlugin)
		}
	}
	// 旧 BYOK 目录（保持向后兼容）
	if home != "" {
		add(filepath.Join(home, ".cursor-local-assistant-v2", "skills"), SkillSourceBYOK)
	}
	return append(workspaceRoots, roots...)
}

// globZCodePluginSkillDirs 枚举 ~/.zcode/cli/plugins/cache/*/*/skills 目录。
func globZCodePluginSkillDirs(home string) []string {
	cacheRoot := filepath.Join(home, ".zcode", "cli", "plugins", "cache")
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, marketplace := range entries {
		if !marketplace.IsDir() {
			continue
		}
		plugins, err := os.ReadDir(filepath.Join(cacheRoot, marketplace.Name()))
		if err != nil {
			continue
		}
		for _, plugin := range plugins {
			if !plugin.IsDir() {
				continue
			}
			skillsDir := filepath.Join(cacheRoot, marketplace.Name(), plugin.Name(), "skills")
			if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
				dirs = append(dirs, skillsDir)
			}
		}
	}
	return dirs
}

// scanOneSkillRoot 扫描单个技能根目录，返回其下所有有效技能。
// 目录结构：<root>/<skillName>/SKILL.md。复用 readSkillFile 解析 frontmatter。
//
// 处理符号链接：ZCode/Claude 等工具用 symlink 指向共享 .agents/skills 下的技能目录，
// entry.IsDir() 对 symlink 返回 false（基于 Lstat），因此需用 os.Stat 跟踪链接判断目标是否目录。
func scanOneSkillRoot(root string, source SkillSource) []SourcedGlobalSkill {
	skills, _ := scanOneSkillRootWithDiagnostics(root, source)
	return skills
}

func scanOneSkillRootWithDiagnostics(root string, source SkillSource) ([]SourcedGlobalSkill, []SourcedGlobalSkill) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	skills := make([]SourcedGlobalSkill, 0, len(entries))
	diagnostics := make([]SourcedGlobalSkill, 0)
	for _, entry := range entries {
		if source == SkillSourceCodex && entry.Name() == ".system" {
			continue
		}
		entryPath := filepath.Join(root, entry.Name())
		// entry.IsDir() 对符号链接返回 false；用 os.Stat 跟踪链接，判断目标是否目录。
		isDir := entry.IsDir()
		if !isDir {
			if info, err := os.Stat(entryPath); err == nil && info.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		skillPath := filepath.Join(entryPath, "SKILL.md")
		global, ok := readSkillFile(skillPath)
		if !ok {
			if len(global.Diagnostics) > 0 {
				if strings.TrimSpace(global.Name) == "" {
					global.Name = entry.Name()
				}
				diagnostics = append(diagnostics, SourcedGlobalSkill{GlobalSkill: global, Source: source})
			}
			continue
		}
		skills = append(skills, SourcedGlobalSkill{GlobalSkill: global, Source: source})
	}
	return skills, diagnostics
}

// skillScanFingerprint includes each SKILL.md metadata so in-place edits invalidate the cache.
func skillScanFingerprint(roots []skillScanRoot) string {
	var b strings.Builder
	for _, root := range roots {
		b.WriteString(string(root.Source))
		b.WriteByte('|')
		b.WriteString(root.Path)
		info, err := os.Stat(root.Path)
		if err != nil {
			b.WriteString("missing")
		} else {
			b.WriteString(fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()))
		}
		b.WriteByte('\n')
		entries, _ := os.ReadDir(root.Path)
		for _, entry := range entries {
			skillPath := filepath.Join(root.Path, entry.Name(), "SKILL.md")
			info, statErr := os.Stat(skillPath)
			if statErr != nil || info.IsDir() {
				continue
			}
			b.WriteString(skillPath)
			b.WriteByte('|')
			b.WriteString(fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()))
			b.WriteByte('\n')
		}
	}
	b.WriteString("os=")
	b.WriteString(runtime.GOOS)
	return b.String()
}

// loadCachedSkills 命中缓存时返回缓存结果。
func loadCachedSkills(fingerprint string) ([]SourcedGlobalSkill, bool) {
	skills, _, ok := loadCachedSkillScan(fingerprint)
	return skills, ok
}

func loadCachedSkillScan(fingerprint string) ([]SourcedGlobalSkill, []SourcedGlobalSkill, bool) {
	skillScanCacheMu.RLock()
	defer skillScanCacheMu.RUnlock()
	if skillScanCacheFp == fingerprint && skillScanCache != nil {
		out := make([]SourcedGlobalSkill, len(skillScanCache))
		copy(out, skillScanCache)
		diagnostics := make([]SourcedGlobalSkill, len(skillScanCacheDiagnostics))
		copy(diagnostics, skillScanCacheDiagnostics)
		return out, diagnostics, true
	}
	return nil, nil, false
}

// InvalidateSkillScanCache 清除扫描缓存，供管理界面「重新扫描」按钮调用。
func InvalidateSkillScanCache() {
	skillScanCacheMu.Lock()
	skillScanCache = nil
	skillScanCacheDiagnostics = nil
	skillScanCacheFp = ""
	skillScanCacheMu.Unlock()
}

// SkillSnapshotItem 是管理界面展示用的单个技能快照（含来源标签）。
type SkillSnapshotItem struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Version     string                    `json:"version,omitempty"`
	ContentHash string                    `json:"contentHash,omitempty"`
	Diagnostics []SkillManifestDiagnostic `json:"diagnostics,omitempty"`
	Valid       bool                      `json:"valid"`
	FullPath    string                    `json:"fullPath"`
	Source      string                    `json:"source"`
}

// SnapshotSourcedSkills 返回当前所有已扫描技能的快照（带来源），供管理界面分类展示。
// workspaceRoot 为空时仅扫描用户级目录。
func SnapshotSourcedSkills(workspaceRoot string) []SkillSnapshotItem {
	sourced := ScanAllSkills(workspaceRoot)
	diagnostics := ScanAllSkillDiagnostics(workspaceRoot)
	out := make([]SkillSnapshotItem, 0, len(sourced)+len(diagnostics))
	for _, sk := range sourced {
		out = append(out, SkillSnapshotItem{
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			ContentHash: sk.ContentHash,
			Valid:       true,
			FullPath:    sk.FullPath,
			Source:      string(sk.Source),
		})
	}
	for _, sk := range diagnostics {
		out = append(out, skillDiagnosticSnapshotItem(sk))
	}
	return out
}

func skillDiagnosticSnapshotItem(sk SourcedGlobalSkill) SkillSnapshotItem {
	return SkillSnapshotItem{
		Name:        sk.Name,
		Description: sk.Description,
		Version:     sk.Version,
		ContentHash: sk.ContentHash,
		Diagnostics: append([]SkillManifestDiagnostic(nil), sk.Diagnostics...),
		Valid:       false,
		FullPath:    sk.FullPath,
		Source:      string(sk.Source),
	}
}
