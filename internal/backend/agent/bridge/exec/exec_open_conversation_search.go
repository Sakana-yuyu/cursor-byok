// exec_open_conversation_search.go 承载 SearchConversations：检索客户端的历史会话索引。
//
// 客户端把本地会话与云端缓存会话统一建索引，exec 侧回 hits 以及三个索引状态位
// （truncated/partial/rebuilding）。这三个状态位是本条能力的关键：索引正在重建或只建了
// 一部分时，「零命中」不等于「这段会话不存在」，必须原样转达给模型。
package execbridge

import (
	"fmt"
	"strings"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

const (
	// conversationSearchDefaultLimit 是模型未指定时请求的命中条数。
	conversationSearchDefaultLimit = 20
	// conversationSearchMaxLimit 是允许模型请求的命中条数上限。
	conversationSearchMaxLimit = 50
	// conversationSearchReplayContentLimit 是回放给模型的总字节上限。
	conversationSearchReplayContentLimit = 16 * replayKiB
	// conversationSearchReplaySnippetLimit 是单条命中摘录的字节上限。
	conversationSearchReplaySnippetLimit = replayKiB
	// conversationSearchReplayHitLimit 是渲染的命中条数上限。
	conversationSearchReplayHitLimit = 50
)

// conversationSearchToolArgs 是 SearchConversations 工具的模型侧参数。
type conversationSearchToolArgs struct {
	Query string
	Limit int32
}

func decodeConversationSearchArgs(raw []byte) (conversationSearchToolArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return conversationSearchToolArgs{}, err
	}
	decoded := conversationSearchToolArgs{
		Query: strings.TrimSpace(readStringArg(args, "query", "q")),
		Limit: conversationSearchDefaultLimit,
	}
	limit, found, err := runtimecore.ReadInt32Arg(args, "limit", "max_results", "maxResults")
	if err != nil {
		return conversationSearchToolArgs{}, err
	}
	if found {
		decoded.Limit = limit
	}
	if decoded.Limit <= 0 {
		decoded.Limit = conversationSearchDefaultLimit
	}
	if decoded.Limit > conversationSearchMaxLimit {
		decoded.Limit = conversationSearchMaxLimit
	}
	return decoded, nil
}

// openSearchConversations 构造 SearchConversations 对应的执行桥请求。
func (bridge *Bridge) openSearchConversations(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	input, err := decodeConversationSearchArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode SearchConversations args failed: %w", err)
	}
	if input.Query == "" {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("SearchConversations requires a non-empty query")
	}
	limit := input.Limit
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-conversation-search-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ConversationSearchArgs{
					ConversationSearchArgs: &agentv1.ConversationSearchArgs{
						Query:      input.Query,
						ToolCallId: toolCall.CallID,
						Limit:      &limit,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "conversation_search",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

// summarizeConversationSearchResult 把会话检索结果渲染成模型可读文本。
//
// 索引状态提示与条数截断提示都写在输出开头：整体截断砍掉的正是结尾，
// 提示放末尾会连同内容一起消失，而索引状态恰恰是模型最不能漏读的一条。
func summarizeConversationSearchResult(result *agentv1.ConversationSearchResult, argsJSON []byte) string {
	if result == nil {
		return "conversation search result missing"
	}
	if failure := result.GetError(); failure != nil {
		return "conversation search failed: " + strings.TrimSpace(failure.GetError())
	}
	input, err := decodeConversationSearchArgs(argsJSON)
	if err != nil {
		input = conversationSearchToolArgs{Limit: conversationSearchDefaultLimit}
	}

	success := result.GetSuccess()
	hits := success.GetHits()
	header := fmt.Sprintf("conversation search: %d matches for %q", len(hits), input.Query)

	notices := conversationSearchIndexNotices(success)
	shown := hits
	if len(shown) > conversationSearchReplayHitLimit {
		shown = shown[:conversationSearchReplayHitLimit]
		notices = append(notices, fmt.Sprintf("[truncated: listed %d of %d hits]", len(shown), len(hits)))
	}
	if len(hits) == 0 {
		if len(notices) == 0 {
			return header + "\nThe conversation index reported a healthy state, so this query really has no matches."
		}
		return header + "\n" + strings.Join(notices, "\n")
	}

	entries := make([]string, 0, len(shown))
	for _, hit := range shown {
		entries = append(entries, renderConversationSearchHit(hit))
	}
	body := strings.Join(entries, "\n")

	prefix := header + "\n"
	if len(notices) > 0 {
		prefix += strings.Join(notices, "\n") + "\n"
	}
	prefix += "\n"
	budget := conversationSearchReplayContentLimit - len(prefix)
	if budget < replayKiB {
		budget = replayKiB
	}
	return prefix + truncateReplayText("SearchConversations", body, budget)
}

// conversationSearchIndexNotices 把三个索引状态位翻译成模型能据以决策的文案。
// 「零命中」在索引重建/部分建成时不构成「不存在」的证据，必须说清楚。
func conversationSearchIndexNotices(success *agentv1.ConversationSearchSuccess) []string {
	notices := make([]string, 0, 3)
	if success.GetRebuilding() {
		notices = append(notices, "[index rebuilding: the client is still rebuilding its conversation index, so this result set is incomplete. A conversation missing here is not evidence that it does not exist - retry the search later before drawing any conclusion from a miss.]")
	}
	if success.GetPartial() {
		notices = append(notices, "[index partial: only part of the conversation history has been indexed so far. A conversation missing here is not evidence that it does not exist.]")
	}
	if success.GetTruncated() {
		notices = append(notices, "[truncated: the client capped this result set; more matches exist. Raise limit or use a narrower query to reach the rest.]")
	}
	return notices
}

func renderConversationSearchHit(hit *agentv1.ConversationSearchHit) string {
	if hit == nil {
		return ""
	}
	attributes := []string{"source=" + conversationSearchSourceName(hit.GetSource())}
	if updated := hit.GetUpdatedAtMs(); updated > 0 {
		attributes = append(attributes, "updated="+time.UnixMilli(updated).UTC().Format(time.RFC3339))
	}
	title := strings.TrimSpace(hit.GetTitle())
	if title == "" {
		title = "(untitled)"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s  %s (%s)", strings.TrimSpace(hit.GetConversationId()), title, strings.Join(attributes, ", "))
	if snippet := strings.TrimSpace(hit.GetSnippet()); snippet != "" {
		builder.WriteString("\n  ")
		builder.WriteString(truncateReplayText("SearchConversations snippet", snippet, conversationSearchReplaySnippetLimit))
	}
	return builder.String()
}

func conversationSearchSourceName(source agentv1.ConversationSearchSource) string {
	switch source {
	case agentv1.ConversationSearchSource_CONVERSATION_SEARCH_SOURCE_LOCAL:
		return "local"
	case agentv1.ConversationSearchSource_CONVERSATION_SEARCH_SOURCE_CLOUD_CACHE:
		return "cloud_cache"
	default:
		return "unknown"
	}
}

// buildSearchConversationsCompletedToolCall 构造 SearchConversations 对应的完成态 ToolCall。
// 卡片直接带上客户端原始结果，IDE 侧渲染真实命中列表；模型只读渲染后的文本。
func buildSearchConversationsCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.ConversationSearchResult) *agentv1.ToolCall {
	input, err := decodeConversationSearchArgs(argsJSON)
	if err != nil {
		input = conversationSearchToolArgs{Limit: conversationSearchDefaultLimit}
	}
	limit := input.Limit
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_SearchConversationsToolCall{
			SearchConversationsToolCall: &agentv1.SearchConversationsToolCall{
				Args: &agentv1.ConversationSearchArgs{
					Query:      input.Query,
					ToolCallId: toolCallID,
					Limit:      &limit,
				},
				Result: result,
			},
		},
	}
}
