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
	"cursor/internal/backend/forwarder"
	"cursor/internal/historymetrics"
)

// HistorySession 单个历史会话的展示元数据（桌面 UI 历史管理）。
type HistorySession struct {
	ID             string `json:"id"`
	CreatedAtUnix  int64  `json:"createdAtUnixMs"`
	UpdatedAtUnix  int64  `json:"updatedAtUnixMs"`
	SizeBytes      int64  `json:"sizeBytes"`
	DebugSizeBytes int64  `json:"debugSizeBytes"`
	SubagentType   string `json:"subagentType,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Title          string `json:"title,omitempty"`
	HasDebug       bool   `json:"hasDebug,omitempty"`
	// Status 来自 state.json 的 current_loop_status：
	// idle/completed/failed/provider_error/canceled/waiting_tool/running 等。
	Status string `json:"status,omitempty"`
	// RequestID 来自 state.json 的 current_request_id，最近一次请求的 ID，方便排查 debug 日志。
	RequestID string `json:"requestId,omitempty"`
}

// historySessionIDPattern 只接受标准 UUID 目录名，防止删除/扫描时路径穿越。
var historySessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const historyTitleMaxRunes = 40

// historyStateFile 对应会话目录下的 state.json 元数据。
type historyStateFile struct {
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	SubagentTypeName  string    `json:"subagent_type_name"`
	Mode              string    `json:"mode"`
	CurrentLoopStatus string    `json:"current_loop_status"`
	CurrentRequestID  string    `json:"current_request_id"`
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
		session.Status = strings.TrimSpace(state.CurrentLoopStatus)
		session.RequestID = strings.TrimSpace(state.CurrentRequestID)
	}
	session.Title = readHistoryTitle(filepath.Join(dir, "context.json"))
	debugDir := filepath.Join(dir, "debug")
	session.HasDebug = dirHasFiles(debugDir)
	session.DebugSizeBytes = dirSize(debugDir)
	session.SizeBytes = dirSize(dir) - session.DebugSizeBytes
	if session.SizeBytes < 0 {
		session.SizeBytes = 0
	}
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
	// 会话目录里含 debug 子目录，同样要在暂停落盘的窗口内删除，否则已入队的事件
	// 会把目录重建成「只有 debug、没有 state.json」的残留会话。
	return forwarder.PurgeDebugLogs(func() error {
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
	})
}

// historyOrphanDebugDirName 是无会话归属的 debug 日志目录（forwarder 在拿不到
// conversation id 时把事件写到 _debug/orphan/<requestID>）。
const historyOrphanDebugDirName = "_debug"

// historyDebugDirs 列出 history 根目录下所有 debug 日志目录，统计与清理共用同一套
// 遍历逻辑，避免两边口径不一致导致「统计为 0 但磁盘仍被占用」。
//
// 覆盖三类：
//   - UUID 命名的会话目录下的 debug/；
//   - 非 UUID 会话目录下的 debug/（forwarder 只对 conversation id 做字符替换，
//     并未强制 UUID，所以确实会出现这种目录）；
//   - _debug/（无会话归属的孤儿日志）。
//
// 目录名直接来自 ReadDir，不含用户输入，因此没有路径穿越风险；非目录项（如
// usage.json）与符号链接（IsDir 为 false）被跳过，清理不会越出 history 根目录。
func historyDebugDirs() ([]string, error) {
	return historyDebugDirsIn(appdata.HistoryRootPath())
}

// historyDebugDirsIn 是 historyDebugDirs 的可测形式：root 由调用方给出。
func historyDebugDirsIn(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history root: %w", err)
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == historyOrphanDebugDirName {
			dirs = append(dirs, filepath.Join(root, name))
			continue
		}
		dirs = append(dirs, filepath.Join(root, name, "debug"))
	}
	return dirs, nil
}

// deleteHistoryDebugLogs 只删除指定会话的 debug 子目录，保留 state.json/context.json。
// 用于「清理调试日志」释放磁盘但保留会话记录。ID 必须是标准 UUID。
// 返回释放的字节数。
func deleteHistoryDebugLogs(sessionIDs []string) (int64, error) {
	if len(sessionIDs) == 0 {
		return 0, nil
	}
	root := appdata.HistoryRootPath()
	var freed int64
	// 在暂停 debug 落盘的窗口内删除，否则清理前已入队的事件会把目录重建回来。
	err := forwarder.PurgeDebugLogs(func() error {
		var firstErr error
		for _, rawID := range sessionIDs {
			id := strings.TrimSpace(rawID)
			if !historySessionIDPattern.MatchString(id) {
				if firstErr == nil {
					firstErr = fmt.Errorf("invalid session id: %q", rawID)
				}
				continue
			}
			debugDir := filepath.Join(root, id, "debug")
			size := dirSize(debugDir)
			if err := os.RemoveAll(debugDir); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("remove debug logs for session %s: %w", id, err)
				}
				continue
			}
			freed += size
		}
		return firstErr
	})
	return freed, err
}

// purgeAllHistoryDebugLogs 清理全部调试日志（含孤儿日志），保留会话本体。
// 返回释放的字节数。供首页「清理调试日志」一键调用，前端不需要先枚举会话。
func purgeAllHistoryDebugLogs() (int64, error) {
	dirs, err := historyDebugDirs()
	if err != nil {
		return 0, err
	}
	if len(dirs) == 0 {
		return 0, nil
	}
	var freed int64
	err = forwarder.PurgeDebugLogs(func() error {
		var firstErr error
		for _, dir := range dirs {
			size := dirSize(dir)
			if err := os.RemoveAll(dir); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("remove debug logs %s: %w", filepath.Base(filepath.Dir(dir)), err)
				}
				continue
			}
			freed += size
		}
		return firstErr
	})
	return freed, err
}

// historyDebugUsage 统计所有调试日志的总占用字节数（含孤儿日志）。
// 用于首页全局提醒（debug 日志占用过大时提示用户清理）。
func historyDebugUsage() (int64, error) {
	dirs, err := historyDebugDirs()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, dir := range dirs {
		total += dirSize(dir)
	}
	return total, nil
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
	// 同 deleteHistorySessions：在暂停 debug 落盘的窗口内删除，避免残留 debug 目录。
	firstErr := forwarder.PurgeDebugLogs(func() error {
		var removeErr error
		for _, entry := range entries {
			name := entry.Name()
			if name == "usage.json" {
				// usage.json 由 forwarder 进程内 store 持有，删除文件会被内存数据重写，
				// 改为原子重置，避免并发写冲突。
				continue
			}
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				if removeErr == nil {
					removeErr = fmt.Errorf("remove %s: %w", name, err)
				}
				continue
			}
			if entry.IsDir() && historySessionIDPattern.MatchString(name) {
				deleted++
			}
		}
		return removeErr
	})
	if err := historymetrics.ResetUsageFile(appdata.UsageFilePath()); err != nil && firstErr == nil {
		firstErr = err
	}
	return deleted, firstErr
}
