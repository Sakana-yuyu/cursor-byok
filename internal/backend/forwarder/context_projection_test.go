package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

type contextProjectionSummaryProvider struct {
	err            error
	invokeObserver bool
}

func (provider contextProjectionSummaryProvider) StartStream(_ context.Context, request ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	if provider.invokeObserver && request.Observer != nil {
		if _, err := request.Observer.RecordLLMRequest(request.RequestID, request.RunID, request.ModelCallID, map[string]any{
			"provider": "test-provider",
			"model":    "test-model",
		}); err != nil {
			return err
		}
	}
	if provider.err != nil {
		return provider.err
	}
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "rolling summary"}); err != nil {
		return err
	}
	return sink(modeladapter.ModelEvent{
		Kind:         modeladapter.ModelEventKindTurnFinished,
		Provider:     "test-provider",
		Model:        "test-model",
		InputTokens:  42,
		OutputTokens: 7,
		UsagePresent: true,
	})
}

type contextProjectionRequestProvider struct {
	requests  chan ProviderRequest
	emitEvent bool
	err       error
}

func (provider *contextProjectionRequestProvider) StartStream(_ context.Context, request ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	provider.requests <- request
	if provider.emitEvent {
		if err := sink(modeladapter.ModelEvent{}); err != nil {
			return err
		}
	}
	if provider.err != nil {
		return provider.err
	}
	return errProviderLoopInterrupted
}

// contextProjectionTimeoutProvider 模拟自动投影摘要长时间没有返回，而主模型请求
// 在最近尾部回退后应立即被重新调度。
type contextProjectionTimeoutProvider struct {
	summaryStarted chan struct{}
	parentRequests chan ProviderRequest
}

func (provider *contextProjectionTimeoutProvider) StartStream(ctx context.Context, request ProviderRequest, _ func(modeladapter.ModelEvent) error) error {
	if strings.Contains(request.CompileSummary, "compaction trigger=auto_projection") {
		select {
		case provider.summaryStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	select {
	case provider.parentRequests <- request:
	default:
	}
	return errProviderLoopInterrupted
}

type contextProjectionLifecycleCompiler struct{}

func (contextProjectionLifecycleCompiler) Compile(conversation *ConversationFile, mode agentv1.AgentMode, _ string, _ string, _ string) (CompiledConversation, error) {
	messages, err := NewHistoryProjector().ProjectPromptReplay(conversation)
	if err != nil {
		return CompiledConversation{}, err
	}
	return CompiledConversation{
		Mode:               mode,
		Messages:           append([]modeladapter.Message{{Role: "system", Content: "system"}}, messages...),
		StableMessageCount: 4,
		Tools: []json.RawMessage{
			json.RawMessage(`{"type":"function","function":{"name":"SeeImage","parameters":{"type":"object"}}}`),
			json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`),
		},
	}, nil
}

func (contextProjectionLifecycleCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

func TestContextProjectionStoreRoundTripDoesNotModifyCanonicalFiles(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(store.conversationDir(persisted.ConversationID), conversationStateFileName))
	if err != nil {
		t.Fatalf("read state before projection: %v", err)
	}
	contextBefore, err := os.ReadFile(filepath.Join(store.conversationDir(persisted.ConversationID), conversationContextFileName))
	if err != nil {
		t.Fatalf("read context before projection: %v", err)
	}

	projection, err := newContextProjectionState(persisted, "model-a", persisted.Entries[0].Seq, persisted.Entries[len(persisted.Entries)-1].Seq, "rolling summary")
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, projection); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	loaded, err := store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if loaded == nil || loaded.Summary != "rolling summary" || loaded.CoveredPrefixFingerprint != projection.CoveredPrefixFingerprint {
		t.Fatalf("loaded projection = %#v, want %#v", loaded, projection)
	}

	stateAfter, err := os.ReadFile(filepath.Join(store.conversationDir(persisted.ConversationID), conversationStateFileName))
	if err != nil {
		t.Fatalf("read state after projection: %v", err)
	}
	contextAfter, err := os.ReadFile(filepath.Join(store.conversationDir(persisted.ConversationID), conversationContextFileName))
	if err != nil {
		t.Fatalf("read context after projection: %v", err)
	}
	if string(stateAfter) != string(stateBefore) {
		t.Fatal("saving projection modified state.json")
	}
	if string(contextAfter) != string(contextBefore) {
		t.Fatal("saving projection modified context.json")
	}
}

func TestCompleteContextProjectionSummaryWritesOnlySidecar(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 8)
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	statePath := filepath.Join(service.store.conversationDir(persisted.ConversationID), conversationStateFileName)
	contextPath := filepath.Join(service.store.conversationDir(persisted.ConversationID), conversationContextFileName)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state before completion: %v", err)
	}
	contextBefore, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read context before completion: %v", err)
	}
	plan, err := buildContextProjectionSummaryPlan(persisted, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil || plan == nil {
		t.Fatalf("buildContextProjectionSummaryPlan() = (%#v, %v)", plan, err)
	}

	if err := service.completeContextProjectionSummary(persisted, newPendingCompaction(plan), "rolling summary"); err != nil {
		t.Fatalf("completeContextProjectionSummary() error = %v", err)
	}
	projection, err := service.store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if projection == nil || projection.Summary != "rolling summary" {
		t.Fatalf("projection = %#v, want rolling summary", projection)
	}
	stateAfter, _ := os.ReadFile(statePath)
	contextAfter, _ := os.ReadFile(contextPath)
	if !reflect.DeepEqual(stateAfter, stateBefore) {
		t.Fatal("automatic projection completion modified state.json")
	}
	if !reflect.DeepEqual(contextAfter, contextBefore) {
		t.Fatal("automatic projection completion modified context.json")
	}
}

func TestHandleContextProjectionSummaryFailureFallsBackToRecentTailSidecar(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, NewStreamBroker())
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	plan, err := buildContextProjectionSummaryPlan(persisted, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil || plan == nil {
		t.Fatalf("buildContextProjectionSummaryPlan() = (%#v, %v)", plan, err)
	}
	stream, err := service.broker.OpenStream("request-8", persisted.ConversationID, 8, "model-a", "model-a", agentv1.AgentMode_AGENT_MODE_AGENT, "question 8")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.Status = StreamStatusStreaming
	stream.CurrentCompactionToken = 1
	stream.PendingCompaction = newPendingCompaction(plan)

	if err := service.handleCompactionEvent(stream, &streamCompactionEvent{
		Token: 1,
		Plan:  newPendingCompaction(plan),
		Err:   errors.New("summary provider unavailable"),
	}); err != nil {
		t.Fatalf("handleCompactionEvent() error = %v", err)
	}
	fallback, err := service.store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if fallback == nil || fallback.Mode != contextProjectionModeRecentTail || fallback.Summary != "" {
		t.Fatalf("fallback projection = %#v, want recent-tail sidecar without summary", fallback)
	}
	projected, err := projectConversationWithContextProjection(persisted, fallback, "model-a")
	if err != nil {
		t.Fatalf("projectConversationWithContextProjection() error = %v", err)
	}
	messages, err := NewHistoryProjector().ProjectPromptReplay(projected)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() error = %v", err)
	}
	if text := contextProjectionMessageText(messages); strings.Contains(text, "<conversation_summary>") {
		t.Fatalf("recent-tail fallback inserted a summary: %s", text)
	}
	select {
	case <-provider.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("recent-tail fallback did not resume the provider pass")
	}
	waitForContextProjectionProviderIdle(t, stream)
}

// TestAutomaticContextProjectionSummaryTimesOutIntoRecentTail 验证自动维护的
// 性能边界：摘要卡住时不能无限阻塞下一次主模型请求，最近尾部回退仍需保留可用上下文。
func TestAutomaticContextProjectionSummaryTimesOutIntoRecentTail(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	provider := &contextProjectionTimeoutProvider{
		summaryStarted: make(chan struct{}, 1),
		parentRequests: make(chan ProviderRequest, 1),
	}
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, NewStreamBroker())
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-timeout"
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	plan, err := buildContextProjectionSummaryPlan(persisted, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil || plan == nil {
		t.Fatalf("buildContextProjectionSummaryPlan() = (%#v, %v)", plan, err)
	}
	stream, err := service.broker.OpenStream(
		"request-timeout",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	defer func() { _ = service.broker.Cancel(stream.RequestID, "test cleanup") }()
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.Status = StreamStatusStreaming
	stream.PendingCompaction = newPendingCompaction(plan)

	if err := service.startPendingCompactionSummary(stream, stream.PendingCompaction); err != nil {
		t.Fatalf("startPendingCompactionSummary() error = %v", err)
	}
	select {
	case <-provider.summaryStarted:
	case <-time.After(time.Second):
		t.Fatal("自动投影摘要没有启动")
	}

	select {
	case request := <-provider.parentRequests:
		if strings.Contains(request.CompileSummary, "compaction trigger=auto_projection") {
			t.Fatalf("等待到的仍是摘要请求：%#v", request)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("自动投影摘要超时后没有及时恢复主模型请求，TTFT 会被摘要阻塞")
	}

	fallback, err := service.store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if fallback == nil || fallback.Mode != contextProjectionModeRecentTail {
		t.Fatalf("fallback projection = %#v，want recent-tail", fallback)
	}

	select {
	case <-provider.summaryStarted:
		t.Fatal("recent-tail fallback unexpectedly retried automatic summary")
	case <-time.After(contextProjectionSummaryTimeout + 250*time.Millisecond):
	}
}

func TestCompactionSummaryTimeoutOnlyAppliesToAutomaticProjection(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	autoCtx, autoCancel := service.newCompactionSummaryContext(newPendingCompaction(&compactionPlan{Trigger: contextProjectionTrigger}))
	defer autoCancel()
	if _, ok := autoCtx.Deadline(); !ok {
		t.Fatal("自动上下文投影摘要必须有首字预算截止时间")
	}

	manualCtx, manualCancel := service.newCompactionSummaryContext(newPendingCompaction(&compactionPlan{Trigger: "manual"}))
	defer manualCancel()
	if _, ok := manualCtx.Deadline(); ok {
		t.Fatal("用户手动压缩不应继承自动投影的短时限")
	}
}

func TestGenerateContextProjectionSummaryDoesNotModifyCanonicalFiles(t *testing.T) {
	tests := []struct {
		name     string
		provider ProviderGateway
		wantErr  bool
	}{
		{name: "completed", provider: contextProjectionSummaryProvider{invokeObserver: true}},
		{name: "provider error", provider: contextProjectionSummaryProvider{err: errors.New("summary provider unavailable"), invokeObserver: true}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewConversationFileStore(t.TempDir())
			conversation := contextProjectionTestConversation(t, 8)
			persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
			if err != nil {
				t.Fatalf("SaveConversationWithEntries() error = %v", err)
			}
			service := newServiceWithDependencies(store, NewHistoryProjector(), nil, test.provider, NewStreamBroker())
			stream, err := service.broker.OpenStream(
				"request-8",
				persisted.ConversationID,
				8,
				"model-a",
				"model-a",
				agentv1.AgentMode_AGENT_MODE_AGENT,
				"question 8",
			)
			if err != nil {
				t.Fatalf("OpenStream() error = %v", err)
			}
			stream.CheckpointConversation = cloneConversationFile(persisted)
			statePath := filepath.Join(store.conversationDir(persisted.ConversationID), conversationStateFileName)
			contextPath := filepath.Join(store.conversationDir(persisted.ConversationID), conversationContextFileName)
			stateBefore, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatalf("read state before summary: %v", err)
			}
			contextBefore, err := os.ReadFile(contextPath)
			if err != nil {
				t.Fatalf("read context before summary: %v", err)
			}

			plan := newPendingCompaction(&compactionPlan{
				Trigger:        contextProjectionTrigger,
				CompactedTurns: []compactedTurnSummary{{UserText: "older context"}},
			})
			_, err = service.generateCompactionSummary(context.Background(), stream, plan, "summary-call")
			if test.wantErr && err == nil {
				t.Fatal("generateCompactionSummary() error = nil, want provider error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("generateCompactionSummary() error = %v", err)
			}
			stateAfter, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatalf("read state after summary: %v", err)
			}
			contextAfter, err := os.ReadFile(contextPath)
			if err != nil {
				t.Fatalf("read context after summary: %v", err)
			}
			if !reflect.DeepEqual(stateAfter, stateBefore) {
				t.Fatal("automatic projection provider lifecycle modified state.json")
			}
			if !reflect.DeepEqual(contextAfter, contextBefore) {
				t.Fatal("automatic projection provider lifecycle modified context.json")
			}
		})
	}
}

func TestValidateContextProjectionAllowsCanonicalTailAppend(t *testing.T) {
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
	})
	conversation.RootConversationID = "root-1"
	conversation.ParentConversationID = "parent-1"
	conversation.ParentToolCallID = "tool-1"
	appendEntriesInPlace(conversation, nil)
	projection, err := newContextProjectionState(conversation, "model-a", conversation.Entries[0].Seq, conversation.Entries[len(conversation.Entries)-1].Seq, "summary")
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}

	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 2, "request-2", "second question"),
		newAssistantTextEntry(2, "request-2", "second answer", "", ""),
	})
	valid, reason := validateContextProjectionState(projection, conversation, "model-a")
	if !valid {
		t.Fatalf("projection invalid after append: reason=%q", reason)
	}
}

func TestContextProjectionStableFrontierResetsUntilSidecarIsApplied(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 8)
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"rolling summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if got := contextProjectionStableMessageCount(state, 7); got != 0 {
		t.Fatalf("first projected stable count = %d, want 0", got)
	}
	if err := service.store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	if err := service.markContextProjectionApplied(persisted, state); err != nil {
		t.Fatalf("markContextProjectionApplied() error = %v", err)
	}
	applied, err := service.store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if applied == nil || !applied.Applied {
		t.Fatalf("applied projection = %#v, want applied sidecar", applied)
	}
	if got := contextProjectionStableMessageCount(applied, 7); got != 7 {
		t.Fatalf("reused projected stable count = %d, want 7", got)
	}
}

func TestDriveProviderAppliesProjectionStableFrontierOnlyOnce(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 3), emitEvent: true}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
	service.resolver = fixedContextWindowResolver{tokens: projectedConversationMaxTokens}
	stream, err := broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)

	requests := make([]ProviderRequest, 0, 3)
	for pass := 0; pass < 3; pass++ {
		if err := service.driveProvider(stream); err != nil {
			t.Fatalf("driveProvider(pass %d) error = %v", pass+1, err)
		}
		select {
		case request := <-provider.requests:
			requests = append(requests, request)
		case <-time.After(2 * time.Second):
			t.Fatalf("provider request %d was not started", pass+1)
		}
		waitForContextProjectionProviderIdle(t, stream)
	}

	firstText := contextProjectionMessageText(requests[0].Messages)
	if !strings.Contains(firstText, "turns two and three summarized") {
		t.Fatalf("first provider request omitted projection summary: %s", firstText)
	}
	for _, covered := range []string{"question 2", "answer 2", "question 3", "answer 3"} {
		if strings.Contains(firstText, covered) {
			t.Fatalf("first provider request retained covered text %q: %s", covered, firstText)
		}
	}
	if requests[0].StableMessageCount != 0 {
		t.Fatalf("first projected stable count = %d, want 0", requests[0].StableMessageCount)
	}
	if requests[1].StableMessageCount != 4 || requests[2].StableMessageCount != 4 {
		t.Fatalf("reused stable counts = (%d, %d), want (4, 4)", requests[1].StableMessageCount, requests[2].StableMessageCount)
	}
	if providerCacheKey(requests[1]) != providerCacheKey(requests[2]) {
		t.Fatal("same applied sidecar and canonical tail produced different semantic cache keys")
	}
	for index, request := range requests {
		for _, tool := range request.Tools {
			name, err := extractToolName(tool)
			if err != nil {
				t.Fatalf("request %d tool decode error = %v", index+1, err)
			}
			if name == seeImageToolName {
				t.Fatalf("request %d retained disabled %s tool", index+1, seeImageToolName)
			}
		}
		diagnostics, ok := request.RequestKnobs["context_projection"].(map[string]any)
		if !ok || diagnostics["mode"] != contextProjectionModeSummary {
			t.Fatalf("request %d projection diagnostics = %#v", index+1, request.RequestKnobs)
		}
		if err := validateProviderRequestContextBudget(persisted, CompiledConversation{Messages: request.Messages, Tools: request.Tools}, request.MaxTokens); err != nil {
			t.Fatalf("request %d violates final context budget: %v", index+1, err)
		}
	}
	applied, err := store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if applied == nil || !applied.Applied {
		t.Fatalf("projection state = %#v, want applied sidecar", applied)
	}
}

func TestDriveProviderIgnoresValidProjectionWhenCanonicalRequestIsBelowPressure(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionTestConversation(t, 8)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
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
		t.Fatal("provider request was not started")
	}
	waitForContextProjectionProviderIdle(t, stream)

	text := contextProjectionMessageText(request.Messages)
	if strings.Contains(text, "turns two and three summarized") {
		t.Fatalf("low-pressure provider request used projection summary: %s", text)
	}
	for _, canonical := range []string{"question 2", "answer 2", "question 3", "answer 3"} {
		if !strings.Contains(text, canonical) {
			t.Fatalf("low-pressure provider request omitted canonical text %q: %s", canonical, text)
		}
	}
	diagnostics, ok := request.RequestKnobs["context_projection"].(map[string]any)
	if !ok || diagnostics["mode"] != "full" || diagnostics["sidecar_hit"] != false {
		t.Fatalf("low-pressure diagnostics = %#v, want full mode without sidecar hit", request.RequestKnobs)
	}
}

func TestDriveProviderManualCompactionUsesCanonicalCompileWhenProjectionIsValid(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}

	compiler := contextProjectionLifecycleCompiler{}
	canonicalCompiled, err := compiler.Compile(
		persisted,
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
		"model-a",
		"",
	)
	if err != nil {
		t.Fatalf("Compile(canonical) error = %v", err)
	}
	projected, err := projectConversationWithContextProjection(persisted, state, "model-a")
	if err != nil {
		t.Fatalf("projectConversationWithContextProjection() error = %v", err)
	}
	projectedCompiled, err := compiler.Compile(
		projected,
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
		"model-a",
		"",
	)
	if err != nil {
		t.Fatalf("Compile(projected) error = %v", err)
	}
	if len(canonicalCompiled.Messages) == len(projectedCompiled.Messages) {
		t.Fatal("test setup did not produce distinct canonical and projected compiles")
	}

	provider := &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), compiler, provider, broker)
	stream, err := broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.ManualCompaction = manualCompactionDirective{Requested: true, Instruction: "preserve decisions"}

	if err := service.driveProvider(stream); err != nil {
		t.Fatalf("driveProvider() error = %v", err)
	}
	stream.mu.Lock()
	pending := stream.PendingCompaction
	providerActive := stream.ProviderActive
	stream.mu.Unlock()
	if pending == nil || pending.Trigger != "manual" {
		t.Fatalf("pending compaction = %#v, want manual plan", pending)
	}
	if got, want := pending.MessageCount, int32(len(canonicalCompiled.Messages)); got != want {
		t.Fatalf("manual message count = %d, want canonical count %d (projected=%d)", got, want, len(projectedCompiled.Messages))
	}
	if got, want := pending.ContextTokens, estimateCompiledPromptTokens(canonicalCompiled); got != want {
		t.Fatalf("manual context tokens = %d, want canonical estimate %d", got, want)
	}
	if providerActive {
		t.Fatal("manual compaction unexpectedly started the parent provider")
	}
	select {
	case request := <-provider.requests:
		t.Fatalf("manual compaction unexpectedly sent provider request: %#v", request)
	default:
	}
}

func TestDriveProviderDoesNotMarkProjectionAppliedWhenProviderFailsBeforeFirstEvent(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	provider := &contextProjectionRequestProvider{
		requests: make(chan ProviderRequest, 1),
		err:      errors.New("provider rejected request before streaming"),
	}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)

	if err := service.driveProvider(stream); err != nil {
		t.Fatalf("driveProvider() error = %v", err)
	}
	select {
	case <-provider.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("provider request was not started")
	}
	waitForContextProjectionProviderTerminal(t, stream)

	loaded, err := store.LoadContextProjection(persisted.ConversationID)
	if err != nil {
		t.Fatalf("LoadContextProjection() error = %v", err)
	}
	if loaded == nil || loaded.Applied {
		t.Fatalf("projection state = %#v, want unapplied sidecar after immediate provider failure", loaded)
	}
}

func TestContextProjectionRequestDiagnosticsDescribeTheFinalProjection(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 8)
	state, err := newContextProjectionState(
		conversation,
		"model-a",
		firstEntrySeqForTurn(conversation.Entries, 2),
		lastEntrySeqForTurn(conversation.Entries, 3),
		"rolling summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	before := CompiledConversation{
		Messages:           []modeladapter.Message{{Role: "system", Content: "system"}, {Role: "user", Content: strings.Repeat("before ", 80)}},
		StableMessageCount: 9,
	}
	after := CompiledConversation{
		Messages:           []modeladapter.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "summary"}, {Role: "user", Content: "tail"}},
		StableMessageCount: 0,
	}
	fields := contextProjectionRequestDiagnostics(
		"parent",
		conversation,
		state,
		true,
		"",
		before,
		after,
		160_000,
		10_000,
		8_000,
		2,
		0.64,
	)
	if got := fields["mode"]; got != contextProjectionModeSummary {
		t.Fatalf("mode = %#v, want %q", got, contextProjectionModeSummary)
	}
	if got := fields["sidecar_hit"]; got != true {
		t.Fatalf("sidecar_hit = %#v, want true", got)
	}
	if got := fields["covered_entry_seq"]; got != state.CoveredEntrySeq {
		t.Fatalf("covered_entry_seq = %#v, want %d", got, state.CoveredEntrySeq)
	}
	if got := fields["before_message_count"]; got != len(before.Messages) {
		t.Fatalf("before_message_count = %#v, want %d", got, len(before.Messages))
	}
	if got := fields["after_message_count"]; got != len(after.Messages) {
		t.Fatalf("after_message_count = %#v, want %d", got, len(after.Messages))
	}
	if got := fields["stable_count_before"]; got != before.StableMessageCount {
		t.Fatalf("stable_count_before = %#v, want %d", got, before.StableMessageCount)
	}
	if got := fields["stable_count_after"]; got != after.StableMessageCount {
		t.Fatalf("stable_count_after = %#v, want %d", got, after.StableMessageCount)
	}
	if got := fields["overflow_retry_ratio"]; got != 0.64 {
		t.Fatalf("overflow_retry_ratio = %#v, want 0.64", got)
	}
	if got := fields["dropped_turns"]; got != 2 {
		t.Fatalf("dropped_turns = %#v, want 2", got)
	}
}

func TestPrepareConversationContextProjectionReportsSidecarInvalidation(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 8)
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"rolling summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	state.ModelKey = "other-model"
	if err := service.store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	projected, activeState, active, reason := service.prepareConversationContextProjectionState(persisted, "model-a")
	if active || activeState != nil || projected != persisted {
		t.Fatalf("invalid sidecar should fall back to canonical: active=%t state=%#v projected=%p canonical=%p", active, activeState, projected, persisted)
	}
	if reason != "model_mismatch" {
		t.Fatalf("invalidation reason = %q, want model_mismatch", reason)
	}
}

func TestValidateContextProjectionFailsClosed(t *testing.T) {
	base := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
	})
	base.RootConversationID = "root-1"
	base.ParentConversationID = "parent-1"
	base.ParentToolCallID = "tool-1"
	appendEntriesInPlace(base, nil)
	projection, err := newContextProjectionState(base, "model-a", base.Entries[0].Seq, base.Entries[len(base.Entries)-1].Seq, "summary")
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*contextProjectionState, *ConversationFile)
		modelKey   string
		wantReason string
	}{
		{name: "schema", mutate: func(state *contextProjectionState, _ *ConversationFile) { state.SchemaVersion++ }, modelKey: "model-a", wantReason: "schema_version_mismatch"},
		{name: "conversation", mutate: func(state *contextProjectionState, _ *ConversationFile) { state.ConversationID = "other" }, modelKey: "model-a", wantReason: "conversation_mismatch"},
		{name: "root lineage", mutate: func(_ *contextProjectionState, conversation *ConversationFile) {
			conversation.RootConversationID = "other-root"
		}, modelKey: "model-a", wantReason: "lineage_mismatch"},
		{name: "parent lineage", mutate: func(_ *contextProjectionState, conversation *ConversationFile) {
			conversation.ParentConversationID = "other-parent"
		}, modelKey: "model-a", wantReason: "lineage_mismatch"},
		{name: "parent tool lineage", mutate: func(_ *contextProjectionState, conversation *ConversationFile) {
			conversation.ParentToolCallID = "other-tool"
		}, modelKey: "model-a", wantReason: "lineage_mismatch"},
		{name: "model", mutate: func(_ *contextProjectionState, _ *ConversationFile) {}, modelKey: "model-b", wantReason: "model_mismatch"},
		{name: "future context version", mutate: func(state *contextProjectionState, conversation *ConversationFile) {
			state.ContextVersion = conversation.ContextVersion + 1
		}, modelKey: "model-a", wantReason: "context_version_ahead"},
		{name: "covered boundary", mutate: func(state *contextProjectionState, _ *ConversationFile) { state.CoveredEntrySeq++ }, modelKey: "model-a", wantReason: "covered_prefix_missing"},
		{name: "fingerprint", mutate: func(state *contextProjectionState, _ *ConversationFile) {
			state.CoveredPrefixFingerprint = "sha256:wrong"
		}, modelKey: "model-a", wantReason: "covered_prefix_mismatch"},
		{name: "empty summary", mutate: func(state *contextProjectionState, _ *ConversationFile) { state.Summary = "  " }, modelKey: "model-a", wantReason: "summary_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := *projection
			conversation := cloneConversationFile(base)
			test.mutate(&state, conversation)
			valid, reason := validateContextProjectionState(&state, conversation, test.modelKey)
			if valid || reason != test.wantReason {
				t.Fatalf("validateContextProjectionState() = (%t, %q), want (false, %q)", valid, reason, test.wantReason)
			}
		})
	}
}

func TestValidateContextProjectionRejectsPartialStructuralBoundaries(t *testing.T) {
	conversation := testConversation(nil)
	toolCall := json.RawMessage(`{}`)
	appendEntriesInPlace(conversation, []HistoryEntry{
		testModelMessageEntry(t, 0, "", modeladapter.Message{Role: "user", Content: "imported question"}),
		testModelMessageEntry(t, 0, "", modeladapter.Message{Role: "assistant", Content: "imported answer"}),
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
		testUserMessageEntry(t, 2, "request-2", "tool question"),
		newToolCallEntry(2, "request-2", "call-2", "Read", "", "", toolCall),
		newToolResultEntry(2, "request-2", "call-2", "Read", `{}`, "tool result", "", toolCall),
		newAssistantTextEntry(2, "request-2", "tool answer", "", ""),
	})
	projection, err := newContextProjectionState(
		conversation,
		"model-a",
		conversation.Entries[0].Seq,
		conversation.Entries[len(conversation.Entries)-1].Seq,
		"summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*contextProjectionState)
		wantReason string
	}{
		{
			name: "imported start in the middle",
			mutate: func(state *contextProjectionState) {
				state.SummaryStartEntrySeq = conversation.Entries[1].Seq
			},
			wantReason: "summary_start_boundary_invalid",
		},
		{
			name: "imported covered in the middle",
			mutate: func(state *contextProjectionState) {
				state.CoveredEntrySeq = conversation.Entries[0].Seq
				state.CoveredPrefixFingerprint, _ = contextProjectionCoveredPrefixFingerprint(conversation.Entries, state.CoveredEntrySeq)
			},
			wantReason: "covered_boundary_invalid",
		},
		{
			name: "tool turn start in the middle",
			mutate: func(state *contextProjectionState) {
				state.SummaryStartEntrySeq = conversation.Entries[5].Seq
			},
			wantReason: "summary_start_boundary_invalid",
		},
		{
			name: "tool batch covered in the middle",
			mutate: func(state *contextProjectionState) {
				state.CoveredEntrySeq = conversation.Entries[5].Seq
				state.CoveredPrefixFingerprint, _ = contextProjectionCoveredPrefixFingerprint(conversation.Entries, state.CoveredEntrySeq)
			},
			wantReason: "covered_boundary_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := *projection
			test.mutate(&state)
			valid, reason := validateContextProjectionState(&state, conversation, "model-a")
			if valid || reason != test.wantReason {
				t.Fatalf("validateContextProjectionState() = (%t, %q), want (false, %q)", valid, reason, test.wantReason)
			}
		})
	}
}

func TestValidateContextProjectionRejectsCoveredPrefixMutation(t *testing.T) {
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "first question"),
		newAssistantTextEntry(1, "request-1", "first answer", "", ""),
	})
	appendEntriesInPlace(conversation, nil)
	projection, err := newContextProjectionState(conversation, "model-a", conversation.Entries[0].Seq, conversation.Entries[len(conversation.Entries)-1].Seq, "summary")
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	conversation.Entries[0].RequestID = "rewritten-request"
	conversation.UpdatedAt = time.Now().UTC()
	valid, reason := validateContextProjectionState(projection, conversation, "model-a")
	if valid || reason != "covered_prefix_mismatch" {
		t.Fatalf("validateContextProjectionState() = (%t, %q), want covered prefix mismatch", valid, reason)
	}
}

func TestBuildContextProjectionSummaryPlanSelectsMiddleTurns(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	canonicalBefore := append([]HistoryEntry(nil), conversation.Entries...)

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want middle-turn projection plan")
	}
	if plan.Trigger != contextProjectionTrigger {
		t.Fatalf("plan trigger = %q, want %q", plan.Trigger, contextProjectionTrigger)
	}
	if plan.ProjectionSummaryStartEntrySeq != firstEntrySeqForTurn(conversation.Entries, 2) {
		t.Fatalf("summary start seq = %d, want start of turn 2", plan.ProjectionSummaryStartEntrySeq)
	}
	if plan.ProjectionCoveredEntrySeq != lastEntrySeqForTurn(conversation.Entries, 3) {
		t.Fatalf("covered seq = %d, want end of turn 3", plan.ProjectionCoveredEntrySeq)
	}
	if plan.ProjectionModelKey != "model-a" || plan.ProjectionCoveredPrefixFingerprint == "" {
		t.Fatalf("projection metadata missing: %#v", plan)
	}
	if len(plan.CompactedTurns) != 2 {
		t.Fatalf("compacted turns = %d, want 2", len(plan.CompactedTurns))
	}
	if !reflect.DeepEqual(conversation.Entries, canonicalBefore) {
		t.Fatal("building projection plan modified canonical entries")
	}
}

func TestBuildForcedCompactionPlanUsesSidecarProjection(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 8)
	conversation.TokenDetailsMaxTokens = 160_000
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	stream := &ActiveStream{
		ConversationID: conversation.ConversationID,
		ModelID:        "model-a",
		ModelName:      "model-a",
		TurnSeq:        8,
		RequestID:      "request-8",
	}
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 500_000)}}}

	plan, err := service.buildForcedCompactionPlan(stream, conversation, compiled)
	if err != nil {
		t.Fatalf("buildForcedCompactionPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildForcedCompactionPlan() = nil, want sidecar projection plan")
	}
	if plan.Trigger != contextProjectionTrigger {
		t.Fatalf("plan trigger = %q, want %q", plan.Trigger, contextProjectionTrigger)
	}
	if plan.ProjectionCoveredEntrySeq <= 0 || plan.ProjectionCoveredPrefixFingerprint == "" {
		t.Fatalf("projection metadata missing: %#v", plan)
	}
}

func TestBuildForcedCompactionPlanProgressivelyTightensExistingProjectionTail(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 10)
	conversation.TokenDetailsMaxTokens = 160_000
	conversation.CurrentTurnSeq = 10
	conversation.CurrentRequestID = "request-10"
	existing, err := newContextProjectionState(
		conversation,
		"model-a",
		firstEntrySeqForTurn(conversation.Entries, 2),
		lastEntrySeqForTurn(conversation.Entries, 5),
		"turns two through five summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := service.store.SaveContextProjection(conversation.ConversationID, existing); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	stream := &ActiveStream{
		ConversationID: conversation.ConversationID,
		ModelID:        "model-a",
		ModelName:      "model-a",
		TurnSeq:        10,
		RequestID:      "request-10",
	}
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 500_000)}}}

	first, err := service.buildForcedCompactionPlan(stream, conversation, compiled)
	if err != nil {
		t.Fatalf("first buildForcedCompactionPlan() error = %v", err)
	}
	if first == nil {
		t.Fatal("first buildForcedCompactionPlan() = nil, want a tighter recent tail")
	}
	if first.ProjectionCoveredEntrySeq != lastEntrySeqForTurn(conversation.Entries, 6) {
		t.Fatalf("first covered seq = %d, want end of turn 6", first.ProjectionCoveredEntrySeq)
	}
	if len(first.CompactedTurns) != 1 {
		t.Fatalf("first compacted turns = %d, want turn 6 only", len(first.CompactedTurns))
	}

	updated, err := newContextProjectionState(
		conversation,
		"model-a",
		existing.SummaryStartEntrySeq,
		first.ProjectionCoveredEntrySeq,
		"turns two through six summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState(updated) error = %v", err)
	}
	if err := service.store.SaveContextProjection(conversation.ConversationID, updated); err != nil {
		t.Fatalf("SaveContextProjection(updated) error = %v", err)
	}
	stream.ContextOverflowCompactionAttempts = 1
	second, err := service.buildForcedCompactionPlan(stream, conversation, compiled)
	if err != nil {
		t.Fatalf("second buildForcedCompactionPlan() error = %v", err)
	}
	if second == nil {
		t.Fatal("second buildForcedCompactionPlan() = nil, want another tighter recent tail")
	}
	if second.ProjectionCoveredEntrySeq != lastEntrySeqForTurn(conversation.Entries, 7) {
		t.Fatalf("second covered seq = %d, want end of turn 7", second.ProjectionCoveredEntrySeq)
	}
	if len(second.CompactedTurns) != 1 {
		t.Fatalf("second compacted turns = %d, want turn 7 only", len(second.CompactedTurns))
	}
}

func TestBuildForcedCompactionPlanTightensAfterProviderOverflowBelowLocalThreshold(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 10)
	conversation.TokenDetailsMaxTokens = 160_000
	conversation.CurrentTurnSeq = 10
	conversation.CurrentRequestID = "request-10"
	existing, err := newContextProjectionState(
		conversation,
		"model-a",
		firstEntrySeqForTurn(conversation.Entries, 2),
		lastEntrySeqForTurn(conversation.Entries, 5),
		"turns two through five summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := service.store.SaveContextProjection(conversation.ConversationID, existing); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}
	stream := &ActiveStream{
		ConversationID: conversation.ConversationID,
		ModelID:        "model-a",
		ModelName:      "model-a",
		TurnSeq:        10,
		RequestID:      "request-10",
	}
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 3_000)}}}

	plan, err := service.buildForcedCompactionPlan(stream, conversation, compiled)
	if err != nil {
		t.Fatalf("buildForcedCompactionPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildForcedCompactionPlan() = nil, want provider-overflow recovery despite the low local estimate")
	}
	if plan.ProjectionCoveredEntrySeq != lastEntrySeqForTurn(conversation.Entries, 6) {
		t.Fatalf("covered seq = %d, want end of turn 6", plan.ProjectionCoveredEntrySeq)
	}
}

func TestBuildAutoCompactionPlanDoesNotRewriteCanonicalToolResults(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 8)
	conversation.TokenDetailsMaxTokens = 160_000
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	for index := range conversation.Entries {
		if conversation.Entries[index].TurnSeq == 2 && conversation.Entries[index].Kind == "assistant_text" {
			conversation.Entries[index] = newToolResultEntry(2, "request-2", "call-2", "Read", `{}`, strings.Repeat("result ", 4_000), "", nil)
			break
		}
	}
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	statePath := filepath.Join(service.store.conversationDir(persisted.ConversationID), conversationStateFileName)
	contextPath := filepath.Join(service.store.conversationDir(persisted.ConversationID), conversationContextFileName)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state before auto projection: %v", err)
	}
	contextBefore, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read context before auto projection: %v", err)
	}
	stream := &ActiveStream{
		ConversationID: persisted.ConversationID,
		ModelID:        "model-a",
		ModelName:      "model-a",
		TurnSeq:        8,
		RequestID:      "request-8",
	}
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 500_000)}}}

	_, _ = service.buildAutoCompactionPlan(stream, persisted, compiled)

	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after auto projection: %v", err)
	}
	contextAfter, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read context after auto projection: %v", err)
	}
	if !reflect.DeepEqual(stateAfter, stateBefore) {
		t.Fatal("automatic context maintenance modified state.json")
	}
	if !reflect.DeepEqual(contextAfter, contextBefore) {
		t.Fatal("automatic context maintenance modified context.json")
	}
}

func TestBuildContextProjectionSummaryPlanSummarizesInterruptedHistoricalToolChain(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	toolCall := []byte(`{}`)
	entries := make([]HistoryEntry, 0, len(conversation.Entries)+1)
	for _, entry := range conversation.Entries {
		entries = append(entries, entry)
		if entry.TurnSeq == 2 && entry.Kind == "user_message" {
			entries = append(entries, newToolCallEntry(2, "request-2", "call-2", "Read", "", "", toolCall))
		}
	}
	conversation.Entries = entries

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want projection plan")
	}
	if !strings.Contains(strings.Join(plan.CompactedTurns[0].Steps, "\n"), "interrupted") {
		t.Fatalf("projection summary = %#v, want interrupted tool-chain note", plan.CompactedTurns)
	}
}

func TestBuildContextProjectionSummaryPlanIncludesAllProviderVisibleTurnEntries(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	payload, err := json.Marshal(modelMessageEntryPayload{
		Message: modeladapter.Message{Role: "user", Content: "provider-visible imported context"},
	})
	if err != nil {
		t.Fatalf("encode model_message payload: %v", err)
	}
	entries := make([]HistoryEntry, 0, len(conversation.Entries)+1)
	for _, entry := range conversation.Entries {
		entries = append(entries, entry)
		if entry.TurnSeq == 2 && entry.Kind == "user_message" {
			entries = append(entries, HistoryEntry{
				Seq:       entry.Seq,
				TurnSeq:   2,
				RequestID: "request-2",
				Role:      "user",
				Kind:      "model_message",
				Payload:   payload,
			})
		}
	}
	for index := range entries {
		entries[index].Seq = int64(index + 1)
	}
	conversation.Entries = entries

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want projection plan")
	}
	messages, err := NewService(t.TempDir(), nilResolver{}).buildCompactionSummaryMessages(newPendingCompaction(plan))
	if err != nil {
		t.Fatalf("buildCompactionSummaryMessages() error = %v", err)
	}
	if text := contextProjectionMessageText(messages); !strings.Contains(text, "provider-visible imported context") {
		t.Fatalf("projection summary input omitted model_message content: %s", text)
	}
}

func TestBuildContextProjectionSummaryPlanCompressesImportedPrehistoryAsOneStructuralGroup(t *testing.T) {
	conversation := testConversation(nil)
	importedEntries := []HistoryEntry{
		testModelMessageEntry(t, 0, "", modeladapter.Message{Role: "user", Content: "imported root question " + strings.Repeat("context ", 8_000)}),
		testModelMessageEntry(t, 0, "", modeladapter.Message{
			Role:                     "assistant",
			ReasoningContent:         "imported reasoning marker",
			ReasoningSignature:       "anthropic-signature",
			ReasoningSignatureSource: modeladapter.ReasoningSignatureSourceAnthropic,
			ToolCalls: []modeladapter.ToolCallDescriptor{
				{ID: "import-call-1", Type: "function", Function: modeladapter.ToolCallFunctionShape{Name: "Read", Arguments: `{"path":"a.go"}`}},
				{ID: "import-call-2", Type: "function", Function: modeladapter.ToolCallFunctionShape{Name: "Grep", Arguments: `{"pattern":"marker"}`}},
			},
		}),
		testModelMessageEntry(t, 0, "", modeladapter.Message{Role: "tool", ToolCallID: "import-call-1", Name: "Read", Content: "read result marker"}),
		testModelMessageEntry(t, 0, "", modeladapter.Message{Role: "tool", ToolCallID: "import-call-2", Name: "Grep", Content: "grep result marker"}),
	}
	entries := append([]HistoryEntry(nil), importedEntries...)
	for turn := int64(1); turn <= 6; turn++ {
		requestID := fmt.Sprintf("request-%d", turn)
		entries = append(entries,
			testUserMessageEntry(t, turn, requestID, fmt.Sprintf("question %d", turn)),
			newAssistantTextEntry(turn, requestID, fmt.Sprintf("answer %d", turn), "", ""),
		)
	}
	appendEntriesInPlace(conversation, entries)
	conversation.CurrentTurnSeq = 6
	conversation.CurrentRequestID = "request-6"

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 130_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want imported prehistory projection plan")
	}
	if plan.ProjectionSummaryStartEntrySeq != conversation.Entries[0].Seq {
		t.Fatalf("summary start seq = %d, want first imported entry %d", plan.ProjectionSummaryStartEntrySeq, conversation.Entries[0].Seq)
	}
	if plan.ProjectionCoveredEntrySeq != conversation.Entries[len(importedEntries)-1].Seq {
		t.Fatalf("covered seq = %d, want last imported entry %d", plan.ProjectionCoveredEntrySeq, conversation.Entries[len(importedEntries)-1].Seq)
	}
	if len(plan.CompactedTurns) != 1 {
		t.Fatalf("compacted groups = %d, want imported prehistory as one group", len(plan.CompactedTurns))
	}
	messages, err := NewService(t.TempDir(), nilResolver{}).buildCompactionSummaryMessages(newPendingCompaction(plan))
	if err != nil {
		t.Fatalf("buildCompactionSummaryMessages() error = %v", err)
	}
	text := contextProjectionMessageText(messages)
	for _, marker := range []string{"imported root question", "imported reasoning marker", "Read=called", "Grep=called", "read result marker", "grep result marker"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("imported projection summary input omitted %q: %s", marker, text)
		}
	}
}

func TestBuildContextProjectionSummaryPlanRejectsIncompleteImportedToolBatch(t *testing.T) {
	conversation := testConversation(nil)
	entries := []HistoryEntry{
		testModelMessageEntry(t, 0, "", modeladapter.Message{Role: "user", Content: "imported question"}),
		testModelMessageEntry(t, 0, "", modeladapter.Message{
			Role:      "assistant",
			ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "missing-result", Type: "function", Function: modeladapter.ToolCallFunctionShape{Name: "Read"}}},
		}),
	}
	for turn := int64(1); turn <= 6; turn++ {
		requestID := fmt.Sprintf("request-%d", turn)
		entries = append(entries,
			testUserMessageEntry(t, turn, requestID, fmt.Sprintf("question %d", turn)),
			newAssistantTextEntry(turn, requestID, fmt.Sprintf("answer %d", turn), "", ""),
		)
	}
	appendEntriesInPlace(conversation, entries)

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 130_000, 160_000, 10_000)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("buildContextProjectionSummaryPlan() = (%#v, %v), want incomplete imported tool batch error", plan, err)
	}
}

func TestContextProjectionMergesCanonicalSummaryIntoSingleProviderSummary(t *testing.T) {
	conversation := testConversation(nil)
	canonicalPayload, err := json.Marshal(compactionSummaryEntryPayload{Summary: "canonical manual summary", Trigger: "manual"})
	if err != nil {
		t.Fatalf("encode canonical summary: %v", err)
	}
	entries := []HistoryEntry{{TurnSeq: 0, Role: "system", Kind: "compacted_summary", Payload: canonicalPayload}}
	for turn := int64(1); turn <= 8; turn++ {
		requestID := fmt.Sprintf("request-%d", turn)
		entries = append(entries,
			testUserMessageEntry(t, turn, requestID, fmt.Sprintf("question %d", turn)),
			newAssistantTextEntry(turn, requestID, fmt.Sprintf("answer %d", turn), "", ""),
		)
	}
	appendEntriesInPlace(conversation, entries)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	canonicalBefore := append([]HistoryEntry(nil), conversation.Entries...)

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 120_000, 160_000, 10_000)
	if err != nil || plan == nil {
		t.Fatalf("buildContextProjectionSummaryPlan() = (%#v, %v)", plan, err)
	}
	if plan.ExistingSummary != "canonical manual summary" {
		t.Fatalf("existing summary = %q, want canonical manual summary", plan.ExistingSummary)
	}
	state, err := newContextProjectionState(
		conversation,
		"model-a",
		plan.ProjectionSummaryStartEntrySeq,
		plan.ProjectionCoveredEntrySeq,
		"merged rolling summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	projected, err := projectConversationWithContextProjection(conversation, state, "model-a")
	if err != nil {
		t.Fatalf("projectConversationWithContextProjection() error = %v", err)
	}
	messages, err := NewHistoryProjector().ProjectPromptReplay(projected)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() error = %v", err)
	}
	text := contextProjectionMessageText(messages)
	if count := strings.Count(text, "<conversation_summary>"); count != 1 {
		t.Fatalf("provider summary count = %d, want 1: %s", count, text)
	}
	if !strings.Contains(text, "merged rolling summary") || strings.Contains(text, "canonical manual summary") {
		t.Fatalf("provider did not replace canonical summary with merged projection: %s", text)
	}
	if summaryIndex, turnIndex := strings.Index(text, "merged rolling summary"), strings.Index(text, "question 4"); summaryIndex < 0 || turnIndex < 0 || summaryIndex > turnIndex {
		t.Fatalf("merged summary moved after stable post-summary history: %s", text)
	}
	if !reflect.DeepEqual(conversation.Entries, canonicalBefore) {
		t.Fatal("projection summary merge modified canonical entries")
	}
}

func TestBuildContextProjectionSummaryPlanFailsClosedForMalformedProviderVisibleEntry(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	entries := make([]HistoryEntry, 0, len(conversation.Entries)+1)
	for _, entry := range conversation.Entries {
		entries = append(entries, entry)
		if entry.TurnSeq == 2 && entry.Kind == "user_message" {
			entries = append(entries, HistoryEntry{
				Seq:       entry.Seq,
				TurnSeq:   2,
				RequestID: "request-2",
				Role:      "user",
				Kind:      "model_message",
				Payload:   json.RawMessage(`{not-json`),
			})
		}
	}
	for index := range entries {
		entries[index].Seq = int64(index + 1)
	}
	conversation.Entries = entries

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", nil, 120_000, 160_000, 10_000)
	if err == nil || !strings.Contains(err.Error(), "model_message") {
		t.Fatalf("buildContextProjectionSummaryPlan() = (%#v, %v), want malformed model_message error", plan, err)
	}
}

func TestBuildContextProjectionSummaryPlanExtendsExistingSummaryAcrossCompleteTurns(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 10)
	conversation.CurrentTurnSeq = 10
	conversation.CurrentRequestID = "request-10"
	existing, err := newContextProjectionState(
		conversation,
		"model-a",
		firstEntrySeqForTurn(conversation.Entries, 2),
		lastEntrySeqForTurn(conversation.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", existing, 120_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want extension plan")
	}
	if plan.ProjectionSummaryStartEntrySeq != existing.SummaryStartEntrySeq {
		t.Fatalf("summary start seq = %d, want existing boundary %d", plan.ProjectionSummaryStartEntrySeq, existing.SummaryStartEntrySeq)
	}
	if plan.ProjectionCoveredEntrySeq != lastEntrySeqForTurn(conversation.Entries, 5) {
		t.Fatalf("covered seq = %d, want end of turn 5", plan.ProjectionCoveredEntrySeq)
	}
	if plan.ExistingSummary != existing.Summary {
		t.Fatalf("existing summary = %q, want %q", plan.ExistingSummary, existing.Summary)
	}
	if len(plan.CompactedTurns) != 2 {
		t.Fatalf("new compacted turns = %d, want turns 4 and 5", len(plan.CompactedTurns))
	}
}

func TestBuildAutoCompactionPlanSkipsValidRecentTailFallback(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := contextProjectionHighPressureConversation(t)
	conversation.TokenDetailsMaxTokens = projectedConversationMaxTokens
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	persisted, err := store.SaveConversationWithEntries(
		conversation.ConversationID,
		conversation,
		conversation.Entries,
	)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	fallback, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"temporary summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	fallback.Mode = contextProjectionModeRecentTail
	fallback.Summary = ""
	if err := store.SaveContextProjection(persisted.ConversationID, fallback); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}

	compiler := contextProjectionLifecycleCompiler{}
	compiled, err := compiler.Compile(
		persisted,
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
		"model-a",
		"",
	)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	service := newServiceWithDependencies(store, NewHistoryProjector(), compiler, nil, NewStreamBroker())
	stream, err := service.broker.OpenStream(
		"request-8",
		persisted.ConversationID,
		8,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"question 8",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	plan, err := service.buildAutoCompactionPlan(stream, persisted, compiled)
	if err != nil {
		t.Fatalf("buildAutoCompactionPlan() error = %v", err)
	}
	if plan != nil {
		t.Fatalf("buildAutoCompactionPlan() = %#v, want nil for valid recent-tail fallback", plan)
	}
}

func TestBuildContextProjectionSummaryPlanRebuildsRecentTailCoveredTurns(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 10)
	conversation.CurrentTurnSeq = 10
	conversation.CurrentRequestID = "request-10"
	existing, err := newContextProjectionState(
		conversation,
		"model-a",
		firstEntrySeqForTurn(conversation.Entries, 2),
		lastEntrySeqForTurn(conversation.Entries, 3),
		"temporary summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	existing.Mode = contextProjectionModeRecentTail
	existing.Summary = ""

	plan, err := buildContextProjectionSummaryPlan(conversation, "model-a", existing, 120_000, 160_000, 10_000)
	if err != nil {
		t.Fatalf("buildContextProjectionSummaryPlan() error = %v", err)
	}
	if plan == nil {
		t.Fatal("buildContextProjectionSummaryPlan() = nil, want replacement summary plan")
	}
	if plan.ProjectionSummaryStartEntrySeq != existing.SummaryStartEntrySeq {
		t.Fatalf("summary start seq = %d, want recent-tail boundary %d", plan.ProjectionSummaryStartEntrySeq, existing.SummaryStartEntrySeq)
	}
	if plan.ProjectionCoveredEntrySeq != lastEntrySeqForTurn(conversation.Entries, 5) {
		t.Fatalf("covered seq = %d, want end of newly eligible turn 5", plan.ProjectionCoveredEntrySeq)
	}
	if plan.ExistingSummary != "" {
		t.Fatalf("existing summary = %q, want empty recent-tail fallback", plan.ExistingSummary)
	}
	if len(plan.CompactedTurns) != 4 {
		t.Fatalf("compacted turns = %d, want rebuilt turns 2 through 5", len(plan.CompactedTurns))
	}
	messages, err := NewService(t.TempDir(), nilResolver{}).buildCompactionSummaryMessages(newPendingCompaction(plan))
	if err != nil {
		t.Fatalf("buildCompactionSummaryMessages() error = %v", err)
	}
	text := contextProjectionMessageText(messages)
	for _, required := range []string{"question 2", "answer 2", "question 3", "answer 3", "question 4", "answer 4", "question 5", "answer 5"} {
		if !strings.Contains(text, required) {
			t.Fatalf("replacement summary input omitted %q: %s", required, text)
		}
	}
}

func TestValidateContextProjectionTurnToolChainAllowsEmbeddedLegacyCall(t *testing.T) {
	toolCall := []byte(`{}`)
	result := newToolResultEntry(2, "request-2", "call-2", "Read", `{}`, "done", "", toolCall)
	if reason := validateContextProjectionTurnToolChain([]HistoryEntry{
		testUserMessageEntry(t, 2, "request-2", "question"),
		result,
	}); reason != "" {
		t.Fatalf("validateContextProjectionTurnToolChain() reason = %q, want valid legacy embedded call", reason)
	}
}

func TestValidateContextProjectionTurnToolChainRejectsMixedRequestIDs(t *testing.T) {
	entries := []HistoryEntry{
		testUserMessageEntry(t, 2, "request-2a", "question"),
		newAssistantTextEntry(2, "request-2b", "answer", "", ""),
	}
	if reason := validateContextProjectionTurnToolChain(entries); !strings.Contains(reason, "mixed request ids") {
		t.Fatalf("validateContextProjectionTurnToolChain() reason = %q, want mixed request ids", reason)
	}
}

func TestProjectConversationWithContextProjectionKeepsPrefixSummaryAndTail(t *testing.T) {
	conversation := contextProjectionTestConversation(t, 8)
	conversation.CurrentTurnSeq = 8
	conversation.CurrentRequestID = "request-8"
	conversation.LatestRequestPrefix = &ConversationRequestPrefix{RequestID: "request-8", ReplayMessageCount: 99}
	canonicalBefore := append([]HistoryEntry(nil), conversation.Entries...)
	state, err := newContextProjectionState(
		conversation,
		"model-a",
		firstEntrySeqForTurn(conversation.Entries, 2),
		lastEntrySeqForTurn(conversation.Entries, 3),
		"turns two and three summarized",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}

	projected, err := projectConversationWithContextProjection(conversation, state, "model-a")
	if err != nil {
		t.Fatalf("projectConversationWithContextProjection() error = %v", err)
	}
	if projected == conversation {
		t.Fatal("projection returned canonical conversation pointer")
	}
	if projected.LatestRequestPrefix != nil {
		t.Fatal("projection retained stale request prefix metadata")
	}
	messages, err := NewHistoryProjector().ProjectPromptReplay(projected)
	if err != nil {
		t.Fatalf("ProjectPromptReplay(projected) error = %v", err)
	}
	joined := contextProjectionMessageText(messages)
	for _, required := range []string{"question 1", "answer 1", "turns two and three summarized", "question 4", "answer 4", "question 8", "answer 8"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("projected replay missing %q: %s", required, joined)
		}
	}
	for _, covered := range []string{"question 2", "answer 2", "question 3", "answer 3"} {
		if strings.Contains(joined, covered) {
			t.Fatalf("projected replay retained covered text %q: %s", covered, joined)
		}
	}
	if strings.Count(joined, "<conversation_summary>") != 1 {
		t.Fatalf("summary count = %d, want 1: %s", strings.Count(joined, "<conversation_summary>"), joined)
	}
	if !reflect.DeepEqual(conversation.Entries, canonicalBefore) {
		t.Fatal("projecting conversation modified canonical entries")
	}
}

func TestPrepareConversationContextProjectionUsesOnlyValidSidecar(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	conversation := contextProjectionTestConversation(t, 8)
	persisted, err := service.store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	state, err := newContextProjectionState(
		persisted,
		"model-a",
		firstEntrySeqForTurn(persisted.Entries, 2),
		lastEntrySeqForTurn(persisted.Entries, 3),
		"valid rolling summary",
	)
	if err != nil {
		t.Fatalf("newContextProjectionState() error = %v", err)
	}
	if err := service.store.SaveContextProjection(persisted.ConversationID, state); err != nil {
		t.Fatalf("SaveContextProjection() error = %v", err)
	}

	projected, active := service.prepareConversationContextProjection(persisted, "model-a")
	if !active || projected == persisted {
		t.Fatal("valid projection was not activated")
	}
	messages, err := NewHistoryProjector().ProjectPromptReplay(projected)
	if err != nil {
		t.Fatalf("ProjectPromptReplay() error = %v", err)
	}
	if text := contextProjectionMessageText(messages); !strings.Contains(text, "valid rolling summary") {
		t.Fatalf("projected prompt missing summary: %s", text)
	}

	invalid := *state
	invalid.ModelKey = "model-b"
	if err := service.store.SaveContextProjection(persisted.ConversationID, &invalid); err != nil {
		t.Fatalf("SaveContextProjection(invalid) error = %v", err)
	}
	canonical, active := service.prepareConversationContextProjection(persisted, "model-a")
	if active || canonical != persisted {
		t.Fatal("invalid projection was activated")
	}
}

func contextProjectionTestConversation(t *testing.T, turns int64) *ConversationFile {
	t.Helper()
	conversation := testConversation(nil)
	entries := make([]HistoryEntry, 0, turns*2)
	for turn := int64(1); turn <= turns; turn++ {
		requestID := "request-" + fmt.Sprint(turn)
		entries = append(entries,
			testUserMessageEntry(t, turn, requestID, "question "+fmt.Sprint(turn)),
			newAssistantTextEntry(turn, requestID, "answer "+fmt.Sprint(turn), "", ""),
		)
	}
	appendEntriesInPlace(conversation, entries)
	return conversation
}

func contextProjectionHighPressureConversation(t *testing.T) *ConversationFile {
	t.Helper()
	conversation := testConversation(nil)
	entries := make([]HistoryEntry, 0, 16)
	for turn := int64(1); turn <= 8; turn++ {
		requestID := "request-" + fmt.Sprint(turn)
		question := "question " + fmt.Sprint(turn)
		answer := "answer " + fmt.Sprint(turn)
		if turn == 2 || turn == 3 {
			question = strings.Repeat(fmt.Sprintf("covered question %d ", turn), 4_000)
			answer = strings.Repeat(fmt.Sprintf("covered answer %d ", turn), 4_000)
		}
		entries = append(entries,
			testUserMessageEntry(t, turn, requestID, question),
			newAssistantTextEntry(turn, requestID, answer, "", ""),
		)
	}
	appendEntriesInPlace(conversation, entries)
	return conversation
}

func firstEntrySeqForTurn(entries []HistoryEntry, turnSeq int64) int64 {
	for _, entry := range entries {
		if entry.TurnSeq == turnSeq {
			return entry.Seq
		}
	}
	return 0
}

func lastEntrySeqForTurn(entries []HistoryEntry, turnSeq int64) int64 {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].TurnSeq == turnSeq {
			return entries[index].Seq
		}
	}
	return 0
}

func contextProjectionMessageText(messages []modeladapter.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func waitForContextProjectionProviderIdle(t *testing.T, stream *ActiveStream) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		active := stream.ProviderActive
		stream.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("provider pass did not become idle")
}

func waitForContextProjectionProviderTerminal(t *testing.T, stream *ActiveStream) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		active := stream.ProviderActive
		status := stream.Status
		phase := stream.Phase
		done := stream.ActorDone
		stream.mu.Unlock()
		if !active && status == StreamStatusFailed && phase == TurnPhaseFailed && done != nil {
			select {
			case <-done:
				return
			default:
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("provider failure did not fully stop the stream actor")
}
