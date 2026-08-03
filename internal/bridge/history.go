package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cursor/internal/appdata"
	"cursor/internal/historymetrics"
)

// HistorySession 单个历史会话的展示元数据（桌面 UI 历史管理）。
type HistorySession struct {
	ID            string `json:"id"`
	CreatedAtUnix int64  `json:"createdAtUnixMs"`
	UpdatedAtUnix int64  `json:"updatedAtUnixMs"`
	SizeBytes     int64  `json:"sizeBytes"`
	SubagentType  string `json:"subagentType,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Title         string `json:"title,omitempty"`
	HasDebug      bool   `json:"hasDebug,omitempty"`
}

// historySessionIDPattern 只接受标准 UUID 目录名，防止删除/扫描时路径穿越。
var historySessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const historyTitleMaxRunes = 40

// historyStateFile 对应会话目录下的 state.json 元数据。
type historyStateFile struct {
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	SubagentTypeName string    `json:"subagent_type_name"`
	Mode             string    `json:"mode"`
}

// historyContextFile 对应会话目录下的 context.json（取首条用户消息做标题）。
type historyContextFile struct {
	Items []historyContextItem `json:"items"`
}

// historyContextItem 对应会话目录下的 context.json 条目。
// 新格式用户消息内容在 payload.text，旧格式在 content。
type historyContextItem struct {
	Role    string          `json:"role"`
	Kind    string          `json:"kind"`
	Content json.RawMessage `json:"content"`
	Payload json.RawMessage `json:"payload"`
}

// scanHistorySessions 扫描 history 根目录，返回所有会话的展示元数据。
// 仅收录标准 UUID 命名目录；忽略 usage.json、_debug 等非会话项。
func scanHistorySessions() ([]HistorySession, error) {
	root := appdata.HistoryRootPath()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history root: %w", err)
	}
	sessions := make([]HistorySession, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !historySessionIDPattern.MatchString(name) {
			continue
		}
		sessions = append(sessions, scanHistorySession(filepath.Join(root, name), name))
	}
	return sessions, nil
}

// scanHistorySession 汇总单个会话目录的元数据。
func scanHistorySession(dir, id string) HistorySession {
	session := HistorySession{ID: id}
	if state := readHistoryState(filepath.Join(dir, "state.json")); state != nil {
		if !state.CreatedAt.IsZero() {
			session.CreatedAtUnix = state.CreatedAt.UnixMilli()
		}
		if !state.UpdatedAt.IsZero() {
			session.UpdatedAtUnix = state.UpdatedAt.UnixMilli()
		}
		session.SubagentType = strings.TrimSpace(state.SubagentTypeName)
		session.Mode = strings.TrimSpace(state.Mode)
	}
	session.Title = readHistoryTitle(filepath.Join(dir, "context.json"))
	session.HasDebug = dirHasFiles(filepath.Join(dir, "debug"))
	session.SizeBytes = dirSize(dir)
	return session
}

func readHistoryState(path string) *historyStateFile {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state historyStateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil
	}
	return &state
}

// readHistoryTitle 从 context.json 提取首条用户消息的前缀作为会话标题。
// 兼容新旧格式：新格式用户消息内容在 payload.text，旧格式在 content。
// request_context 等注入条目不含用户文本，自然跳过。
func readHistoryTitle(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc historyContextFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	for _, item := range doc.Items {
		if strings.TrimSpace(item.Role) != "user" {
			continue
		}
		if text := extractHistoryText(item); text != "" {
			return truncateHistoryTitle(text)
		}
	}
	return ""
}

// extractHistoryText 从用户条目提取纯文本：优先新格式 payload.text，兼容旧格式 content。
func extractHistoryText(item historyContextItem) string {
	if text := extractHistoryTextSource(item.Payload); text != "" {
		return text
	}
	return extractHistoryTextSource(item.Content)
}

// extractHistoryTextSource 解析文本来源字段：支持 {"text": ...} 包装或裸 string/parts 数组。
func extractHistoryTextSource(source json.RawMessage) string {
	if len(source) == 0 {
		return ""
	}
	var wrapper struct {
		Text json.RawMessage `json:"text"`
	}
	if err := json.Unmarshal(source, &wrapper); err == nil && len(wrapper.Text) > 0 {
		return extractHistoryTextPart(wrapper.Text)
	}
	return extractHistoryTextPart(source)
}

// extractHistoryTextPart 解析单个文本值：string 或 [{type:"text",text:...}] parts 数组。
func extractHistoryTextPart(source json.RawMessage) string {
	if len(source) == 0 {
		return ""
	}
	var plain string
	if err := json.Unmarshal(source, &plain); err == nil {
		return strings.TrimSpace(plain)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(source, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			if strings.TrimSpace(part.Type) != "text" {
				continue
			}
			builder.WriteString(part.Text)
		}
		return strings.TrimSpace(builder.String())
	}
	return ""
}

// truncateHistoryTitle 单行化并截断标题文本。
func truncateHistoryTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > historyTitleMaxRunes {
		runes = runes[:historyTitleMaxRunes]
	}
	return string(runes)
}

// dirHasFiles 判断目录下是否存在任何文件（递归）。
func dirHasFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// dirSize 递归统计目录占用字节数（只统计普通文件）。
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// deleteHistorySessions 删除指定会话目录。ID 必须是标准 UUID，否则拒绝，防止路径穿越。
func deleteHistorySessions(sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	root := appdata.HistoryRootPath()
	var firstErr error
	for _, rawID := range sessionIDs {
		id := strings.TrimSpace(rawID)
		if !historySessionIDPattern.MatchString(id) {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid session id: %q", rawID)
			}
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, id)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove session %s: %w", id, err)
		}
	}
	return firstErr
}

// clearHistory 一键清理：删除全部会话目录与遗留目录（_debug 等），并把 usage.json 重置为空档。
// 返回删除的会话目录数。
func clearHistory() (int, error) {
	root := appdata.HistoryRootPath()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read history root: %w", err)
	}
	deleted := 0
	var firstErr error
	for _, entry := range entries {
		name := entry.Name()
		if name == "usage.json" {
			// usage.json 由 forwarder 进程内 store 持有，删除文件会被内存数据重写，
			// 改为原子重置，避免并发写冲突。
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove %s: %w", name, err)
			}
			continue
		}
		if entry.IsDir() && historySessionIDPattern.MatchString(name) {
			deleted++
		}
	}
	if err := historymetrics.ResetUsageFile(appdata.UsageFilePath()); err != nil && firstErr == nil {
		firstErr = err
	}
	return deleted, firstErr
}