// skill_store.go 扫描 app-data 技能目录，向每个 agent 对话的系统 prompt 注入全局技能。
//
// 技能目录结构：<root>/<skillName>/SKILL.md
// SKILL.md frontmatter 格式（无前置 ---）：
//
//	name: <技能名>
//	description: <描述>
//	---
//	<正文>
package forwarder

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	skillManifestMaxMetadataBytes    = 16 * 1024
	skillManifestMaxNameBytes        = 256
	skillManifestMaxDescriptionBytes = 8 * 1024
	skillManifestMaxVersionBytes     = 256
)

// SkillManifestDiagnostic describes one validation failure for a SKILL.md manifest.
type SkillManifestDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// GlobalSkill 是从 app-data 技能目录扫描到的一个技能记录。
type GlobalSkill struct {
	Name        string
	Description string
	Version     string
	ContentHash string
	Diagnostics []SkillManifestDiagnostic
	FullPath    string // SKILL.md 的绝对路径
}

// SkillStore 扫描 app-data 技能目录，构建供 prompt 注入用的 agent_skills 片段。
type SkillStore struct {
	root string
	mu   sync.Mutex
	// activator 实现调用链稀疏激活；为 nil 时退化为全量注入（保持旧行为）。
	activator *SkillActivator
	// convStore 可选，用于读写会话父子激活集；为 nil 时父子传递降级。
	convStore      *ConversationFileStore
	scanEnabled    bool
	skillSources   map[string]bool
	disabledSkills map[string]bool
}

// NewSkillStore 创建 SkillStore。root 为空时 Scan 始终返回空。
func NewSkillStore(root string) *SkillStore {
	store := &SkillStore{root: strings.TrimSpace(root), scanEnabled: true}
	store.activator = NewSkillActivator(store)
	return store
}

// SetScanSettings keeps system-prompt skill activation aligned with the settings UI.
func (s *SkillStore) SetScanSettings(enabled bool, sources map[string]bool, disabled map[string]bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.scanEnabled = enabled
	s.skillSources = cloneSkillSettingMap(sources)
	s.disabledSkills = cloneSkillSettingMap(disabled)
	s.mu.Unlock()
}

// SetConversationStore 注入会话存储，启用调用链父子激活集传递。
// 必须在 NewSkillStore 之后、首次编译之前调用。传 nil 关闭父子传递。
func (s *SkillStore) SetConversationStore(convStore *ConversationFileStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.convStore = convStore
	s.mu.Unlock()
}

// Scan 扫描 root/<skillName>/SKILL.md，返回所有有效技能（按 name 排序）。
func (s *SkillStore) Scan() ([]GlobalSkill, error) {
	return s.ScanForWorkspace("")
}

type skillStoreScanSnapshot struct {
	root           string
	scanEnabled    bool
	skillSources   map[string]bool
	disabledSkills map[string]bool
}

// ScanForWorkspace scans skills for the explicit workspace without mutating shared store scope.
func (s *SkillStore) ScanForWorkspace(workspaceRoot string) ([]GlobalSkill, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	snapshot := skillStoreScanSnapshot{
		root:           s.root,
		scanEnabled:    s.scanEnabled,
		skillSources:   cloneSkillSettingMap(s.skillSources),
		disabledSkills: cloneSkillSettingMap(s.disabledSkills),
	}
	s.mu.Unlock()
	if snapshot.root == "" {
		return nil, nil
	}
	return scanSkillsForWorkspace(strings.TrimSpace(workspaceRoot), snapshot)
}

// scanSkillsForWorkspace 返回供激活器筛选的全部候选技能。
//
// 主来源是跨工具多源扫描（ScanAllSkills，覆盖 Cursor/Claude/Codex/ZCode/.agents 等），
// 在其基础上补齐 root（旧 BYOK 目录）里多源扫描未覆盖的技能，保证向后兼容。
// 结果按 name 去重、排序，交给激活器做 BM25 Top-K 稀疏激活。
func scanSkillsForWorkspace(workspaceRoot string, snapshot skillStoreScanSnapshot) ([]GlobalSkill, error) {
	if !snapshot.scanEnabled {
		return nil, nil
	}
	settings := SkillMCPScanSettings{
		Enabled:        true,
		SkillSources:   snapshot.skillSources,
		DisabledSkills: snapshot.disabledSkills,
	}
	merged := filterScannedSkills(ScanAllSkills(workspaceRoot), settings)
	seen := make(map[string]struct{}, len(merged))
	skills := make([]GlobalSkill, 0, len(merged))
	for _, sk := range merged {
		key := strings.ToLower(strings.TrimSpace(sk.Name))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		skills = append(skills, sk.GlobalSkill)
	}
	// 旧 BYOK root 作为保底补充（兼容历史：可能有多源扫描未纳入的目录布局）。
	if strings.TrimSpace(snapshot.root) != "" && sourceEnabled(snapshot.skillSources, string(SkillSourceBYOK)) {
		if legacy, err := scanLegacySkillRoot(snapshot.root); err == nil {
			for _, sk := range legacy {
				key := strings.ToLower(strings.TrimSpace(sk.Name))
				if key == "" {
					continue
				}
				if snapshot.disabledSkills != nil && snapshot.disabledSkills[key] {
					continue
				}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				skills = append(skills, sk)
			}
		}
	}
	if len(skills) == 0 {
		return nil, nil
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

func cloneSkillSettingMap(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(source))
	for key, value := range source {
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "" {
			cloned[key] = value
		}
	}
	return cloned
}

// scanLegacySkillRoot 扫描单个旧式技能根目录（<root>/<name>/SKILL.md）。
// 处理符号链接：entry.IsDir() 对 symlink 返回 false，需用 os.Stat 跟踪链接。
func scanLegacySkillRoot(root string) ([]GlobalSkill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan skills dir: %w", err)
	}
	skills := make([]GlobalSkill, 0, len(entries))
	for _, entry := range entries {
		entryPath := filepath.Join(root, entry.Name())
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
		skill, ok := readSkillFile(skillPath)
		if !ok {
			continue
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

// BuildAgentSkillsPromptSection 构建 <agent_skills> XML prompt 片段，
// 格式与 Cursor 原生的 agent_skills 注入一致（每个技能带 fullPath 属性和描述文字）。
// 返回 (片段文本, 技能数, error)；无技能时返回 ("", 0, nil)。
func (s *SkillStore) BuildAgentSkillsPromptSection() (string, int, error) {
	return s.BuildAgentSkillsPromptSectionForWorkspace("")
}

// BuildAgentSkillsPromptSectionForWorkspace builds the full skill section for an explicit workspace.
func (s *SkillStore) BuildAgentSkillsPromptSectionForWorkspace(workspaceRoot string) (string, int, error) {
	if s == nil {
		return "", 0, nil
	}
	skills, err := s.ScanForWorkspace(workspaceRoot)
	if err != nil {
		return "", 0, err
	}
	if len(skills) == 0 {
		return "", 0, nil
	}
	lines := make([]string, 0, len(skills)+2)
	lines = append(lines, `<agent_skills>`)
	for _, sk := range skills {
		desc := strings.TrimSpace(sk.Description)
		if desc == "" {
			desc = sk.Name
		}
		lines = append(lines,
			fmt.Sprintf(`<agent_skill fullPath="%s">%s</agent_skill>`,
				escapeSharedRulePromptText(sk.FullPath), escapeSharedRulePromptText(desc)),
		)
	}
	lines = append(lines, `</agent_skills>`)
	return strings.Join(lines, "\n"), len(skills), nil
}

// BuildActivatedSkillsPromptSection 按「调用链稀疏激活」选取技能并构建 <agent_skills> 片段。
//
// 激活规则（见 docs/superpowers/specs/2026-07-29-skill-sparse-activation-design.md）：
//   - 用 BM25 对 queryText 与各技能 description 打分，取 Top-3（≥阈值）。
//   - 元技能 find-skills 始终常驻（不占 Top-3 名额）。
//   - conversation 为子代理会话时，重打分并以父会话 LastActivatedSkills 作保底候选。
//   - 非子代理会话时，best-effort 把本轮激活技能名写回 conversation.LastActivatedSkills。
//
// 返回 (片段文本, 激活技能数, error)；无激活技能时返回 ("", 0, nil)。
// activator 为 nil 时退化为全量注入（调用 BuildAgentSkillsPromptSection）。
func (s *SkillStore) BuildActivatedSkillsPromptSection(queryText string, conversation *ConversationFile) (string, int, error) {
	return s.BuildActivatedSkillsPromptSectionForWorkspace("", queryText, conversation)
}

// BuildActivatedSkillsPromptSectionGoal 与 BuildActivatedSkillsPromptSection 相同，
// 但强制注入 goal-loop 技能（若扫描可见且未被禁用），供 /goal 会话使用。
func (s *SkillStore) BuildActivatedSkillsPromptSectionGoal(queryText string, conversation *ConversationFile) (string, int, error) {
	return s.buildActivatedSkillsPromptSectionForWorkspaceExcluding("", queryText, conversation, nil, true)
}

// BuildActivatedSkillsPromptSectionForWorkspace sparsely activates skills for an explicit workspace.
func (s *SkillStore) BuildActivatedSkillsPromptSectionForWorkspace(workspaceRoot string, queryText string, conversation *ConversationFile) (string, int, error) {
	return s.buildActivatedSkillsPromptSectionForWorkspaceExcluding(workspaceRoot, queryText, conversation, nil, false)
}

func (s *SkillStore) buildActivatedSkillsPromptSectionForWorkspaceExcluding(workspaceRoot string, queryText string, conversation *ConversationFile, excludedPaths map[string]struct{}, goalMode bool) (string, int, error) {
	if s == nil {
		return "", 0, nil
	}
	if s.activator == nil {
		skills, err := s.ScanForWorkspace(workspaceRoot)
		if err != nil {
			return "", 0, err
		}
		prompt, count := buildAgentSkillsSectionFromList(filterSkillsExcludingPaths(skills, excludedPaths))
		return prompt, count, nil
	}

	var parentActivated []string
	isChild := conversation != nil && strings.TrimSpace(conversation.SubagentTypeName) != ""
	if isChild {
		parentActivated = s.readParentActivatedSkills(conversation)
	}

	activated := s.activator.activateForWorkspaceExcluding(workspaceRoot, queryText, parentActivated, excludedPaths, goalMode)
	if len(activated) == 0 {
		return "", 0, nil
	}

	// 非子代理会话：best-effort 写回本轮激活集，供后续子代理读取。
	if !isChild && conversation != nil && strings.TrimSpace(conversation.ConversationID) != "" {
		names := make([]string, 0, len(activated))
		for _, sk := range activated {
			names = append(names, sk.Name)
		}
		s.writeActivatedSkillsToConversation(conversation, names)
	}

	prompt, count := buildAgentSkillsSectionFromList(activated)
	return prompt, count, nil
}

func skillPathKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	key := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func filterSkillsExcludingPaths(skills []GlobalSkill, excludedPaths map[string]struct{}) []GlobalSkill {
	if len(skills) == 0 || len(excludedPaths) == 0 {
		return skills
	}
	filtered := make([]GlobalSkill, 0, len(skills))
	for _, skill := range skills {
		if _, excluded := excludedPaths[skillPathKey(skill.FullPath)]; excluded {
			continue
		}
		filtered = append(filtered, skill)
	}
	return filtered
}

// buildAgentSkillsSectionFromList 把已激活技能列表构建为 <agent_skills> 片段。
func buildAgentSkillsSectionFromList(skills []GlobalSkill) (string, int) {
	if len(skills) == 0 {
		return "", 0
	}
	lines := make([]string, 0, len(skills)+2)
	lines = append(lines, `<agent_skills>`)
	for _, sk := range skills {
		desc := strings.TrimSpace(sk.Description)
		if desc == "" {
			desc = sk.Name
		}
		lines = append(lines,
			fmt.Sprintf(`<agent_skill fullPath="%s">%s</agent_skill>`,
				escapeSharedRulePromptText(sk.FullPath), escapeSharedRulePromptText(desc)),
		)
	}
	lines = append(lines, `</agent_skills>`)
	return strings.Join(lines, "\n"), len(skills)
}

// readParentActivatedSkills 读取父会话最近一次激活的技能名列表。
// 父会话缺失/读取失败/convStore 未注入时返回 nil（降级为纯重打分）。
func (s *SkillStore) readParentActivatedSkills(conversation *ConversationFile) []string {
	if s == nil || s.convStore == nil || conversation == nil {
		return nil
	}
	parentID := strings.TrimSpace(conversation.ParentConversationID)
	if parentID == "" {
		return nil
	}
	parent, err := s.convStore.LoadConversation(parentID)
	if err != nil || parent == nil {
		return nil
	}
	return parent.LastActivatedSkills
}

// writeActivatedSkillsToConversation 把本轮激活技能名 best-effort 写回当前会话。
// 仅更新内存对象与持久化元数据；失败仅记日志，不阻断编译（见 prefix-cache-stability：
// 该字段不进 model-visible history，不影响 replay prefix）。
func (s *SkillStore) writeActivatedSkillsToConversation(conversation *ConversationFile, names []string) {
	if s == nil || s.convStore == nil || conversation == nil {
		return
	}
	convID := strings.TrimSpace(conversation.ConversationID)
	if convID == "" {
		return
	}
	conversation.LastActivatedSkills = names
	snapshot := append([]string(nil), names...)
	_, _ = s.convStore.UpdateConversationMeta(convID, func(c *ConversationFile) error {
		c.LastActivatedSkills = snapshot
		return nil
	})
}

// readSkillFile 读取 SKILL.md 并解析 frontmatter，返回 GlobalSkill 及是否有效。
func readSkillFile(path string) (GlobalSkill, bool) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GlobalSkill{
			FullPath: absPath,
			Diagnostics: []SkillManifestDiagnostic{{
				Code:    "read_error",
				Message: "skill manifest could not be read",
			}},
		}, false
	}
	metadata, diagnostics := parseSKILLManifest(data)
	sum := sha256.Sum256(data)
	skill := GlobalSkill{
		Name:        strings.TrimSpace(metadata.Name),
		Description: strings.TrimSpace(metadata.Description),
		Version:     strings.TrimSpace(metadata.Version),
		ContentHash: hex.EncodeToString(sum[:]),
		Diagnostics: diagnostics,
		FullPath:    absPath,
	}
	return skill, len(diagnostics) == 0
}

type skillManifestMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
}

func parseSKILLManifest(data []byte) (skillManifestMetadata, []SkillManifestDiagnostic) {
	frontmatter, standardYAML := extractSKILLFrontmatter(string(data))
	if len(frontmatter) > skillManifestMaxMetadataBytes {
		return skillManifestMetadata{}, []SkillManifestDiagnostic{{
			Code:    "metadata_too_large",
			Message: fmt.Sprintf("skill manifest metadata exceeds %d bytes", skillManifestMaxMetadataBytes),
		}}
	}
	var metadata skillManifestMetadata
	if standardYAML {
		if err := yaml.Unmarshal([]byte(frontmatter), &metadata); err != nil {
			return skillManifestMetadata{}, []SkillManifestDiagnostic{{
				Code:    "invalid_yaml",
				Message: "skill manifest metadata is not valid YAML",
			}}
		}
	} else {
		metadata = parseLegacySKILLFrontmatter(frontmatter)
	}
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	metadata.Version = strings.TrimSpace(metadata.Version)
	diagnostics := validateSKILLManifest(metadata)
	return metadata, diagnostics
}

func validateSKILLManifest(metadata skillManifestMetadata) []SkillManifestDiagnostic {
	diagnostics := make([]SkillManifestDiagnostic, 0, 6)
	validateRequiredSkillMetadata := func(field string, value string, maxBytes int, required bool) {
		if required && value == "" {
			diagnostics = append(diagnostics, SkillManifestDiagnostic{
				Code:    field + "_required",
				Message: "skill manifest " + field + " is required",
			})
			return
		}
		if len(value) > maxBytes {
			diagnostics = append(diagnostics, SkillManifestDiagnostic{
				Code:    field + "_too_large",
				Message: fmt.Sprintf("skill manifest %s exceeds %d bytes", field, maxBytes),
			})
		}
		if containsControlCharacter(value) {
			diagnostics = append(diagnostics, SkillManifestDiagnostic{
				Code:    field + "_control_character",
				Message: "skill manifest " + field + " contains a control character",
			})
		}
	}
	validateRequiredSkillMetadata("name", metadata.Name, skillManifestMaxNameBytes, true)
	validateRequiredSkillMetadata("description", metadata.Description, skillManifestMaxDescriptionBytes, true)
	validateRequiredSkillMetadata("version", metadata.Version, skillManifestMaxVersionBytes, false)
	return diagnostics
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func extractSKILLFrontmatter(content string) (string, bool) {
	content = strings.TrimPrefix(strings.ReplaceAll(content, "\r\n", "\n"), "\ufeff")
	lines := strings.Split(content, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	standardYAML := start < len(lines) && strings.TrimSpace(lines[start]) == "---"
	if standardYAML {
		start++
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n"), standardYAML
}

func parseLegacySKILLFrontmatter(content string) skillManifestMetadata {
	var metadata skillManifestMetadata
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "name:"):
			metadata.Name = unquoteFrontmatterValue(strings.TrimSpace(strings.TrimPrefix(line, "name:")))
		case strings.HasPrefix(line, "description:"):
			metadata.Description = unquoteFrontmatterValue(strings.TrimSpace(strings.TrimPrefix(line, "description:")))
		case strings.HasPrefix(line, "version:"):
			metadata.Version = unquoteFrontmatterValue(strings.TrimSpace(strings.TrimPrefix(line, "version:")))
		}
	}
	return metadata
}

// parseSKILLFrontmatter 从 SKILL.md 内容解析 name / description 字段。
//
// 支持两种 frontmatter 格式：
//  1. 标准 YAML frontmatter：文件以 "---" 开头，到第二个 "---" 行结束：
//     ---\nname: x\ndescription: y\n---\n正文
//  2. 旧式（无前置 ---）：文件以 "key: value" 行开头，到第一个 "---" 行结束：
//     name: x\ndescription: y\n---\n正文
//
// 两种格式下 name/description 的值都允许带引号（"..." 或 '...'）。
func parseSKILLFrontmatter(content string) (name, description string) {
	metadata, _ := parseSKILLManifest([]byte(content))
	return metadata.Name, metadata.Description
}

// unquoteFrontmatterValue 去掉 frontmatter 值的外层引号（YAML 允许 "..." 或 '...'）。
func unquoteFrontmatterValue(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
