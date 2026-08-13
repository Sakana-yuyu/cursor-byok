package forwarder

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	askQuestionCancelToolCallID   = "call-ask-question-cancel"
	askQuestionCancelInteractionD = "8801"
)

// openPendingAskQuestionStream 复现「模型问了用户一个问题、回合停在 awaiting_user」的现场：
// history 里已经有 user_message 与 AskQuestion 的 tool_call，stream 上挂着 pending
// interaction 与 15 分钟看门狗定时器，但还没有任何 tool_result。
func openPendingAskQuestionStream(t *testing.T, service *Service, requestID string, conversationID string) *ActiveStream {
	t.Helper()
	stream, err := service.broker.OpenStream(requestID, conversationID, 1, "model-id", "Model Name",
		agentv1.AgentMode_AGENT_MODE_AGENT, "帮我决定缓存方案")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := service.replaceCheckpointConversation(stream, testConversation(nil)); err != nil {
		t.Fatalf("replaceCheckpointConversation() error = %v", err)
	}
	toolCallPayload, err := protojson.Marshal(&agentv1.ToolCall{
		Tool: &agentv1.ToolCall_AskQuestionToolCall{
			AskQuestionToolCall: &agentv1.AskQuestionToolCall{
				Args: &agentv1.AskQuestionArgs{Title: "缓存方案"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal AskQuestion tool call: %v", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		testUserMessageEntry(t, 1, requestID, "帮我决定缓存方案"),
		newToolCallEntry(1, requestID, askQuestionCancelToolCallID, "AskQuestion", "", "", toolCallPayload),
	}); err != nil {
		t.Fatalf("appendConversationEntries() error = %v", err)
	}
	pending := runtimecore.PendingInteraction{
		InteractionID:   askQuestionCancelInteractionD,
		ToolCallID:      askQuestionCancelToolCallID,
		ModelCallID:     "model-call-1",
		ArgsJSON:        []byte(`{"title":"缓存方案"}`),
		InteractionKind: "ask_question",
		OpenedAt:        time.Now().UTC(),
	}
	stream.mu.Lock()
	stream.PendingInteractions[pending.InteractionID] = pending
	stream.Phase = TurnPhaseWaitingExternal
	stream.mu.Unlock()
	service.scheduleStreamTimer(
		stream,
		providerTimerKey(streamTimerInteractionTimeout, pending.InteractionID),
		defaultInteractionTimeout,
		streamTimerInteractionTimeout,
		pending.InteractionID,
		0,
		"等待用户输入超时",
	)
	return stream
}

func persistedToolResultEntries(t *testing.T, service *Service, conversationID string, toolCallID string) []toolResultEntryPayload {
	t.Helper()
	conversation, err := service.store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	if conversation == nil {
		t.Fatal("conversation was not persisted")
	}
	results := make([]toolResultEntryPayload, 0, 2)
	for _, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		var payload toolResultEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(payload.ToolCallID) == strings.TrimSpace(toolCallID) {
			results = append(results, payload)
		}
	}
	return results
}

func replaySignature(messages []modeladapter.Message) []string {
	items := make([]string, 0, len(messages))
	for _, message := range messages {
		builder := strings.Builder{}
		builder.WriteString(strings.TrimSpace(message.Role))
		builder.WriteString("|")
		builder.WriteString(strings.TrimSpace(message.ToolCallID))
		builder.WriteString("|")
		for _, toolCall := range message.ToolCalls {
			builder.WriteString(strings.TrimSpace(toolCall.ID))
			builder.WriteString(",")
		}
		builder.WriteString("|")
		builder.WriteString(strings.TrimSpace(message.Content))
		items = append(items, builder.String())
	}
	return items
}

func projectPersistedReplay(t *testing.T, service *Service, conversationID string) []modeladapter.Message {
	t.Helper()
	conversation, err := service.store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	messages, err := NewHistoryProjector().ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() error = %v", err)
	}
	return messages
}

// TestCancelIntentClosesPendingAskQuestionWithToolResult 覆盖核心缺口：
// 用户主动取消时，pending interaction 此前只被 map 清空，没有任何路径写终态，
// 那条 AskQuestion 的 tool_call 永远悬空。
func TestCancelIntentClosesPendingAskQuestionWithToolResult(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-cancel-ask-question"
	conversationID := "conv-cancel-ask-question"

	openPendingAskQuestionStream(t, service, requestID, conversationID)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	results := persistedToolResultEntries(t, service, conversationID, askQuestionCancelToolCallID)
	if len(results) != 1 {
		t.Fatalf("tool_result count for canceled AskQuestion = %d, want 1", len(results))
	}
	if got := strings.TrimSpace(results[0].ToolName); got != "AskQuestion" {
		t.Fatalf("tool_result tool_name = %q, want AskQuestion", got)
	}
}

// TestCancelIntentPublishesToolCallCompletedForPendingAskQuestion 断言取消后
// Cursor 前台的问答卡片被收口，而不是无限等待一个永远不会到来的回答。
func TestCancelIntentPublishesToolCallCompletedForPendingAskQuestion(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-cancel-ask-question-event"
	conversationID := "conv-cancel-ask-question-event"

	stream := openPendingAskQuestionStream(t, service, requestID, conversationID)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	stream.mu.Lock()
	backlog := append([]StreamEvent(nil), stream.Backlog...)
	stream.mu.Unlock()
	for _, event := range backlog {
		completed := event.Message.GetInteractionUpdate().GetToolCallCompleted()
		if completed != nil && strings.TrimSpace(completed.GetCallId()) == askQuestionCancelToolCallID {
			return
		}
	}
	t.Fatal("cancel did not publish tool_call_completed for the pending AskQuestion card")
}

// TestCancelIntentClearsInteractionWatchdogTimer 防重复收口：取消已经写过终态后，
// 15 分钟看门狗不得再补第二条 timeout tool_result。
func TestCancelIntentClearsInteractionWatchdogTimer(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-cancel-ask-question-timer"
	conversationID := "conv-cancel-ask-question-timer"

	stream := openPendingAskQuestionStream(t, service, requestID, conversationID)
	timerKey := providerTimerKey(streamTimerInteractionTimeout, askQuestionCancelInteractionD)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	stream.mu.Lock()
	_, timerStillArmed := stream.StreamTimers[timerKey]
	stream.mu.Unlock()
	if timerStillArmed {
		t.Fatal("interaction watchdog timer survived the cancel")
	}

	if err := service.recoverStaleInteractionWithoutResponse(stream, &streamTimerEvent{
		Key:    timerKey,
		Kind:   streamTimerInteractionTimeout,
		ExecID: askQuestionCancelInteractionD,
		Reason: "等待用户输入超时",
	}); err != nil {
		t.Fatalf("recoverStaleInteractionWithoutResponse() error = %v", err)
	}
	if results := persistedToolResultEntries(t, service, conversationID, askQuestionCancelToolCallID); len(results) != 1 {
		t.Fatalf("tool_result count after watchdog fire = %d, want 1 (no duplicate closure)", len(results))
	}
}

// TestInterruptedCancelKeepsAskQuestionToolCallInReplay 断言 interrupted 终态取消
// （orphan cancel 放弃路径）后，AskQuestion 的 tool_call 因为配对上了 tool_result
// 而被保留在 replay 里，不再被 trimReplayDanglingAssistantToolCalls 摘掉。
func TestInterruptedCancelKeepsAskQuestionToolCallInReplay(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-interrupted-ask-question"
	conversationID := "conv-interrupted-ask-question"

	openPendingAskQuestionStream(t, service, requestID, conversationID)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:                 "cancel",
		RequestID:            requestID,
		ConversationID:       conversationID,
		CancelReason:         "[interrupted] RunSSE client stayed disconnected",
		CancelTerminalStatus: conversationStatusInterrupted,
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	messages := projectPersistedReplay(t, service, conversationID)
	assistantKept := false
	toolResultKept := false
	for _, message := range messages {
		for _, toolCall := range message.ToolCalls {
			if strings.TrimSpace(toolCall.ID) == askQuestionCancelToolCallID {
				assistantKept = true
			}
		}
		if strings.TrimSpace(message.Role) == "tool" && strings.TrimSpace(message.ToolCallID) == askQuestionCancelToolCallID {
			toolResultKept = true
		}
	}
	if !assistantKept {
		t.Fatalf("AskQuestion tool_call was trimmed from replay: %#v", replaySignature(messages))
	}
	if !toolResultKept {
		t.Fatalf("AskQuestion tool_result missing from replay: %#v", replaySignature(messages))
	}
}

// TestCanceledTurnStripsInteractionActivityFromReplay 锁定一条容易被误判的既有语义：
// control{status:"canceled"} 会让 sanitizeCanceledReplayEntries 按 keep_stable_input
// 把整个回合的 tool_call/tool_result 都从 replay 里剔除，只留 user_message。
// 也就是说「模型记得自己问过用户」这件事，对普通取消而言不是靠补写 tool_result 恢复的
// ——补写解决的是客户端卡片收口、看门狗去重与 context.json 自洽。
func TestCanceledTurnStripsInteractionActivityFromReplay(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-canceled-strip"
	conversationID := "conv-canceled-strip"

	openPendingAskQuestionStream(t, service, requestID, conversationID)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	if results := persistedToolResultEntries(t, service, conversationID, askQuestionCancelToolCallID); len(results) != 1 {
		t.Fatalf("tool_result count = %d, want 1 (history must stay self-consistent)", len(results))
	}
	for _, message := range projectPersistedReplay(t, service, conversationID) {
		if strings.TrimSpace(message.ToolCallID) == askQuestionCancelToolCallID || len(message.ToolCalls) > 0 {
			t.Fatalf("canceled turn leaked tool activity into replay: %#v", replaySignature(projectPersistedReplay(t, service, conversationID)))
		}
	}
}

// TestCancelKeepsReplayPrefixStable 前缀缓存硬约束：取消收口只能在模型可见历史尾部
// 追加，取消前已经发出过的消息序列必须仍是取消后序列的前缀。
func TestCancelKeepsReplayPrefixStable(t *testing.T) {
	cases := []struct {
		name           string
		reason         string
		terminalStatus string
	}{
		{name: "user_stopped_generation", reason: "user_stopped_generation"},
		{name: "follow_up", reason: "new_message_submitted"},
		{name: "interrupted", reason: "[interrupted] RunSSE client stayed disconnected", terminalStatus: conversationStatusInterrupted},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService(t.TempDir(), nilResolver{})
			requestID := "req-prefix-" + testCase.name
			conversationID := "conv-prefix-" + testCase.name

			openPendingAskQuestionStream(t, service, requestID, conversationID)
			before := replaySignature(projectPersistedReplay(t, service, conversationID))

			if err := service.handleCancelIntent(InboundIntent{
				Kind:                 "cancel",
				RequestID:            requestID,
				ConversationID:       conversationID,
				CancelReason:         testCase.reason,
				CancelTerminalStatus: testCase.terminalStatus,
			}); err != nil {
				t.Fatalf("handleCancelIntent() error = %v", err)
			}

			after := replaySignature(projectPersistedReplay(t, service, conversationID))
			if len(after) < len(before) {
				t.Fatalf("replay shrank across cancel: before=%#v after=%#v", before, after)
			}
			for index := range before {
				if before[index] != after[index] {
					t.Fatalf("replay prefix changed at %d: before=%q after=%q", index, before[index], after[index])
				}
			}
		})
	}
}

// TestFollowUpCancelClosesPendingAskQuestion 覆盖后台化分支：新消息顶掉当前回合时，
// pending interaction 同样必须被收口，否则这是最容易漏掉的一条路径。
func TestFollowUpCancelClosesPendingAskQuestion(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-followup-ask-question"
	conversationID := "conv-followup-ask-question"

	openPendingAskQuestionStream(t, service, requestID, conversationID)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "new_message_submitted",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	results := persistedToolResultEntries(t, service, conversationID, askQuestionCancelToolCallID)
	if len(results) != 1 {
		t.Fatalf("tool_result count for follow-up canceled AskQuestion = %d, want 1", len(results))
	}
	if got := strings.TrimSpace(results[0].ToolName); got != "AskQuestion" {
		t.Fatalf("tool_result tool_name = %q, want AskQuestion", got)
	}
}

// TestCanceledInteractionToolResultCarriesNoReplayPolicy 防误用：这条 tool_result
// 绝不能带 replay_policy——那是 control{status:"canceled"} 专用的清洗语义。
func TestCanceledInteractionToolResultCarriesNoReplayPolicy(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	requestID := "req-cancel-ask-question-policy"
	conversationID := "conv-cancel-ask-question-policy"

	openPendingAskQuestionStream(t, service, requestID, conversationID)

	if err := service.handleCancelIntent(InboundIntent{
		Kind:           "cancel",
		RequestID:      requestID,
		ConversationID: conversationID,
		CancelReason:   "user_stopped_generation",
	}); err != nil {
		t.Fatalf("handleCancelIntent() error = %v", err)
	}

	conversation, err := service.store.LoadConversation(conversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	for _, entry := range conversation.Entries {
		if strings.TrimSpace(entry.Kind) != "tool_result" {
			continue
		}
		if policy, ok := canceledReplayPolicyForEntry(entry); ok {
			t.Fatalf("interaction closure tool_result carries cancel replay policy %q", policy)
		}
	}
}
