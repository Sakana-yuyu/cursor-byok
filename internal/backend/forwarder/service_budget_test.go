package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	legacyruntime "cursor/internal/runtime"
)

func TestCompactActiveTurnToolResultsForBudgetKeepsLatestPairAndCanonicalHistory(t *testing.T) {
	const (
		turnSeq   = int64(3)
		requestID = "request-3"
	)
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, turnSeq, requestID, "inspect the failure"),
		newToolCallEntry(turnSeq, requestID, "call-1", "Read", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-1", "Read", `{"path":"first.log"}`, strings.Repeat("first-result-", 7_000), "", nil),
		newToolCallEntry(turnSeq, requestID, "call-2", "Shell", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-2", "Shell", `{"command":"inspect"}`, strings.Repeat("second-result-", 7_000), "", nil),
		newToolCallEntry(turnSeq, requestID, "call-3", "Grep", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-3", "Grep", `{"pattern":"error"}`, strings.Repeat("latest-result-", 1_500), "", nil),
	})
	conversation.CurrentTurnSeq = turnSeq
	conversation.CurrentRequestID = requestID
	canonicalBefore := cloneConversationFile(conversation)

	latestResult := strings.Repeat("latest-result-", 1_500)
	compiled := CompiledConversation{Messages: []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "inspect the failure"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-1", Function: modeladapter.ToolCallFunctionShape{Name: "Read", Arguments: `{"path":"first.log"}`}}}},
		{Role: "tool", Name: "Read", ToolCallID: "call-1", Content: strings.Repeat("first-result-", 7_000)},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-2", Function: modeladapter.ToolCallFunctionShape{Name: "Shell", Arguments: `{"command":"inspect"}`}}}},
		{Role: "tool", Name: "Shell", ToolCallID: "call-2", Content: strings.Repeat("second-result-", 7_000)},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-3", Function: modeladapter.ToolCallFunctionShape{Name: "Grep", Arguments: `{"pattern":"error"}`}}}},
		{Role: "tool", Name: "Grep", ToolCallID: "call-3", Content: latestResult},
	}, StableMessageCount: 8}
	const budgetTokens = int64(12_000)
	if before := estimateCompiledPromptTokens(compiled); before <= budgetTokens {
		t.Fatalf("test setup input tokens = %d, want above budget %d", before, budgetTokens)
	}

	projected, stats, ok := compactActiveTurnToolResultsForBudget(conversation, compiled, budgetTokens)
	if !ok {
		t.Fatalf("compactActiveTurnToolResultsForBudget() ok = false, stats = %#v", stats)
	}
	if after := estimateCompiledPromptTokens(projected); after > budgetTokens {
		t.Fatalf("projected input tokens = %d, want <= %d", after, budgetTokens)
	}
	if stats.ShortenedResults != 2 || stats.LatestToolCallID != "call-3" {
		t.Fatalf("projection stats = %#v, want two shortened results and latest call-3", stats)
	}
	if projected.Messages[7].Content != latestResult {
		t.Fatal("latest tool result was changed")
	}
	for _, index := range []int{3, 5} {
		message := projected.Messages[index]
		if !strings.Contains(message.Content, "omitted middle") || !strings.Contains(message.Content, "of ") {
			t.Fatalf("earlier tool result %d has no explicit truncation metadata: %q", index, message.Content)
		}
	}
	for index, callID := range []string{"call-1", "call-2", "call-3"} {
		assistantMessage := projected.Messages[2+index*2]
		toolMessage := projected.Messages[3+index*2]
		if len(assistantMessage.ToolCalls) != 1 || assistantMessage.ToolCalls[0].ID != callID || toolMessage.ToolCallID != callID {
			t.Fatalf("tool pair %d was broken: assistant=%#v tool=%#v", index, assistantMessage, toolMessage)
		}
	}
	if projected.StableMessageCount != 3 {
		t.Fatalf("stable message count = %d, want first rewritten index 3", projected.StableMessageCount)
	}
	if !reflect.DeepEqual(conversation, canonicalBefore) {
		t.Fatal("active-turn projection modified canonical conversation")
	}
}

func TestCompactActiveTurnToolResultsForBudgetUsesNewestMessageWhenToolCallIDRepeats(t *testing.T) {
	const (
		turnSeq   = int64(2)
		requestID = "request-2"
	)
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, turnSeq, requestID, "continue"),
		newToolCallEntry(turnSeq, requestID, "call-reused", "Read", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-reused", "Read", `{}`, strings.Repeat("current-result-", 6_000), "", nil),
		newToolCallEntry(turnSeq, requestID, "call-latest", "Grep", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-latest", "Grep", `{}`, "latest", "", nil),
	})
	conversation.CurrentTurnSeq = turnSeq
	conversation.CurrentRequestID = requestID
	historicalResult := strings.Repeat("historical-result-", 2_000)
	currentResult := strings.Repeat("current-result-", 6_000)
	compiled := CompiledConversation{Messages: []modeladapter.Message{
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-reused"}}},
		{Role: "tool", Name: "Read", ToolCallID: "call-reused", Content: historicalResult},
		{Role: "user", Content: "continue"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-reused"}}},
		{Role: "tool", Name: "Read", ToolCallID: "call-reused", Content: currentResult},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-latest"}}},
		{Role: "tool", Name: "Grep", ToolCallID: "call-latest", Content: "latest"},
	}}
	before := estimateCompiledPromptTokens(compiled)
	projected, stats, ok := compactActiveTurnToolResultsForBudget(conversation, compiled, before-10_000)
	if !ok {
		t.Fatalf("compactActiveTurnToolResultsForBudget() ok = false, stats = %#v", stats)
	}
	if projected.Messages[1].Content != historicalResult {
		t.Fatal("historical result with a reused tool_call_id was changed")
	}
	if projected.Messages[4].Content == currentResult {
		t.Fatal("active-turn result with the reused tool_call_id was not shortened")
	}
}

func TestCompactActiveTurnToolResultsForBudgetOmissionKeepsOriginalSize(t *testing.T) {
	const (
		turnSeq   = int64(4)
		requestID = "request-4"
	)
	earlierResult := strings.Repeat("earlier-result-", 6_000)
	latestResult := strings.Repeat("latest-result-", 6_000)
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, turnSeq, requestID, "inspect"),
		newToolCallEntry(turnSeq, requestID, "call-1", "Read", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-1", "Read", `{}`, earlierResult, "", nil),
		newToolCallEntry(turnSeq, requestID, "call-2", "Shell", "", "", nil),
		newToolResultEntry(turnSeq, requestID, "call-2", "Shell", `{}`, latestResult, "", nil),
	})
	conversation.CurrentTurnSeq = turnSeq
	conversation.CurrentRequestID = requestID
	compiled := CompiledConversation{Messages: []modeladapter.Message{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-1"}}},
		{Role: "tool", Name: "Read", ToolCallID: "call-1", Content: earlierResult},
		{Role: "assistant", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "call-2"}}},
		{Role: "tool", Name: "Shell", ToolCallID: "call-2", Content: latestResult},
	}}
	projected, stats, ok := compactActiveTurnToolResultsForBudget(conversation, compiled, 1)
	if ok {
		t.Fatal("compactActiveTurnToolResultsForBudget() ok = true, want latest result to keep request over budget")
	}
	if stats.OmittedResults != 1 {
		t.Fatalf("omitted results = %d, want 1", stats.OmittedResults)
	}
	omitted := projected.Messages[2].Content
	if !strings.Contains(omitted, "Read result omitted") || !strings.Contains(omitted, "original_bytes="+fmt.Sprint(len(earlierResult))) {
		t.Fatalf("omission marker lacks tool name or original size: %q", omitted)
	}
	if projected.Messages[4].Content != latestResult {
		t.Fatal("latest tool result was changed during full omission fallback")
	}
}

func TestDriveProviderShrinksEarlierActiveTurnToolResultsAfterHistoryProjection(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	toolCall := testEditToolCall(t, "trace.log")
	const (
		turnSeq   = int64(3)
		requestID = "request-3"
	)
	latestResult := strings.Repeat("latest-result-", 1_500)
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "older question"),
		newAssistantTextEntry(1, "request-1", "older answer", "", ""),
		testUserMessageEntry(t, turnSeq, requestID, "inspect the failure"),
		newToolCallEntry(turnSeq, requestID, "call-1", "Read", "", "", toolCall),
		newToolResultEntry(turnSeq, requestID, "call-1", "Read", `{"path":"first.log"}`, strings.Repeat("first-result-", 7_000), "", toolCall),
		newToolCallEntry(turnSeq, requestID, "call-2", "Shell", "", "", toolCall),
		newToolResultEntry(turnSeq, requestID, "call-2", "Shell", `{"command":"inspect"}`, strings.Repeat("second-result-", 7_000), "", toolCall),
		newToolCallEntry(turnSeq, requestID, "call-3", "Grep", "", "", toolCall),
		newToolResultEntry(turnSeq, requestID, "call-3", "Grep", `{"pattern":"error"}`, latestResult, "", toolCall),
	})
	conversation.TokenDetailsMaxTokens = 40_000
	conversation.CurrentTurnSeq = turnSeq
	conversation.CurrentRequestID = requestID
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	projection, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 1),
		lastEntrySeqForTurn(persisted.Entries, 1),
		"older turn summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, projection); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}

	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
	service.resolver = fixedContextWindowResolver{tokens: 40_000}
	stream, err := broker.OpenStream(requestID, persisted.ConversationID, turnSeq, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "inspect the failure")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)

	if err := service.driveProvider(stream); err != nil {
		t.Fatalf("driveProvider() error = %v", err)
	}
	var request ProviderRequest
	select {
	case request = <-provider.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("provider request was not started after active-turn emergency projection")
	}
	budgetTokens := int64(float64(conversation.TokenDetailsMaxTokens)*contextProjectionHardRatio) - compactionAutoReserveTokens
	if inputTokens := estimateCompiledPromptTokens(CompiledConversation{Messages: request.Messages, Tools: request.Tools}); inputTokens > budgetTokens {
		t.Fatalf("provider input tokens = %d, want <= %d", inputTokens, budgetTokens)
	}
	toolResults := make(map[string]modeladapter.Message)
	toolCalls := make(map[string]struct{})
	for _, message := range request.Messages {
		for _, call := range message.ToolCalls {
			toolCalls[call.ID] = struct{}{}
		}
		if message.Role == "tool" {
			toolResults[message.ToolCallID] = message
		}
	}
	for _, callID := range []string{"call-1", "call-2", "call-3"} {
		if _, ok := toolCalls[callID]; !ok {
			t.Fatalf("provider request omitted tool call %s", callID)
		}
		if _, ok := toolResults[callID]; !ok {
			t.Fatalf("provider request omitted tool result %s", callID)
		}
	}
	if toolResults["call-3"].Content != latestResult {
		t.Fatal("provider request changed the latest tool result")
	}
	for _, callID := range []string{"call-1", "call-2"} {
		if !strings.Contains(toolResults[callID].Content, "omitted middle") {
			t.Fatalf("provider request did not shorten earlier result %s: %q", callID, toolResults[callID].Content)
		}
	}
	diagnostics, ok := request.RequestKnobs["context_projection"].(map[string]any)
	if !ok || diagnostics["active_turn_shortened_results"] != 2 {
		t.Fatalf("context projection diagnostics = %#v, want two shortened active-turn results", request.RequestKnobs)
	}
	loaded, err := store.LoadConversation(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	canonicalResults := make(map[string]string)
	for _, entry := range loaded.Entries {
		if entry.TurnSeq != turnSeq || entry.RequestID != requestID || entry.Kind != "tool_result" {
			continue
		}
		var payload toolResultEntryPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatalf("decode canonical tool result: %v", err)
		}
		canonicalResults[payload.ToolCallID] = payload.ResultText
	}
	for callID, want := range map[string]string{
		"call-1": strings.Repeat("first-result-", 7_000),
		"call-2": strings.Repeat("second-result-", 7_000),
		"call-3": latestResult,
	} {
		if canonicalResults[callID] != want {
			t.Fatalf("canonical tool result %s was modified: got bytes=%d want bytes=%d", callID, len(canonicalResults[callID]), len(want))
		}
	}
}

func TestValidateProviderRequestContextBudgetRejectsOverflowAfterOutputBudgeting(t *testing.T) {
	conversation := &ConversationFile{TokenDetailsMaxTokens: 2_000}
	compiled := CompiledConversation{
		Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 3_000)}},
	}

	err := validateProviderRequestContextBudget(conversation, compiled, 1)
	if err == nil {
		t.Fatal("validateProviderRequestContextBudget() error = nil, want context overflow")
	}
	terminal, ok := err.(compactionTerminalError)
	if !ok {
		t.Fatalf("error type = %T, want compactionTerminalError", err)
	}
	if terminal.TerminalCode() != compactionOverflowTerminalCode {
		t.Fatalf("terminal code = %q, want %q", terminal.TerminalCode(), compactionOverflowTerminalCode)
	}
}

func TestValidateProviderRequestContextBudgetAllowsExactSafetyBoundary(t *testing.T) {
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: "hello"}}}
	inputTokens := estimateCompiledPromptTokens(compiled)
	conversation := &ConversationFile{TokenDetailsMaxTokens: uint32(inputTokens + 7 + providerOutputSafetyTokens)}

	if err := validateProviderRequestContextBudget(conversation, compiled, 7); err != nil {
		t.Fatalf("validateProviderRequestContextBudget() error = %v, want nil", err)
	}
}

type unresolvedChannelResolver struct{}

func (unresolvedChannelResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return nil, legacyruntime.ErrChannelNotAvailable
}
func (unresolvedChannelResolver) ProviderStreamIdleTimeout(context.Context) time.Duration { return 0 }
func (unresolvedChannelResolver) TurnStaleTimeout(context.Context) time.Duration          { return 0 }
func (unresolvedChannelResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 0
}

type fixedContextWindowResolver struct {
	tokens int
}

func (r fixedContextWindowResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return &legacyruntime.ResolvedChannel{ContextWindowTokens: r.tokens}, nil
}
func (fixedContextWindowResolver) ProviderStreamIdleTimeout(context.Context) time.Duration { return 0 }
func (fixedContextWindowResolver) TurnStaleTimeout(context.Context) time.Duration          { return 0 }
func (fixedContextWindowResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 0
}

func TestResolveContextWindowTokensFallsBackToDefaultChannelWindow(t *testing.T) {
	service := &Service{}
	if got := service.resolveContextWindowTokens("missing-model"); got != defaultResolvedContextWindowTokens {
		t.Fatalf("nil resolver fallback = %d, want %d", got, defaultResolvedContextWindowTokens)
	}

	service = &Service{resolver: unresolvedChannelResolver{}}
	if got := service.resolveContextWindowTokens("missing-model"); got != defaultResolvedContextWindowTokens {
		t.Fatalf("unresolved channel fallback = %d, want %d", got, defaultResolvedContextWindowTokens)
	}
}

func TestContextProjectionPressureNotTriggeredForModeratePromptWith200KWindow(t *testing.T) {
	service := &Service{resolver: unresolvedChannelResolver{}}
	conversation := &ConversationFile{TokenDetailsMaxTokens: service.resolveContextWindowTokens("channel-a")}
	compiled := CompiledConversation{Messages: []modeladapter.Message{
		{Role: "system", Content: strings.Repeat("system-", 20_000)},
		{Role: "user", Content: "hello"},
	}}
	budgetTokens := int64(float64(conversation.TokenDetailsMaxTokens)*contextProjectionHardRatio) - compactionAutoReserveTokens
	inputTokens := estimateCompiledPromptTokens(compiled)
	if inputTokens > budgetTokens {
		t.Fatalf("test setup input tokens = %d, want <= budget %d", inputTokens, budgetTokens)
	}
	if service.contextProjectionPressureExceeded(&ActiveStream{ModelID: "channel-a"}, conversation, compiled) {
		t.Fatal("contextProjectionPressureExceeded() = true, want false for moderate prompt with 200K fallback window")
	}

	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	oldBudget := int64(float64(projectedConversationMaxTokens)*contextProjectionHardRatio) - compactionAutoReserveTokens
	if inputTokens <= oldBudget {
		t.Fatalf("test setup input tokens = %d, want above legacy 64K budget %d", inputTokens, oldBudget)
	}
	if !service.contextProjectionPressureExceeded(&ActiveStream{ModelID: "channel-a"}, conversation, compiled) {
		t.Fatal("contextProjectionPressureExceeded() = false, want true when window falls back to 64K")
	}
}
