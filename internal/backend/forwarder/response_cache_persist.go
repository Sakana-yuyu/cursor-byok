package forwarder

// response_cache_persist.go 实现本地响应缓存的磁盘持久化（L2 层）。
//
// 目的：Cursor 客户端频繁重启，纯内存缓存每次启动即清空，跨会话的重复请求
// （同一问题、同一工具结果序列）无法命中。持久化后缓存可跨进程存活，
// 配合 LRU 淘汰与长 TTL，命中窗口从「本次进程」扩展到「多天」。

import (
	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	runtimecore "cursor/internal/backend/agent/core"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// modelEventJSON 是 ModelEvent 的磁盘形态。
// 刻意排除 Err 字段：error 接口无法从 JSON 恢复，而缓存的流全部是成功收口流（Err 恒为 nil）。
type modelEventJSON struct {
	Kind                     modeladapter.ModelEventKind `json:"kind"`
	OccurredAt               time.Time                   `json:"occurredAt"`
	Provider                 string                      `json:"provider"`
	Model                    string                      `json:"model"`
	BaseURL                  string                      `json:"baseURL"`
	GroupName                string                      `json:"groupName"`
	Text                     string                      `json:"text"`
	ThinkingStyle            agentv1.ThinkingStyle       `json:"thinkingStyle"`
	ThinkingDurationMS       int32                       `json:"thinkingDurationMs"`
	ThinkingSignature        string                      `json:"thinkingSignature"`
	ThinkingSignatureSource  string                      `json:"thinkingSignatureSource"`
	ProviderItemID           string                      `json:"providerItemId"`
	ProviderStatus           string                      `json:"providerStatus"`
	ProviderSummary          json.RawMessage             `json:"providerSummary,omitempty"`
	ProviderCallID           string                      `json:"providerCallId"`
	ToolCallID               string                      `json:"toolCallId"`
	ToolCall                 *agentv1.ToolCall           `json:"toolCall,omitempty"`
	ToolCallDelta            *agentv1.ToolCallDelta      `json:"toolCallDelta,omitempty"`
	ArgsTextDelta            string                      `json:"argsTextDelta"`
	InputTokens              int64                       `json:"inputTokens"`
	OutputTokens             int64                       `json:"outputTokens"`
	CacheReadTokens          int64                       `json:"cacheReadTokens"`
	CacheWriteTokens         int64                       `json:"cacheWriteTokens"`
	UsagePresent             bool                        `json:"usagePresent"`
	CacheReadPresent         bool                        `json:"cacheReadPresent"`
	CacheWritePresent        bool                        `json:"cacheWritePresent"`
	ToolInvocation           *runtimecore.ToolInvocation `json:"toolInvocation,omitempty"`
	FinishReason             string                      `json:"finishReason"`
}

func modelEventToJSON(event modeladapter.ModelEvent) modelEventJSON {
	return modelEventJSON{
		Kind:                    event.Kind,
		OccurredAt:              event.OccurredAt,
		Provider:                event.Provider,
		Model:                   event.Model,
		BaseURL:                 event.BaseURL,
		GroupName:               event.GroupName,
		Text:                    event.Text,
		ThinkingStyle:           event.ThinkingStyle,
		ThinkingDurationMS:      event.ThinkingDurationMS,
		ThinkingSignature:       event.ThinkingSignature,
		ThinkingSignatureSource: event.ThinkingSignatureSource,
		ProviderItemID:          event.ProviderItemID,
		ProviderStatus:          event.ProviderStatus,
		ProviderSummary:         event.ProviderSummary,
		ProviderCallID:          event.ProviderCallID,
		ToolCallID:              event.ToolCallID,
		ToolCall:                event.ToolCall,
		ToolCallDelta:           event.ToolCallDelta,
		ArgsTextDelta:           event.ArgsTextDelta,
		InputTokens:             event.InputTokens,
		OutputTokens:            event.OutputTokens,
		CacheReadTokens:         event.CacheReadTokens,
		CacheWriteTokens:        event.CacheWriteTokens,
		UsagePresent:            event.UsagePresent,
		CacheReadPresent:        event.CacheReadPresent,
		CacheWritePresent:       event.CacheWritePresent,
		ToolInvocation:          event.ToolInvocation,
		FinishReason:            event.FinishReason,
	}
}

func modelEventFromJSON(entry modelEventJSON) modeladapter.ModelEvent {
	return modeladapter.ModelEvent{
		Kind:                    entry.Kind,
		OccurredAt:              entry.OccurredAt,
		Provider:                entry.Provider,
		Model:                   entry.Model,
		BaseURL:                 entry.BaseURL,
		GroupName:               entry.GroupName,
		Text:                    entry.Text,
		ThinkingStyle:           entry.ThinkingStyle,
		ThinkingDurationMS:      entry.ThinkingDurationMS,
		ThinkingSignature:       entry.ThinkingSignature,
		ThinkingSignatureSource: entry.ThinkingSignatureSource,
		ProviderItemID:          entry.ProviderItemID,
		ProviderStatus:          entry.ProviderStatus,
		ProviderSummary:         entry.ProviderSummary,
		ProviderCallID:          entry.ProviderCallID,
		ToolCallID:              entry.ToolCallID,
		ToolCall:                entry.ToolCall,
		ToolCallDelta:           entry.ToolCallDelta,
		ArgsTextDelta:           entry.ArgsTextDelta,
		InputTokens:             entry.InputTokens,
		OutputTokens:            entry.OutputTokens,
		CacheReadTokens:         entry.CacheReadTokens,
		CacheWriteTokens:        entry.CacheWriteTokens,
		UsagePresent:            entry.UsagePresent,
		CacheReadPresent:        entry.CacheReadPresent,
		CacheWritePresent:       entry.CacheWritePresent,
		ToolInvocation:          entry.ToolInvocation,
		FinishReason:            entry.FinishReason,
	}
}

// responseCacheDiskFileVersion 是磁盘缓存文件格式版本；结构变更时递增并迁移/丢弃旧文件。
const responseCacheDiskFileVersion = 1

// saveDebounceDelay 控制磁盘写入的节流间隔：多条写入合并为一次落盘。
const saveDebounceDelay = 2 * time.Second

// responseCacheDiskFile 是磁盘缓存文件的整体结构。
type responseCacheDiskFile struct {
	Version int                                 `json:"version"`
	SavedAt time.Time                           `json:"savedAt"`
	Entries map[string]responseCacheDiskEntry   `json:"entries"`
}

// responseCacheDiskEntry 是单条缓存的磁盘形态（与内存形态一一对应）。
type responseCacheDiskEntry struct {
	Events            []modelEventJSON `json:"events"`
	SavedInputTokens  int64            `json:"savedInputTokens"`
	SavedOutputTokens int64            `json:"savedOutputTokens"`
	ExpiresAt         time.Time        `json:"expiresAt"`
	LastAccessAt      time.Time        `json:"lastAccessAt"`
}

// loadResponseCacheFromDisk 从磁盘加载缓存条目；文件缺失或损坏时返回空集合。
// 过期条目在此处直接丢弃，避免加载进内存后占用 LRU 槽位。
func loadResponseCacheFromDisk(path string) map[string]*responseCacheEntry {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file responseCacheDiskFile
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != responseCacheDiskFileVersion {
		return nil
	}
	now := time.Now()
	entries := make(map[string]*responseCacheEntry, len(file.Entries))
	for key, disk := range file.Entries {
		if now.After(disk.ExpiresAt) || len(disk.Events) == 0 {
			continue
		}
		events := make([]modeladapter.ModelEvent, len(disk.Events))
		for index, event := range disk.Events {
			events[index] = modelEventFromJSON(event)
		}
		entries[key] = &responseCacheEntry{
			events:            events,
			savedInputTokens:  disk.SavedInputTokens,
			savedOutputTokens: disk.SavedOutputTokens,
			expiresAt:         disk.ExpiresAt,
			lastAccessAt:      disk.LastAccessAt,
		}
	}
	return entries
}

// saveResponseCacheToDisk 原子写缓存文件：先写临时文件再 rename，避免进程被杀时损坏主文件。
func saveResponseCacheToDisk(path string, entries map[string]*responseCacheEntry) error {
	file := responseCacheDiskFile{
		Version: responseCacheDiskFileVersion,
		SavedAt: time.Now(),
		Entries: make(map[string]responseCacheDiskEntry, len(entries)),
	}
	for key, entry := range entries {
		events := make([]modelEventJSON, len(entry.events))
		for index, event := range entry.events {
			events[index] = modelEventToJSON(event)
		}
		file.Entries[key] = responseCacheDiskEntry{
			Events:            events,
			SavedInputTokens:  entry.savedInputTokens,
			SavedOutputTokens: entry.savedOutputTokens,
			ExpiresAt:         entry.expiresAt,
			LastAccessAt:      entry.lastAccessAt,
		}
	}
	payload, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), historyDirPerm); err != nil {
		return err
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, payload, historyFilePerm); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, historyFilePerm); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return os.Rename(tempPath, path)
}