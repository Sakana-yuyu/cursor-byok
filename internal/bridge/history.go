package bridge

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cursor/internal/appdata"
	"cursor/internal/backend/forwarder"
	"cursor/internal/client"
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
// 返回删除的会话目录数。resetUsage 优先复用 forwarder 带锁的用量重置（丢弃 pending 事件），
// 避免与进程内 UsageFileStore 的防抖写并发导致重置后旧数据写回；为空时回退到直接重置文件。
func clearHistory(resetUsage func() error) (int, error) {
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
	if resetUsage != nil {
		if err := resetUsage(); err != nil && firstErr == nil {
			firstErr = err
		}
	} else if err := historymetrics.ResetUsageFile(appdata.UsageFilePath()); err != nil && firstErr == nil {
		firstErr = err
	}
	return deleted, firstErr
}

// SessionDebugFile 是 debug 子目录下单个调试日志文件的展示元数据。
type SessionDebugFile struct {
	Name        string `json:"name"`
	SizeBytes   int64  `json:"sizeBytes"`
	ModTimeUnix int64  `json:"modTimeUnixMs"`
}

// sessionDebugFileWhitelist 是允许读取尾部的 debug 文件名白名单。
// 与 internal/backend/forwarder/debug_recorder.go 写入的固定文件名保持一致，
// 拒绝任意路径拼接，防止通过 filename 参数穿越到会话目录之外。
var sessionDebugFileWhitelist = map[string]bool{
	"bidi.raw.jsonl":     true,
	"bidi.decoded.jsonl": true,
	"runtime.jsonl":      true,
	"runsse.jsonl":       true,
	"provider.jsonl":     true,
}

// readSessionDebugTailDefault 是 maxBytes<=0 时读取尾部的默认字节数。
const readSessionDebugTailDefault int64 = 64 * 1024

// validateSessionID 校验 sessionID 是标准 UUID，防止路径穿越。
func validateSessionID(sessionID string) (string, error) {
	id := strings.TrimSpace(sessionID)
	if !historySessionIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid session id: %q", sessionID)
	}
	return id, nil
}

// exportSessionDebugBundleIn 是 exportSessionDebugBundle 的可测形式：
// historyRoot 与 logsRoot 由调用方给出，便于用 t.TempDir() 构造隔离环境。
// 只读遍历，不进 PurgeDebugLogs 闸门（读操作不该阻塞落盘）。
// 目标会话目录或 debug 子目录不存在时返回明确错误，不返回空路径。
func exportSessionDebugBundleIn(historyRoot, logsRoot, sessionID string) (string, error) {
	id, err := validateSessionID(sessionID)
	if err != nil {
		return "", err
	}
	sessionDir := filepath.Join(historyRoot, id)
	if info, statErr := os.Stat(sessionDir); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("session directory not found: %s", id)
	}
	debugDir := filepath.Join(sessionDir, "debug")
	if info, statErr := os.Stat(debugDir); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("debug directory not found for session %s", id)
	}

	if err := os.MkdirAll(logsRoot, 0o755); err != nil {
		return "", fmt.Errorf("create logs directory: %w", err)
	}
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	zipName := fmt.Sprintf("session-%s-%s.zip", short, time.Now().Format("20060102-150405"))
	zipPath := filepath.Join(logsRoot, zipName)

	zipFile, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("create bundle file: %w", err)
	}
	defer func() { _ = zipFile.Close() }()

	zipWriter := zip.NewWriter(zipFile)

	// 打包会话目录下的 state.json、context.json 与 debug/*。
	// 不递归整个会话目录（避免把临时文件、残留目录都塞进证据包），
	// 而是显式枚举排查证据所需的三类文件。
	entries := []string{"state.json", "context.json"}
	for _, name := range entries {
		src := filepath.Join(sessionDir, name)
		if _, statErr := os.Stat(src); statErr != nil {
			continue
		}
		if err := appendFileToZip(zipWriter, sessionDir, name, src); err != nil {
			_ = zipWriter.Close()
			_ = os.Remove(zipPath)
			return "", fmt.Errorf("pack %s: %w", name, err)
		}
	}

	debugEntries, readErr := os.ReadDir(debugDir)
	if readErr != nil {
		_ = zipWriter.Close()
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("read debug dir: %w", readErr)
	}
	for _, entry := range debugEntries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if err := appendFileToZip(zipWriter, debugDir, filepath.Join("debug", name), filepath.Join(debugDir, name)); err != nil {
			_ = zipWriter.Close()
			_ = os.Remove(zipPath)
			return "", fmt.Errorf("pack debug/%s: %w", name, err)
		}
	}

	if err := zipWriter.Close(); err != nil {
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("close bundle: %w", err)
	}
	return zipPath, nil
}

// appendFileToZip 把单个文件写入 zip，archiveName 是 zip 内的相对路径。
func appendFileToZip(zipWriter *zip.Writer, baseDir, archiveName, srcPath string) error {
	writer, err := zipWriter.Create(filepath.ToSlash(archiveName))
	if err != nil {
		return err
	}
	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := io.Copy(writer, file); err != nil {
		return err
	}
	return nil
}

// listSessionDebugFilesIn 是 listSessionDebugFiles 的可测形式。
// debug 目录不存在返回空切片 + nil（落盘 worker 并发删除后的瞬时状态视为正常空）。
func listSessionDebugFilesIn(historyRoot, sessionID string) ([]SessionDebugFile, error) {
	id, err := validateSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	debugDir := filepath.Join(historyRoot, id, "debug")
	entries, err := os.ReadDir(debugDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionDebugFile{}, nil
		}
		return nil, fmt.Errorf("read debug dir: %w", err)
	}
	files := make([]SessionDebugFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		files = append(files, SessionDebugFile{
			Name:        entry.Name(),
			SizeBytes:   info.Size(),
			ModTimeUnix: info.ModTime().UnixMilli(),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// readSessionDebugTailIn 是 readSessionDebugTail 的可测形式。
// filename 必须命中白名单，否则拒绝；只读文件尾部 maxBytes 字节。
// 文件不存在返回明确错误。
func readSessionDebugTailIn(historyRoot, sessionID, filename string, maxBytes int64) (string, error) {
	id, err := validateSessionID(sessionID)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(filename)
	if !sessionDebugFileWhitelist[name] {
		return "", fmt.Errorf("debug file %q is not allowed", filename)
	}
	if maxBytes <= 0 {
		maxBytes = readSessionDebugTailDefault
	}
	path := filepath.Join(historyRoot, id, "debug", name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("debug file not found: %s", name)
		}
		return "", fmt.Errorf("stat debug file: %w", err)
	}

	// 文件小于阈值时整体读取；否则定位到尾部 maxBytes 处读取，保留最新内容
	// （rotateIfNeeded 保留尾部策略与「读尾部」语义一致，最新部分最可能含错误）。
	if info.Size() <= maxBytes {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read debug file: %w", readErr)
		}
		return string(data), nil
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open debug file: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(-maxBytes, io.SeekEnd); err != nil {
		return "", fmt.Errorf("seek debug file: %w", err)
	}
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("read debug tail: %w", err)
	}
	return string(buf[:n]), nil
}

// exportSessionDebugBundle 把指定会话的排查证据打包成 zip，返回 zip 路径。
func exportSessionDebugBundle(sessionID string) (string, error) {
	return exportSessionDebugBundleIn(appdata.HistoryRootPath(), client.ResolveLogsRootPath(), sessionID)
}

// listSessionDebugFiles 列出指定会话 debug 子目录下的文件元信息。
func listSessionDebugFiles(sessionID string) ([]SessionDebugFile, error) {
	return listSessionDebugFilesIn(appdata.HistoryRootPath(), sessionID)
}

// readSessionDebugTail 读取指定会话 debug 文件的尾部内容。
func readSessionDebugTail(sessionID, filename string, maxBytes int64) (string, error) {
	return readSessionDebugTailIn(appdata.HistoryRootPath(), sessionID, filename, maxBytes)
}
