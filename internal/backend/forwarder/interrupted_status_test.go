package forwarder

import (
	"reflect"
	"testing"
)

func interruptedControlTestEntry(turnSeq int64, requestID string, reason string) HistoryEntry {
	return newMetadataEntry(turnSeq, requestID, "control", map[string]any{
		"status": "interrupted",
		"reason": reason,
	})
}

func TestDeriveRequestLoopStatusRecognizesInterruptedControlEntry(t *testing.T) {
	entries := []HistoryEntry{
		{Seq: 1, TurnSeq: 1, RequestID: "request-1", Kind: "user_message"},
		{Seq: 2, TurnSeq: 1, RequestID: "request-1", Kind: "assistant_text"},
		{Seq: 3, TurnSeq: 1, RequestID: "request-1", Kind: "tool_call", ToolCallID: "tool-1"},
		interruptedControlTestEntry(1, "request-1", "service restarted"),
	}

	if status := deriveRequestLoopStatus(entries, "request-1", 1, "idle"); status != "interrupted" {
		t.Fatalf("deriveRequestLoopStatus() = %q, want %q", status, "interrupted")
	}
}

func TestDeriveConversationLoopStateAfterInterruptedFollowsNewTurn(t *testing.T) {
	conversation := &ConversationFile{
		ConversationID:     "conversation-1",
		RootConversationID: "conversation-1",
		NextTurnSeq:        1,
		NextEntrySeq:       1,
	}
	appendEntriesInPlace(conversation, []HistoryEntry{
		{TurnSeq: 1, RequestID: "request-1", Kind: "user_message"},
		interruptedControlTestEntry(1, "request-1", "service restarted"),
		{TurnSeq: 2, RequestID: "request-2", Kind: "user_message"},
		{TurnSeq: 2, RequestID: "request-2", Kind: "tool_call", ToolCallID: "tool-2"},
	})

	deriveConversationLoopState(conversation)

	if conversation.CurrentLoopStatus != "waiting_tool" {
		t.Fatalf("CurrentLoopStatus = %q, want %q", conversation.CurrentLoopStatus, "waiting_tool")
	}
	if conversation.CurrentRequestID != "request-2" {
		t.Fatalf("CurrentRequestID = %q, want %q", conversation.CurrentRequestID, "request-2")
	}
}

// TestCanceledReplayPolicyForEntryIgnoresInterruptedControlEntry 是防回归断言：
// interrupted 绝不能走 canceled 的 replay 清洗策略，否则 sanitizeCanceledReplayEntries
// 会回溯性删掉该回合已发给模型的 assistant_text/tool_call/tool_result。
func TestCanceledReplayPolicyForEntryIgnoresInterruptedControlEntry(t *testing.T) {
	if policy, ok := canceledReplayPolicyForEntry(interruptedControlTestEntry(1, "request-1", "service restarted")); ok {
		t.Fatalf("canceledReplayPolicyForEntry() = (%q, true), want ok == false", policy)
	}
}

func TestPromptReplayUnchangedAfterAppendingInterruptedMarker(t *testing.T) {
	toolCall := testEditToolCall(t, "main.go")
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "改一下 main.go"),
		newAssistantTextEntry(1, "request-1", "开始修改", "", ""),
		newToolCallEntry(1, "request-1", "tool-1", "Edit", "", "", toolCall),
		newToolResultEntry(1, "request-1", "tool-1", "Edit", "{}", "已写入", "", toolCall),
	})

	projector := NewHistoryProjector()
	before, err := projector.ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() before error = %v", err)
	}
	if len(before) == 0 {
		t.Fatal("ProjectPromptReplay() before returned no messages; test setup is useless")
	}

	appendEntriesInPlace(conversation, []HistoryEntry{interruptedControlTestEntry(1, "request-1", "service restarted")})

	after, err := projector.ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() after error = %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("prompt replay changed after interrupted marker:\nbefore = %#v\nafter  = %#v", before, after)
	}
}

func TestCursorTranscriptTurnStatusMapsInterruptedToAbortedTurnEnded(t *testing.T) {
	line, ok := cursorTranscriptTurnStatus(interruptedControlTestEntry(1, "request-1", "service shutting down"))
	if !ok {
		t.Fatal("cursorTranscriptTurnStatus() ok = false, want true")
	}
	if line.Type != "turn_ended" || line.Status != "aborted" {
		t.Fatalf("cursorTranscriptTurnStatus() = %+v, want turn_ended/aborted", line)
	}

	canceled, ok := cursorTranscriptTurnStatus(newMetadataEntry(1, "request-1", "control", map[string]any{
		"status": "canceled",
		"reason": "user aborted",
	}))
	if !ok {
		t.Fatal("cursorTranscriptTurnStatus() canceled ok = false, want true")
	}
	if line.Error == canceled.Error {
		t.Fatalf("interrupted error text %q must be distinguishable from canceled", line.Error)
	}
}

// TestEmptyResumeCannotBeIgnoredForInterruptedConversation 钉死当前意图：
// interrupted 是「被打断、可继续」的会话，空 resume 必须真正跑起来，
// 不能被顺手并入 completed/idle 的忽略分支。
func TestEmptyResumeCannotBeIgnoredForInterruptedConversation(t *testing.T) {
	conversation := &ConversationFile{
		ConversationID:    "conversation-1",
		CurrentLoopStatus: "interrupted",
		CurrentRequestID:  "request-1",
	}
	if emptyResumeCanBeIgnoredForConversation(conversation) {
		t.Fatal("emptyResumeCanBeIgnoredForConversation(interrupted) = true, want false")
	}
}
