// skill_multisource.go 实现跨工具的多源技能扫描。
//
// 原生 Cursor 只扫描 ~/.cursor/skills 和 RequestContext 带来的 descriptor；BYOK 场景下
// 客户端往往不填 descriptor，导致系统提示里 <agent_skills> 为空，模型无从得知可用技能。
// 本扫描器汇总主流编码工具（Cursor / Claude Code / Codex / ZCode / 共享 .agents / 旧 BYOK）
// 的技能目录，按 name 去重，作为 RequestContext.SkillOptions 的补充来源注入。
//
// 扫描结果仍交给 SkillStore 的稀疏激活（BM25 Top-K）筛选，不全量注入，遵守 prefix-cache-stability。
package forwarder

import (
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
	SkillSourceCursor      SkillSource = "cursor"       // ~/.cursor/skills 或 <ws>/.cursor/skills
	SkillSourceClaude      SkillSource = "claude"       // ~/.claude/skills 或 <ws>/.claude/skills
	SkillSourceCodex       SkillSource = "codex"        // ~/.codex/skills
	SkillSourceShared      SkillSource = "shared"       // ~/.agents/skills 或 <ws>/.agents/skills（跨工具共享标准）
	SkillSourceZCode       SkillSource = "zcode"        // ~/.zcode/skills 或 <ws>/.zcode/skills
	SkillSourceZCodePlugin SkillSource = "zcode-plugin" // ~/.zcode/cli/plugins/cache/*/*/skills
	SkillSourceBYOK        SkillSource = "byok"         // ~/.cursor-local-assistant-v2/skills（旧 BYOK，保持兼容）
)

// SourcedGlobalSkill 是带来源标签的 GlobalSkill。
type SourcedGlobalSkill struct {
	GlobalSkill
	Source SkillSource
}

// skillScanCache 缓存一次扫描结果，避免每个请求都扫盘（prefix-cache 要求注入稳定）。
// 用 mtime 失效：任一被扫描目录的 mtime 变化即视为过期。
var (
	skillScanCacheMu sync.RWMutex
	skillScanCache   []SourcedGlobalSkill
	skillScanCacheFp string // 缓存对应的指纹（各目录 mtime 拼接）
)

// ScanAllSkills 扫描所有工具的技能目录，按 name（小写）去重，先到先得（优先级高的在前）。
// workspaceRoot 为空时仅扫描用户级目录。结果按 name 排序，保证稳定。
func ScanAllSkills(workspaceRoot string) []SourcedGlobalSkill {
	roots := orderedSkillScanRoots(workspaceRoot)
	fingerprint := skillScanFingerprint(roots)
	if cached, ok := loadCachedSkills(fingerprint); ok {
		return cached
	}

	seen := make(map[string]struct{}, 64)
	merged := make([]SourcedGlobalSkill, 0, 64)
	for _, root := range roots {
		for _, sk := range scanOneSkillRoot(root.Path, root.Source) {
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
	skillScanCacheFp = fingerprint
	skillScanCacheMu.Unlock()
	return merged
}

// skillScanRoot 是一个待扫描目录及其来源标签。
type skillScanRoot struct {
	Path   string
	Source SkillSource
}

// orderedSkillScanRoots 按优先级返回所有待扫描目录（高优先级在前，去重时先到先得）。
// 顺序：Cursor 全局/项目 → Claude → Codex → 共享 .agents → ZCode → ZCode 插件 → 旧 BYOK。
func orderedSkillScanRoots(workspaceRoot string) []skillScanRoot {
	home, _ := os.UserHomeDir()
	home = strings.TrimSpace(home)
	ws := strings.TrimSpace(workspaceRoot)

	var roots []skillScanRoot
	add := func(path string, source SkillSource) {
		if strings.TrimSpace(path) == "" {
			return
		}
		roots = append(roots, skillScanRoot{Path: path, Source: source})
	}

	// Cursor（原生优先级最高）
	if home != "" {
		add(filepath.Join(home, ".cursor", "skills"), SkillSourceCursor)
	}
	if ws != "" {
		add(filepath.Join(ws, ".cursor", "skills"), SkillSourceCursor)
	}
	// Claude Code
	if home != "" {
		add(filepath.Join(home, ".claude", "skills"), SkillSourceClaude)
	}
	if ws != "" {
		add(filepath.Join(ws, ".claude", "skills"), SkillSourceClaude)
	}
	// Codex / GPT
	if home != "" {
		add(filepath.Join(home, ".codex", "skills"), SkillSourceCodex)
	}
	// 共享标准 .agents（跨工具）
	if home != "" {
		add(filepath.Join(home, ".agents", "skills"), SkillSourceShared)
	}
	if ws != "" {
		add(filepath.Join(ws, ".agents", "skills"), SkillSourceShared)
	}
	// ZCode
	if home != "" {
		add(filepath.Join(home, ".zcode", "skills"), SkillSourceZCode)
	}
	if ws != "" {
		add(filepath.Join(ws, ".zcode", "skills"), SkillSourceZCode)
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
	return roots
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
func scanOneSkillRoot(root string, source SkillSource) []SourcedGlobalSkill {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	skills := make([]SourcedGlobalSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(root, entry.Name(), "SKILL.md")
		global, ok := readSkillFile(skillPath)
		if !ok {
			continue
		}
		skills = append(skills, SourcedGlobalSkill{GlobalSkill: global, Source: source})
	}
	return skills
}

// skillScanFingerprint 把各目录的 mtime 拼成指纹，任一变化即让缓存失效。
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
			b.WriteString(info.ModTime().Format("20060102T150405"))
		}
		b.WriteByte('\n')
	}
	b.WriteString("os=")
	b.WriteString(runtime.GOOS)
	return b.String()
}

// loadCachedSkills 命中缓存时返回缓存结果。
func loadCachedSkills(fingerprint string) ([]SourcedGlobalSkill, bool) {
	skillScanCacheMu.RLock()
	defer skillScanCacheMu.RUnlock()
	if skillScanCacheFp == fingerprint && skillScanCache != nil {
		out := make([]SourcedGlobalSkill, len(skillScanCache))
		copy(out, skillScanCache)
		return out, true
	}
	return nil, false
}

// InvalidateSkillScanCache 清除扫描缓存，供管理界面「重新扫描」按钮调用。
func InvalidateSkillScanCache() {
	skillScanCacheMu.Lock()
	skillScanCache = nil
	skillScanCacheFp = ""
	skillScanCacheMu.Unlock()
}

// SkillSnapshotItem 是管理界面展示用的单个技能快照（含来源标签）。
type SkillSnapshotItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FullPath    string `json:"fullPath"`
	Source      string `json:"source"`
}

// SnapshotSourcedSkills 返回当前所有已扫描技能的快照（带来源），供管理界面分类展示。
// workspaceRoot 为空时仅扫描用户级目录。
func SnapshotSourcedSkills(workspaceRoot string) []SkillSnapshotItem {
	sourced := ScanAllSkills(workspaceRoot)
	out := make([]SkillSnapshotItem, 0, len(sourced))
	for _, sk := range sourced {
		out = append(out, SkillSnapshotItem{
			Name:        sk.Name,
			Description: sk.Description,
			FullPath:    sk.FullPath,
			Source:      string(sk.Source),
		})
	}
	return out
}
