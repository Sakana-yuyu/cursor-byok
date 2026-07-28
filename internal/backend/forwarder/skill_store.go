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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// GlobalSkill 是从 app-data 技能目录扫描到的一个技能记录。
type GlobalSkill struct {
	Name        string
	Description string
	FullPath    string // SKILL.md 的绝对路径
}

// SkillStore 扫描 app-data 技能目录，构建供 prompt 注入用的 agent_skills 片段。
type SkillStore struct {
	root string
	mu   sync.Mutex
}

// NewSkillStore 创建 SkillStore。root 为空时 Scan 始终返回空。
func NewSkillStore(root string) *SkillStore {
	return &SkillStore{root: strings.TrimSpace(root)}
}

// Scan 扫描 root/<skillName>/SKILL.md，返回所有有效技能（按 name 排序）。
func (s *SkillStore) Scan() ([]GlobalSkill, error) {
	if s == nil || s.root == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanLocked()
}

func (s *SkillStore) scanLocked() ([]GlobalSkill, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan skills dir: %w", err)
	}
	skills := make([]GlobalSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(s.root, entry.Name(), "SKILL.md")
		skill, ok := readSkillFile(skillPath)
		if !ok {
			continue
		}
		skills = append(skills, skill)
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

// BuildAgentSkillsPromptSection 构建 <agent_skills> XML prompt 片段，
// 格式与 Cursor 原生的 agent_skills 注入一致（每个技能带 fullPath 属性和描述文字）。
// 返回 (片段文本, 技能数, error)；无技能时返回 ("", 0, nil)。
func (s *SkillStore) BuildAgentSkillsPromptSection() (string, int, error) {
	if s == nil {
		return "", 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	skills, err := s.scanLocked()
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
			fmt.Sprintf(`<agent_skill fullPath=%q>%s</agent_skill>`,
				sk.FullPath, escapeSharedRulePromptText(desc)),
		)
	}
	lines = append(lines, `</agent_skills>`)
	return strings.Join(lines, "\n"), len(skills), nil
}

// readSkillFile 读取 SKILL.md 并解析 frontmatter，返回 GlobalSkill 及是否有效。
func readSkillFile(path string) (GlobalSkill, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GlobalSkill{}, false
	}
	name, desc := parseSKILLFrontmatter(string(data))
	if strings.TrimSpace(name) == "" {
		return GlobalSkill{}, false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	return GlobalSkill{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(desc),
		FullPath:    absPath,
	}, true
}

// parseSKILLFrontmatter 从 SKILL.md 内容解析 name / description 字段。
//
// Cursor SKILL.md 格式：文件以 "key: value" 行开头，到第一个 "---" 行（或文件末）结束
// frontmatter，不含前置 "---"。
func parseSKILLFrontmatter(content string) (name, description string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			continue
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			continue
		}
	}
	return
}
