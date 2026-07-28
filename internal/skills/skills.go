// Package skills 把内置技能（find-skills + superpowers）随程序二进制分发，
// 并在启动时自动释放到 Cursor 客户端的全局技能目录（~/.cursor/skills/），
// 让用户在任意项目输入 / 即可调用，无需手动放置文件。
//
// 释放策略：仅写入缺失或内容变化的文件；不删除用户自行添加的其他技能。
// 失败仅记日志，不阻断启动。
package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:bundled
var bundled embed.FS

// CursorSkillsDir 返回 Cursor 全局技能目录 ~/.cursor/skills。
func CursorSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("user home directory is empty")
	}
	return filepath.Join(home, ".cursor", "skills"), nil
}

// SyncResult 是一次技能释放的汇总结果。
type SyncResult struct {
	Total   int // 内置技能总数
	Written int // 本次实际写入/更新的文件数
	Skipped int // 内容未变化而跳过的文件数
	Failed  int // 写入失败的文件数
}

// SyncToCursorSkillsDir 把内置技能释放到目标目录（默认 ~/.cursor/skills）。
// targetDir 为空时使用 CursorSkillsDir()。仅写入缺失或内容变化的文件，不删除已有内容。
func SyncToCursorSkillsDir(targetDir string) (SyncResult, error) {
	result := SyncResult{}
	if strings.TrimSpace(targetDir) == "" {
		dir, err := CursorSkillsDir()
		if err != nil {
			return result, err
		}
		targetDir = dir
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return result, fmt.Errorf("create skills dir: %w", err)
	}

	// 遍历 embed 树里的所有文件，逐个同步。
	walkErr := fs.WalkDir(bundled, "bundled", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := bundled.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// path 形如 bundled/<skillName>/<...>/file；去掉 "bundled/" 前缀得到目标相对路径。
		// 注意 embed.FS 的路径始终用正斜杠（/），即使在本机是 Windows。
		rel := strings.TrimPrefix(path, "bundled/")
		if rel == "" || rel == path {
			return nil
		}
		dest := filepath.Join(targetDir, filepath.FromSlash(rel))
		// 判断是否需要写入：文件缺失或内容不同。
		existing, readErr := os.ReadFile(dest)
		if readErr == nil && string(existing) == string(data) {
			result.Skipped++
			return nil
		}
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			result.Failed++
			return nil // 单个文件失败不中断整体
		}
		if writeErr := os.WriteFile(dest, data, 0o644); writeErr != nil {
			result.Failed++
			return nil
		}
		result.Written++
		return nil
	})
	if walkErr != nil {
		return result, walkErr
	}
	// 统计内置技能数（bundled 下含 SKILL.md 的顶层子目录数）。embed.FS 路径用正斜杠。
	entries, _ := bundled.ReadDir("bundled")
	for _, e := range entries {
		if e.IsDir() {
			if _, err := bundled.ReadFile("bundled/" + e.Name() + "/SKILL.md"); err == nil {
				result.Total++
			}
		}
	}
	return result, nil
}
