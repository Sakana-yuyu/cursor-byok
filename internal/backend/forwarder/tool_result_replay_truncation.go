package forwarder

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"

	"google.golang.org/protobuf/encoding/protojson"
)

const (
	projectedReplayKiB = 1024

	projectedReadReplayLimit       = 64 * projectedReplayKiB
	projectedShellReplayLimit      = 128 * projectedReplayKiB
	projectedShellStreamLimit      = 16 * projectedReplayKiB
	projectedShellInterleavedLimit = 32 * projectedReplayKiB
	projectedGrepReplayLimit       = 32 * projectedReplayKiB
	projectedEditReplayLimit       = 32 * projectedReplayKiB
	projectedPatchEditReplayLimit  = 4 * projectedReplayKiB
	projectedWebFetchReplayLimit   = 32 * projectedReplayKiB
	projectedWebSearchReplayLimit  = 16 * projectedReplayKiB
	projectedMcpReplayLimit        = 32 * projectedReplayKiB
	projectedTaskReplayLimit       = 64 * projectedReplayKiB
	activeTurnEmergencyReplayLimit = 4 * projectedReplayKiB

	// 陈旧工具结果激进截断阈值（移植自 Reasonix 的「cache-first context maintenance」）。
	// 投影层不改动历史，只让「陈旧且巨大」的工具结果在模型视图里比当前更短，从而减缓上下文增长、
	// 推迟昂贵的摘要压缩，并稳态下保持 prompt-cache 命中。这是零缓存风险的 append-only 路径。
	staleToolResultAggressiveThreshold = 8 * projectedReplayKiB // 仅当陈旧结果 > 8 KiB 才启动激进截断，避免无谓截断已经很小的结果
	staleToolResultReplayLimit         = 4 * projectedReplayKiB // 陈旧大结果的目标上限，远小于各工具的常规 32-128 KiB 限制
)

type activeTurnToolResultCompactionStats struct {
	BeforeTokens     int64
	AfterTokens      int64
	ShortenedResults int
	OmittedResults   int
	LatestToolCallID string
}

// compactActiveTurnToolResultsForBudget is an emergency provider-view projection.
// It preserves the canonical conversation and the newest completed tool result,
// while shrinking older results from the active turn until the prompt fits.
func compactActiveTurnToolResultsForBudget(conversation *ConversationFile, compiled CompiledConversation, budgetTokens int64) (CompiledConversation, activeTurnToolResultCompactionStats, bool) {
	stats := activeTurnToolResultCompactionStats{BeforeTokens: estimateCompiledPromptTokens(compiled)}
	stats.AfterTokens = stats.BeforeTokens
	if conversation == nil || budgetTokens <= 0 || stats.BeforeTokens <= budgetTokens {
		return compiled, stats, stats.BeforeTokens <= budgetTokens
	}
	turnSeq := conversation.CurrentTurnSeq
	if turnSeq <= 0 {
		turnSeq = conversation.NextTurnSeq - 1
	}
	requestID := strings.TrimSpace(conversation.CurrentRequestID)
	latestToolCallID := latestCompletedToolCallIDForTurn(conversation.Entries, turnSeq, requestID)
	stats.LatestToolCallID = latestToolCallID
	if turnSeq <= 0 || requestID == "" || latestToolCallID == "" {
		return compiled, stats, false
	}
	currentToolCallIDs := make(map[string]struct{})
	for _, entry := range conversation.Entries {
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.RequestID) != requestID || strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		if toolCallID := historyEntryToolCallID(entry); toolCallID != "" {
			currentToolCallIDs[toolCallID] = struct{}{}
		}
	}
	if len(currentToolCallIDs) < 2 {
		return compiled, stats, false
	}

	projected := compiled
	projected.Messages = append([]modeladapter.Message(nil), compiled.Messages...)
	currentToolMessageIndexes := make(map[string]int, len(currentToolCallIDs))
	for index, message := range projected.Messages {
		toolCallID := strings.TrimSpace(message.ToolCallID)
		if strings.TrimSpace(message.Role) != "tool" || toolCallID == "" {
			continue
		}
		if _, ok := currentToolCallIDs[toolCallID]; ok {
			// The active turn is the newest replay suffix. Selecting the last provider
			// message for each ID prevents an older turn that reused an ID from being
			// rewritten by this emergency projection.
			currentToolMessageIndexes[toolCallID] = index
		}
	}
	type activeTurnToolResultCandidate struct {
		Index         int
		OriginalBytes int
		ToolName      string
		ToolCallID    string
	}
	candidateIndexes := make([]activeTurnToolResultCandidate, 0, len(currentToolCallIDs)-1)
	for index, message := range projected.Messages {
		toolCallID := strings.TrimSpace(message.ToolCallID)
		if toolCallID == "" || toolCallID == latestToolCallID || currentToolMessageIndexes[toolCallID] != index {
			continue
		}
		candidateIndexes = append(candidateIndexes, activeTurnToolResultCandidate{
			Index:         index,
			OriginalBytes: len(message.Content),
			ToolName:      firstNonEmpty(strings.TrimSpace(message.Name), "tool"),
			ToolCallID:    toolCallID,
		})
	}
	for _, candidate := range candidateIndexes {
		message := &projected.Messages[candidate.Index]
		if len(message.Content) <= activeTurnEmergencyReplayLimit {
			continue
		}
		message.Content = truncateProjectedReplayTextMiddle(candidate.ToolName, message.Content, activeTurnEmergencyReplayLimit)
		if candidate.Index < projected.StableMessageCount {
			projected.StableMessageCount = candidate.Index
		}
		stats.ShortenedResults++
		stats.AfterTokens = estimateCompiledPromptTokens(projected)
		if stats.AfterTokens <= budgetTokens {
			return projected, stats, true
		}
	}
	for _, candidate := range candidateIndexes {
		message := &projected.Messages[candidate.Index]
		minimal := fmt.Sprintf(
			"[%s result omitted from the active-turn provider view after exceeding the context budget; tool_call_id=%s original_bytes=%d original result remains in canonical history]",
			candidate.ToolName,
			candidate.ToolCallID,
			candidate.OriginalBytes,
		)
		if message.Content == minimal {
			continue
		}
		message.Content = minimal
		if candidate.Index < projected.StableMessageCount {
			projected.StableMessageCount = candidate.Index
		}
		stats.OmittedResults++
		stats.AfterTokens = estimateCompiledPromptTokens(projected)
		if stats.AfterTokens <= budgetTokens {
			return projected, stats, true
		}
	}
	stats.AfterTokens = estimateCompiledPromptTokens(projected)
	return projected, stats, stats.AfterTokens <= budgetTokens
}

func limitProjectedToolResultReplay(toolName string, content string, resultText string, fromStoredToolCall bool, historical bool) string {
	if compacted, ok := compactProjectedGenerateImageResultReplay(toolName, content, resultText); ok {
		return compacted
	}
	if historical {
		if compacted, ok := compactHistoricalEditErrorReplay(toolName, content); ok {
			content = compacted
			fromStoredToolCall = false
		}
	}
	if compacted, ok := compactProjectedEditToolResultReplay(toolName, content); ok {
		content = compacted
		fromStoredToolCall = false
	}
	if compacted, ok := compactProjectedShellToolResultReplay(toolName, content); ok {
		content = compacted
		fromStoredToolCall = false
	}
	if historical && len(content) > staleToolResultAggressiveThreshold {
		if compacted, ok := compactHistoricalLsResultReplay(toolName, content, resultText); ok {
			content = compacted
			fromStoredToolCall = false
		}
	}
	limit, ok := projectedToolReplayLimit(toolName)
	if !ok {
		return strings.TrimSpace(content)
	}
	// 陈旧（非当前/上一轮）且明显过大的工具结果，把上限下调到 staleToolResultReplayLimit。
	// 这里只动投影给模型看的内容，不碰持久化历史，因此完全符合 append-only 与 prefix-cache 稳定性。
	staleAggressive := historical && limit > staleToolResultReplayLimit && len(content) > staleToolResultAggressiveThreshold
	if staleAggressive {
		limit = staleToolResultReplayLimit
	}
	content = strings.TrimSpace(content)
	if len(content) <= limit {
		return content
	}
	if staleAggressive {
		// 陈旧大结果两端都可能携带关键信息（命令在头部、报错/退出码在尾部），故保留头尾。
		if fromStoredToolCall {
			fallback := strings.TrimSpace(resultText)
			notice := fmt.Sprintf("[stale tool result replay shortened: stored ToolCall result exceeded %d bytes]", limit)
			if fallback == "" {
				fallback = notice
			} else {
				fallback += "\n\n" + notice
			}
			return truncateProjectedReplayTextMiddle(toolName, fallback, limit)
		}
		return truncateProjectedReplayTextMiddle(toolName, content, limit)
	}
	if fromStoredToolCall {
		fallback := strings.TrimSpace(resultText)
		notice := fmt.Sprintf("[tool result replay truncated: stored ToolCall result exceeded %d bytes]", limit)
		if fallback == "" {
			fallback = notice
		} else {
			fallback += "\n\n" + notice
		}
		return truncateProjectedReplayText(toolName, fallback, limit)
	}
	return truncateProjectedReplayText(toolName, content, limit)
}

func projectedToolReplayLimit(toolName string) (int, bool) {
	switch strings.TrimSpace(toolName) {
	case "GenerateImage":
		return projectedWebSearchReplayLimit, true
	case "Read":
		return projectedReadReplayLimit, true
	case "Shell":
		return projectedShellReplayLimit, true
	case "Grep":
		return projectedGrepReplayLimit, true
	case "Ls":
		return projectedGrepReplayLimit, true
	case "PatchEdit", "PatchEditLines", "PatchEditSpan":
		return projectedPatchEditReplayLimit, true
	case "Edit", "Write":
		return projectedEditReplayLimit, true
	case "WebFetch":
		return projectedWebFetchReplayLimit, true
	case "WebSearch":
		return projectedWebSearchReplayLimit, true
	case "CallMcpTool", "FetchMcpResource", "ListMcpResources":
		return projectedMcpReplayLimit, true
	case "Task":
		return projectedTaskReplayLimit, true
	default:
		return 0, false
	}
}

func compactHistoricalLsResultReplay(toolName string, content string, resultText string) (string, bool) {
	if strings.TrimSpace(toolName) != "Ls" {
		return "", false
	}
	const notice = "[历史 Ls 目录树已从 provider 回放中省略；如需精确内容请重新调用 Ls]"
	if fallback := strings.TrimSpace(resultText); fallback != "" {
		return fallback + "\n\n" + notice, true
	}

	path, files, ok := projectedLsReplaySummary(content)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("ls success path=%s files=%d\n\n%s", path, files, notice), true
}

func projectedLsReplaySummary(content string) (string, int, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", 0, false
	}

	toolCall := &agentv1.ToolCall{}
	if err := protojson.Unmarshal([]byte(trimmed), toolCall); err == nil {
		if ls := toolCall.GetLsToolCall(); ls != nil {
			path := strings.TrimSpace(ls.GetArgs().GetPath())
			if root := lsResultDirectoryRoot(ls.GetResult()); root != nil {
				path = firstNonEmpty(path, strings.TrimSpace(root.GetAbsPath()))
				return path, projectedLsFileCount(root), path != ""
			}
		}
	}

	result := &agentv1.LsResult{}
	if err := protojson.Unmarshal([]byte(trimmed), result); err != nil {
		return "", 0, false
	}
	root := lsResultDirectoryRoot(result)
	if root == nil || strings.TrimSpace(root.GetAbsPath()) == "" {
		return "", 0, false
	}
	return strings.TrimSpace(root.GetAbsPath()), projectedLsFileCount(root), true
}

func lsResultDirectoryRoot(result *agentv1.LsResult) *agentv1.LsDirectoryTreeNode {
	if result == nil {
		return nil
	}
	if success := result.GetSuccess(); success != nil {
		return success.GetDirectoryTreeRoot()
	}
	if timeout := result.GetTimeout(); timeout != nil {
		return timeout.GetDirectoryTreeRoot()
	}
	return nil
}

func projectedLsFileCount(root *agentv1.LsDirectoryTreeNode) int {
	if root == nil {
		return 0
	}
	if count := int(root.GetNumFiles()); count > 0 {
		return count
	}
	count := len(root.GetChildrenFiles())
	for _, child := range root.GetChildrenDirs() {
		count += projectedLsFileCount(child)
	}
	return count
}

func compactProjectedGenerateImageResultReplay(toolName string, content string, resultText string) (string, bool) {
	if strings.TrimSpace(toolName) != "GenerateImage" {
		return "", false
	}
	fallback := strings.TrimSpace(resultText)
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		if fallback == "" {
			return "", false
		}
		return fallback, true
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		if fallback == "" {
			return truncateProjectedReplayText("GenerateImage", trimmed, projectedWebSearchReplayLimit), true
		}
		return fallback, true
	}
	if compactGenerateImagePayload(payload) {
		encoded, err := json.Marshal(payload)
		if err == nil {
			return string(encoded), true
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return trimmed, true
}

func compactGenerateImagePayload(value any) bool {
	changed := false
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if text, ok := child.(string); ok {
				switch key {
				case "image_data", "imageData":
					item[key] = fmt.Sprintf("[base64 image data omitted from replay; bytes=%d]", len(strings.TrimSpace(text)))
					changed = true
					continue
				}
			}
			if compactGenerateImagePayload(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range item {
			if compactGenerateImagePayload(child) {
				changed = true
			}
		}
	}
	return changed
}

func patchEditReplayLimit(toolName string) int {
	switch strings.TrimSpace(toolName) {
	case "PatchEdit", "PatchEditLines", "PatchEditSpan":
		return projectedPatchEditReplayLimit
	default:
		return projectedEditReplayLimit
	}
}

func compactProjectedShellToolResultReplay(toolName string, content string) (string, bool) {
	if strings.TrimSpace(toolName) != "Shell" {
		return "", false
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return "", false
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", false
	}
	if !compactProjectedShellFields(payload) {
		return "", false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func compactProjectedShellFields(value any) bool {
	changed := false
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if text, ok := child.(string); ok {
				switch key {
				case "stdout", "stderr":
					next := truncateProjectedReplayTextMiddle("Shell "+key, text, projectedShellStreamLimit)
					if next != text {
						item[key] = next
						changed = true
					}
					continue
				case "interleaved_output", "interleavedOutput":
					next := truncateProjectedReplayTextMiddle("Shell interleaved output", text, projectedShellInterleavedLimit)
					if next != text {
						item[key] = next
						changed = true
					}
					continue
				}
			}
			if compactProjectedShellFields(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range item {
			if compactProjectedShellFields(child) {
				changed = true
			}
		}
	}
	return changed
}

func compactProjectedEditToolResultReplay(toolName string, content string) (string, bool) {
	switch strings.TrimSpace(toolName) {
	case "PatchEdit", "PatchEditLines", "PatchEditSpan", "Edit", "Write":
	default:
		return "", false
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return "", false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", false
	}
	success, ok := payload["success"].(map[string]any)
	if !ok {
		return "", false
	}
	diffString := firstJSONText(success, "diff_string", "diffString")
	beforeContent := ""
	afterContent := ""
	if diffString == "" {
		beforeContent = firstJSONText(success, "before_full_file_content", "beforeFullFileContent")
		afterContent = firstJSONText(success, "after_full_file_content", "afterFullFileContent")
		if beforeContent != "" {
			diffString, _, _ = computeEditDiff(beforeContent, afterContent)
		}
	}
	if diffString != "" {
		diffString = truncateProjectedReplayText(firstNonEmpty(strings.TrimSpace(toolName), "PatchEdit"), diffString, patchEditReplayLimit(toolName))
		encoded, err := json.Marshal(map[string]any{
			"success": map[string]any{
				"diff_string": diffString,
			},
		})
		if err != nil {
			return "", false
		}
		return string(encoded), true
	}
	if afterContent != "" {
		afterContent = truncateProjectedReplayText(firstNonEmpty(strings.TrimSpace(toolName), "Write"), afterContent, patchEditReplayLimit(toolName))
		encoded, err := json.Marshal(map[string]any{
			"success": map[string]any{
				"after_full_file_content": afterContent,
			},
		})
		if err != nil {
			return "", false
		}
		return string(encoded), true
	}
	return "", false
}

func compactHistoricalEditErrorReplay(toolName string, content string) (string, bool) {
	switch strings.TrimSpace(toolName) {
	case "PatchEdit", "PatchEditLines", "PatchEditSpan", "Edit", "Write":
	default:
		return "", false
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", false
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		return "", false
	}
	if firstJSONText(errorPayload, "error", "modelVisibleError") == "" {
		return "", false
	}
	encoded, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"error": "historical edit error omitted from replay; re-read the file before editing",
		},
	})
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func firstJSONText(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			return value
		}
	}
	return ""
}

func truncateProjectedReplayText(toolName string, text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return strings.TrimSpace(text)
	}
	original := len(text)
	notice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; showing %d of %d bytes]", toolName, limit, limit, original)
	for {
		keep := limit - len(notice)
		if keep <= 0 {
			return truncateProjectedUTF8(text, limit)
		}
		kept := truncateProjectedUTF8(text, keep)
		nextNotice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; showing %d of %d bytes]", toolName, limit, len(kept), original)
		output := strings.TrimRight(kept, "\n") + nextNotice
		if len(output) <= limit || nextNotice == notice {
			return output
		}
		notice = nextNotice
	}
}

func truncateProjectedReplayTextMiddle(toolName string, text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	original := len(text)
	notice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; omitted middle; showing %d of %d bytes]\n\n", toolName, limit, limit, original)
	for {
		keep := limit - len(notice)
		if keep <= 0 {
			return truncateProjectedUTF8(text, limit)
		}
		// 头尾预算按工具类型差异化分配（移植自 cursor2api）：定位/列表类结果开头是关键信息，
		// shell 输出错误与退出码在尾部；其余工具保持 50/50。
		headLimit := keep * toolReplayHeadRatio(toolName) / 100
		if headLimit < 1 {
			headLimit = 1
		}
		head := truncateProjectedUTF8(text, headLimit)
		if head == "" && headLimit > 0 {
			// 头部预算在多字节字符边界上拿不到任何字节：预算全部让给尾部，
			// 避免 head+tail < keep 造成 showing 数字与实保留量偏差。
			headLimit = 0
		}
		tailLimit := keep - headLimit
		if tailLimit < 0 {
			tailLimit = 0
		}
		tail := truncateProjectedUTF8Suffix(text, tailLimit)
		kept := len(head) + len(tail)
		nextNotice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; omitted middle; showing %d of %d bytes]\n\n", toolName, limit, kept, original)
		output := head + nextNotice + tail
		if len(output) <= limit || nextNotice == notice {
			return output
		}
		notice = nextNotice
	}
}

// toolReplayHeadRatio 返回工具结果截断时「头部」占保留预算的百分比（尾部 = 100% - 该值）。
// 默认 50/50；按工具类型差异化，尽量在有限预算内保住该类结果的高价值端。
func toolReplayHeadRatio(toolName string) int {
	name := strings.TrimSpace(toolName)
	switch {
	case name == "Read", name == "Grep", name == "WebSearch", name == "WebFetch":
		// 定位/列表类：开头含文件头、命中列表、摘要，头部价值最高。
		return 70
	case name == "Shell" || strings.HasPrefix(name, "Shell "):
		// 命令输出：报错、退出码、堆栈通常在尾部。
		return 25
	default:
		return 50
	}
}

func truncateProjectedUTF8(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if limit > len(text) {
		limit = len(text)
	}
	truncated := text[:limit]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func truncateProjectedUTF8Suffix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	if start < 0 {
		start = 0
	}
	suffix := text[start:]
	for !utf8.ValidString(suffix) && start < len(text) {
		start++
		suffix = text[start:]
	}
	return suffix
}
