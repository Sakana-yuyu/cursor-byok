package execbridge

import (
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func conversationSearchHits(count int, snippet string) []*agentv1.ConversationSearchHit {
	hits := make([]*agentv1.ConversationSearchHit, 0, count)
	for index := 0; index < count; index++ {
		text := snippet
		hits = append(hits, &agentv1.ConversationSearchHit{
			ConversationId: "conv-" + string(rune('a'+index%26)),
			Title:          "Fixing the auth bug",
			Source:         agentv1.ConversationSearchSource_CONVERSATION_SEARCH_SOURCE_LOCAL,
			UpdatedAtMs:    1_700_000_000_000,
			Snippet:        &text,
		})
	}
	return hits
}

func conversationSearchSuccess(success *agentv1.ConversationSearchSuccess) *agentv1.ConversationSearchResult {
	return &agentv1.ConversationSearchResult{
		Result: &agentv1.ConversationSearchResult_Success{Success: success},
	}
}

func TestOpenSearchConversationsUsesTrimmedQueryAndDefaultLimit(t *testing.T) {
	bridge := NewBridge()
	serverMessage, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-1", ToolName: "SearchConversations", ArgsJSON: []byte(`{"query":"  auth bug  "}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(SearchConversations): %v", err)
	}
	if pending.ExecKind != "conversation_search" {
		t.Fatalf("exec kind = %q", pending.ExecKind)
	}
	request := serverMessage.GetExecServerMessage().GetConversationSearchArgs()
	if request == nil {
		t.Fatal("conversation search exec arm is not selected")
	}
	if request.GetQuery() != "auth bug" {
		t.Errorf("query = %q", request.GetQuery())
	}
	if request.GetLimit() != conversationSearchDefaultLimit {
		t.Errorf("limit = %d, want the default %d", request.GetLimit(), conversationSearchDefaultLimit)
	}
	if request.GetToolCallId() != "call-1" {
		t.Errorf("tool_call_id = %q", request.GetToolCallId())
	}
}

func TestOpenSearchConversationsReadsNumericLimitAndCamelCaseAlias(t *testing.T) {
	bridge := NewBridge()
	// DecodeArgsMap runs with UseNumber(), so a float64 type assertion would be
	// permanently false and would silently drop the limit.
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-2", ToolName: "SearchConversations", ArgsJSON: []byte(`{"query":"x","maxResults":7}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(SearchConversations): %v", err)
	}
	if got := serverMessage.GetExecServerMessage().GetConversationSearchArgs().GetLimit(); got != 7 {
		t.Errorf("limit = %d, want 7 from the camelCase alias", got)
	}
}

func TestOpenSearchConversationsClampsLimit(t *testing.T) {
	bridge := NewBridge()
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-3", ToolName: "SearchConversations", ArgsJSON: []byte(`{"query":"x","limit":9000}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(SearchConversations): %v", err)
	}
	if got := serverMessage.GetExecServerMessage().GetConversationSearchArgs().GetLimit(); got != conversationSearchMaxLimit {
		t.Errorf("limit = %d, want it clamped to %d", got, conversationSearchMaxLimit)
	}
}

func TestOpenSearchConversationsRequiresQuery(t *testing.T) {
	bridge := NewBridge()
	_, _, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-4", ToolName: "SearchConversations", ArgsJSON: []byte(`{"query":"   "}`),
	})
	if err == nil {
		t.Fatal("an empty query must be rejected before dispatch")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("error = %v, want it to name the query argument", err)
	}
}

func TestSummarizeConversationSearchRebuildingNeverReadsAsNoResults(t *testing.T) {
	summary := summarizeConversationSearchResult(conversationSearchSuccess(&agentv1.ConversationSearchSuccess{
		Rebuilding: true,
	}), []byte(`{"query":"auth bug"}`))
	if !strings.Contains(summary, "rebuilding") {
		t.Fatalf("the rebuilding state must be stated verbatim:\n%s", summary)
	}
	// "0 matches" while the index is rebuilding is not evidence of absence; the model
	// must be told so, or it will conclude the conversation never existed.
	if !strings.Contains(summary, "not") {
		t.Errorf("the rebuilding notice must warn that a miss is not evidence of absence:\n%s", summary)
	}
	if strings.Contains(strings.ToLower(summary), "no matching conversations") {
		t.Errorf("a rebuilding index must not be reported as a definitive empty result:\n%s", summary)
	}
}

func TestSummarizeConversationSearchPartialStateIsReported(t *testing.T) {
	summary := summarizeConversationSearchResult(conversationSearchSuccess(&agentv1.ConversationSearchSuccess{
		Hits:    conversationSearchHits(2, "snippet"),
		Partial: true,
	}), []byte(`{"query":"auth bug"}`))
	if !strings.Contains(summary, "partial") {
		t.Errorf("the partial state must be stated verbatim:\n%s", summary)
	}
	head := summary
	if len(head) > 500 {
		head = head[:500]
	}
	if !strings.Contains(head, "partial") {
		t.Errorf("index-state notices must sit at the head of the output:\n%s", head)
	}
}

func TestSummarizeConversationSearchTruncatedStateIsReported(t *testing.T) {
	summary := summarizeConversationSearchResult(conversationSearchSuccess(&agentv1.ConversationSearchSuccess{
		Hits:      conversationSearchHits(3, "snippet"),
		Truncated: true,
	}), []byte(`{"query":"auth bug","limit":3}`))
	if !strings.Contains(summary, "truncated") {
		t.Errorf("the truncated state must be stated verbatim:\n%s", summary)
	}
	if !strings.Contains(summary, "limit") {
		t.Errorf("the truncated notice must tell the model how to see more:\n%s", summary)
	}
}

func TestSummarizeConversationSearchCleanEmptyResultIsDefinitive(t *testing.T) {
	summary := summarizeConversationSearchResult(conversationSearchSuccess(&agentv1.ConversationSearchSuccess{}),
		[]byte(`{"query":"auth bug"}`))
	if strings.Contains(summary, "rebuilding") || strings.Contains(summary, "partial") {
		t.Errorf("a healthy index must not carry index-state warnings:\n%s", summary)
	}
	if !strings.Contains(summary, "0 matches") {
		t.Errorf("summary = %q", summary)
	}
}

func TestSummarizeConversationSearchRendersHitFields(t *testing.T) {
	summary := summarizeConversationSearchResult(conversationSearchSuccess(&agentv1.ConversationSearchSuccess{
		Hits: conversationSearchHits(1, "we fixed the token refresh"),
	}), []byte(`{"query":"auth bug"}`))
	for _, want := range []string{"conv-a", "Fixing the auth bug", "local", "we fixed the token refresh"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary)
		}
	}
}

func TestSummarizeConversationSearchTruncatesLongOutputWithNoticeAtTheStart(t *testing.T) {
	summary := summarizeConversationSearchResult(conversationSearchSuccess(&agentv1.ConversationSearchSuccess{
		Hits: conversationSearchHits(conversationSearchReplayHitLimit+5, strings.Repeat("s", 4000)),
	}), []byte(`{"query":"auth bug"}`))
	if len(summary) > conversationSearchReplayContentLimit {
		t.Fatalf("summary is %d bytes, want at most %d", len(summary), conversationSearchReplayContentLimit)
	}
	head := summary
	if len(head) > 600 {
		head = head[:600]
	}
	// The overall cap cuts the tail, so a notice placed at the end would be cut with it.
	if !strings.Contains(head, "[truncated: listed") {
		t.Errorf("the hit-count notice must survive at the head of the output:\n%s", head)
	}
	if strings.Contains(summary, strings.Repeat("s", conversationSearchReplaySnippetLimit+1)) {
		t.Errorf("a single snippet must be capped at %d bytes", conversationSearchReplaySnippetLimit)
	}
}

func TestSummarizeConversationSearchErrorArms(t *testing.T) {
	if got := summarizeConversationSearchResult(nil, []byte(`{}`)); got != "conversation search result missing" {
		t.Errorf("summary = %q", got)
	}
	failed := summarizeConversationSearchResult(&agentv1.ConversationSearchResult{
		Result: &agentv1.ConversationSearchResult_Error{Error: &agentv1.ConversationSearchError{Error: "boom"}},
	}, []byte(`{}`))
	if !strings.Contains(failed, "boom") {
		t.Errorf("summary = %q", failed)
	}
}

func TestApplyConversationSearchBuildsSearchConversationsToolCall(t *testing.T) {
	bridge := NewBridge()
	_, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-5", ToolName: "SearchConversations", ArgsJSON: []byte(`{"query":"auth bug","limit":5}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(SearchConversations): %v", err)
	}
	applied, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id: pending.MessageID, ExecId: pending.ExecID,
		Message: &agentv1.ExecClientMessage_ConversationSearchResult{
			ConversationSearchResult: conversationSearchSuccess(&agentv1.ConversationSearchSuccess{
				Hits: conversationSearchHits(1, "snippet"),
			}),
		},
	}, pending)
	if err != nil {
		t.Fatalf("ApplyExecClientMessage(SearchConversations): %v", err)
	}
	if !applied.IsTerminal {
		t.Error("conversation search result must be terminal")
	}
	toolCall := applied.ToolCall.GetSearchConversationsToolCall()
	if toolCall == nil {
		t.Fatalf("SearchConversations must render through its own ToolCall arm: %#v", applied.ToolCall)
	}
	if toolCall.GetArgs().GetQuery() != "auth bug" || toolCall.GetArgs().GetLimit() != 5 {
		t.Errorf("tool call args = %#v", toolCall.GetArgs())
	}
	if toolCall.GetArgs().GetToolCallId() != "call-5" {
		t.Errorf("tool_call_id = %q", toolCall.GetArgs().GetToolCallId())
	}
	// The card must carry the client's own result so the IDE renders real hits,
	// while the model gets the rendered text payload.
	if len(toolCall.GetResult().GetSuccess().GetHits()) != 1 {
		t.Errorf("tool call result = %#v", toolCall.GetResult())
	}
	if applied.ToolResultPayload == "" {
		t.Error("the model payload must not be empty")
	}
}

func TestApplyConversationSearchCarriesErrorIntoToolCall(t *testing.T) {
	bridge := NewBridge()
	_, pending, err := bridge.OpenExec(OpenExecContext{}, runtimecore.ToolInvocation{
		CallID: "call-6", ToolName: "SearchConversations", ArgsJSON: []byte(`{"query":"auth bug"}`),
	})
	if err != nil {
		t.Fatalf("OpenExec(SearchConversations): %v", err)
	}
	applied, err := bridge.ApplyExecClientMessage(&agentv1.ExecClientMessage{
		Id: pending.MessageID, ExecId: pending.ExecID,
		Message: &agentv1.ExecClientMessage_ConversationSearchResult{
			ConversationSearchResult: &agentv1.ConversationSearchResult{
				Result: &agentv1.ConversationSearchResult_Error{Error: &agentv1.ConversationSearchError{Error: "boom"}},
			},
		},
	}, pending)
	if err != nil {
		t.Fatalf("ApplyExecClientMessage(SearchConversations): %v", err)
	}
	if got := applied.ToolCall.GetSearchConversationsToolCall().GetResult().GetError().GetError(); got != "boom" {
		t.Errorf("tool call error = %q", got)
	}
	if !strings.Contains(applied.ToolResultPayload, "boom") {
		t.Errorf("model payload = %q", applied.ToolResultPayload)
	}
}
