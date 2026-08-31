package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

type fakeDelegatedCompiler struct{}

func (fakeDelegatedCompiler) Compile(_ *ConversationFile, _ agentv1.AgentMode, _ string, _ string, _ string) (CompiledConversation, error) {
	return CompiledConversation{
		Messages: []modeladapter.Message{{Role: "system", Content: "sys"}},
		Tools:    []json.RawMessage{},
	}, nil
}

func (fakeDelegatedCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

// fakeDelegatedProvider 前 errorsBeforeSuccess 次调用返回超限错误，之后成功。
type fakeDelegatedProvider struct {
	errorsBeforeSuccess int
	callCount           int
	requests            []ProviderRequest
}

func (f *fakeDelegatedProvider) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	// 摘要请求不消耗 errorsBeforeSuccess 计数也不记录到 requests，
	// 避免 LLM 摘要调用干扰溢出重试测试的 callCount 和 requests 断言。
	if strings.Contains(req.CompileSummary, "compaction summary") {
		return errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model")
	}
	f.callCount++
	f.requests = append(f.requests, req)
	if f.callCount <= f.errorsBeforeSuccess {
		return errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model")
	}
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished}); err != nil {
		return err
	}
	return nil
}

// overflowlessProvider 返回非超限错误，用于验证「不触发压缩重试」。
type overflowlessProvider struct{}

func (overflowlessProvider) StartStream(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error {
	return errors.New("boom: request_timeout")
}

type overflowThenToolProvider struct {
	callCount int
	requests  []ProviderRequest
}

func (provider *overflowThenToolProvider) StartStream(_ context.Context, request ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	provider.callCount++
	provider.requests = append(provider.requests, request)
	switch provider.callCount {
	case 1:
		return errors.New("context_length_exceeded")
	case 2:
		if err := sink(modeladapter.ModelEvent{
			Kind: modeladapter.ModelEventKindToolLikeCompleted,
			ToolInvocation: &runtimecore.ToolInvocation{
				CallID:   "call-1",
				ToolName: "Read",
				ArgsJSON: []byte(`{"path":"README.md"}`),
			},
		}); err != nil {
			return err
		}
		return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished, FinishReason: "tool_calls"})
	default:
		if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "done"}); err != nil {
			return err
		}
		return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
	}
}

type trueWindowDelegatedProvider struct {
	maxInputTokens int64
	requests       []ProviderRequest
}

func (provider *trueWindowDelegatedProvider) StartStream(_ context.Context, request ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	provider.requests = append(provider.requests, request)
	inputTokens := estimateCompiledPromptTokens(CompiledConversation{Messages: request.Messages, Tools: request.Tools})
	if inputTokens > provider.maxInputTokens {
		return errors.New("context_length_exceeded: provider true window is 64k")
	}
	return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
}

func newCompactionTestAdapter(provider ProviderGateway) *localDelegatedAgentAdapter {
	return &localDelegatedAgentAdapter{
		compiler: fakeDelegatedCompiler{},
		provider: provider,
		toolExecutor: func(context.Context, delegation.TaskRequest, runtimecore.ToolInvocation) (string, error) {
			return "ok", nil
		},
		maxPasses:            10,
		resolveContextWindow: func(string) uint32 { return 272_000 },
	}
}

func TestExecuteRecoversFromContextOverflow(t *testing.T) {
	provider := &fakeDelegatedProvider{errorsBeforeSuccess: 1}
	adapter := newCompactionTestAdapter(provider)
	adapter.compiler = delegatedOverflowCompiler{}
	req := delegation.TaskRequest{ID: "t1", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("expected recovery, got error: %v", result.Error)
	}
	if provider.callCount != 2 {
		t.Fatalf("callCount = %d, want 2 (one fail + one retry)", provider.callCount)
	}
}

func TestExecuteOverflowRetryLimit(t *testing.T) {
	provider := &fakeDelegatedProvider{errorsBeforeSuccess: 5}
	adapter := newCompactionTestAdapter(provider)
	adapter.compiler = delegatedOverflowCompiler{}
	req := delegation.TaskRequest{ID: "t2", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error == nil {
		t.Fatal("expected failure after retry limit")
	}
	// 首次调用 + 2 次重试 = 3 次；超过则失败
	if provider.callCount != 1+delegatedCompactionRetryLimit {
		t.Fatalf("callCount = %d, want %d", provider.callCount, 1+delegatedCompactionRetryLimit)
	}
}

func TestExecuteOverflowRetriesShrinkTheWorkerWindow(t *testing.T) {
	provider := &fakeDelegatedProvider{errorsBeforeSuccess: 2}
	adapter := newCompactionTestAdapter(provider)
	adapter.compiler = delegatedOverflowCompiler{}
	req := delegation.TaskRequest{ID: "shrinking", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("expected recovery, got error: %v", result.Error)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(provider.requests))
	}
	previous := estimateModelMessagesTokens(provider.requests[0].Messages)
	for index := 1; index < len(provider.requests); index++ {
		current := estimateModelMessagesTokens(provider.requests[index].Messages)
		if current >= previous {
			t.Fatalf("retry %d sent %d tokens after %d; each overflow retry must shrink the window", index, current, previous)
		}
		previous = current
	}
	for index, providerRequest := range provider.requests {
		diagnostics, ok := providerRequest.RequestKnobs["context_projection"].(map[string]any)
		if !ok {
			t.Fatalf("request %d has no context projection diagnostics: %#v", index, providerRequest.RequestKnobs)
		}
		if got := diagnostics["mode"]; got != "window" {
			t.Fatalf("request %d diagnostics mode = %#v, want window", index, got)
		}
		if got := diagnostics["overflow_retry_ordinal"]; got != index {
			t.Fatalf("request %d retry ordinal = %#v, want %d", index, got, index)
		}
		if index > 0 {
			dropped, ok := diagnostics["dropped_groups"].(int)
			if !ok || dropped <= 0 {
				t.Fatalf("request %d diagnostics dropped_groups = %#v, want a positive count", index, diagnostics["dropped_groups"])
			}
		}
	}
}

func TestExecuteOverflowRetryShrinksAgainstPreviousSentInputWhenConfiguredWindowIsTooLarge(t *testing.T) {
	provider := &trueWindowDelegatedProvider{maxInputTokens: 64_000}
	adapter := newCompactionTestAdapter(provider)
	adapter.compiler = delegatedMisconfiguredWindowCompiler{}
	req := delegation.TaskRequest{ID: "true-window", Prompt: "current task", ModelID: "m1", ModelName: "gpt-5.6-luna"}

	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("expected recovery against the provider's true 64k window, got error: %v", result.Error)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("provider requests = %d, want an overflow followed by a smaller retry", len(provider.requests))
	}
	firstTokens := estimateCompiledPromptTokens(CompiledConversation{Messages: provider.requests[0].Messages, Tools: provider.requests[0].Tools})
	if firstTokens < 90_000 || firstTokens > 110_000 {
		t.Fatalf("first configured-window request tokens = %d, want approximately 100k", firstTokens)
	}
	previousTokens := firstTokens
	for index := 1; index < len(provider.requests); index++ {
		currentTokens := estimateCompiledPromptTokens(CompiledConversation{Messages: provider.requests[index].Messages, Tools: provider.requests[index].Tools})
		if currentTokens >= previousTokens {
			t.Fatalf("retry %d tokens = %d after %d, want a strict decrease", index, currentTokens, previousTokens)
		}
		previousTokens = currentTokens
	}
	if previousTokens > provider.maxInputTokens {
		t.Fatalf("final retry tokens = %d, still above provider true window %d", previousTokens, provider.maxInputTokens)
	}
}

func TestExecuteResetsOverflowWindowAfterRecoveredToolPass(t *testing.T) {
	provider := &overflowThenToolProvider{}
	adapter := newCompactionTestAdapter(provider)
	adapter.compiler = delegatedMisconfiguredWindowCompiler{}
	req := delegation.TaskRequest{ID: "recover-tool", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}

	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("expected tool follow-up after overflow recovery, got error: %v", result.Error)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if provider.callCount != 3 {
		t.Fatalf("provider calls = %d, want overflow + recovered tool pass + follow-up", provider.callCount)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("captured requests = %d, want 3", len(provider.requests))
	}
	const oldContextMarker = "old-canonical-context"
	firstText := contextProjectionMessageText(provider.requests[0].Messages)
	retryText := contextProjectionMessageText(provider.requests[1].Messages)
	followUpText := contextProjectionMessageText(provider.requests[2].Messages)
	if !strings.Contains(firstText, oldContextMarker) {
		t.Fatalf("initial request omitted canonical marker: %s", firstText)
	}
	if strings.Contains(retryText, oldContextMarker) {
		t.Fatal("overflow retry did not slide out the old optional context")
	}
	if !strings.Contains(followUpText, oldContextMarker) {
		t.Fatal("tool follow-up was not rebuilt from complete worker history")
	}
	for index, wantOrdinal := range []int{0, 1, 0} {
		diagnostics, ok := provider.requests[index].RequestKnobs["context_projection"].(map[string]any)
		if !ok || diagnostics["overflow_retry_ordinal"] != wantOrdinal {
			t.Fatalf("request %d diagnostics = %#v, want retry ordinal %d", index, provider.requests[index].RequestKnobs, wantOrdinal)
		}
	}
}

type delegatedOverflowCompiler struct{}

func (delegatedOverflowCompiler) Compile(_ *ConversationFile, _ agentv1.AgentMode, _ string, _ string, _ string) (CompiledConversation, error) {
	messages := []modeladapter.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "task"}}
	for i := 0; i < 8; i++ {
		messages = append(messages, modeladapter.Message{Role: "user", Content: strings.Repeat("x", 100_000)})
		if i < 7 {
			messages = append(messages, modeladapter.Message{Role: "assistant", Content: "ack"})
		}
	}
	return CompiledConversation{Messages: messages}, nil
}

func (delegatedOverflowCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

type delegatedMisconfiguredWindowCompiler struct{}

func (delegatedMisconfiguredWindowCompiler) Compile(_ *ConversationFile, _ agentv1.AgentMode, _ string, _ string, _ string) (CompiledConversation, error) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "delegated task"},
	}
	for index := 0; index < 3; index++ {
		marker := fmt.Sprintf("old-context-%d ", index+1)
		if index == 0 {
			marker = "old-canonical-context "
		}
		messages = append(messages,
			modeladapter.Message{Role: "user", Content: marker + strings.Repeat("x", 100_000)},
			modeladapter.Message{Role: "assistant", Content: fmt.Sprintf("old context %d acknowledged", index+1)},
		)
	}
	return CompiledConversation{Messages: messages}, nil
}

func (delegatedMisconfiguredWindowCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

func TestExecuteNonOverflowErrorFailsImmediately(t *testing.T) {
	adapter := newCompactionTestAdapter(overflowlessProvider{})
	req := delegation.TaskRequest{ID: "t3", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error == nil || !strings.Contains(result.Error.Error(), "boom") {
		t.Fatalf("expected immediate non-overflow failure, got %v", result.Error)
	}
}

func TestRunProviderPassRejectsDelegatedRequestOutsideSharedWindow(t *testing.T) {
	provider := &fakeDelegatedProvider{}
	adapter := &localDelegatedAgentAdapter{
		provider: provider,
		resolveBudget: func(string, string, *ConversationFile, CompiledConversation) (int, map[string]any) {
			return 1, nil
		},
	}
	conversation := &ConversationFile{TokenDetailsMaxTokens: 2_000}
	compiled := CompiledConversation{
		Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 3_000)}},
	}
	identity := localDelegatedIdentity{
		taskID:         "budget-task",
		requestID:      "budget-request",
		conversationID: "budget-conversation",
		runID:          "budget-run",
	}

	_, err := adapter.runProviderPass(
		context.Background(),
		delegation.TaskRequest{ID: identity.taskID, ModelID: "model-a", ModelName: "model-a"},
		identity,
		conversation,
		compiled,
		delegatedProviderPassView{
			HistoryMessages: compiled.Messages,
			Messages:        compiled.Messages,
			InputTokens:     estimateCompiledPromptTokens(compiled),
		},
		1,
		0,
		1,
	)
	if err == nil {
		t.Fatal("runProviderPass() error = nil, want context overflow")
	}
	terminal, ok := err.(interface{ TerminalCode() string })
	if !ok || terminal.TerminalCode() != compactionOverflowTerminalCode {
		t.Fatalf("runProviderPass() error = %T %v, want terminal code %q", err, err, compactionOverflowTerminalCode)
	}
	if provider.callCount != 0 {
		t.Fatalf("provider calls = %d, want 0 for an invalid shared-window request", provider.callCount)
	}
}

func TestBuildDelegatedAssistantToolMessagePreservesReasoningAndNormalizesIDs(t *testing.T) {
	invocations := []runtimecore.ToolInvocation{
		{ToolName: "Read", ReasoningContent: "need context", ReasoningSignature: "signature", ReasoningSignatureSource: modeladapter.ReasoningSignatureSourceOpenAIResponses, ReasoningProviderItemID: "reasoning-1"},
		{CallID: "call", ToolName: "Glob"},
	}
	normalizeDelegatedToolInvocationIDs(invocations)
	message := buildDelegatedAssistantToolMessage("", invocations)
	if message.ReasoningContent != "need context" || message.OpenAIResponsesReasoningID != "reasoning-1" {
		t.Fatalf("reasoning carrier was not preserved: %+v", message)
	}
	if len(message.ToolCalls) != 2 || message.ToolCalls[0].ID == "" || message.ToolCalls[0].ID != invocations[0].CallID {
		t.Fatalf("tool call ids do not match executable results: %+v", message.ToolCalls)
	}
}

func TestDelegatedStableMessageCountStopsAtAWindowGapAfterSystemMessage(t *testing.T) {
	compiled := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "initial task"},
		{Role: "user", Content: "old request"},
		{Role: "assistant", Content: "old reply"},
		{Role: "user", Content: "current request"},
	}
	windowed := []modeladapter.Message{
		compiled[0],
		compiled[1],
		compiled[4],
	}

	// StableMessageCount is counted in replay messages, excluding the system
	// prompt. The gap must stop the stable prefix before current request.
	if got := delegatedStableMessageCount(3, compiled, windowed); got != 1 {
		t.Fatalf("delegatedStableMessageCount() = %d, want 1 after window gap", got)
	}
}

func TestBuildChildConversationUsesTheDelegatedModelContextWindow(t *testing.T) {
	adapter := &localDelegatedAgentAdapter{
		resolveContextWindow: func(modelID string) uint32 {
			if modelID != "worker-model" {
				t.Fatalf("resolveContextWindow model = %q, want worker-model", modelID)
			}
			return 8_192
		},
	}
	store := NewConversationFileStore(t.TempDir())
	parent := testConversation(nil)
	parent.TokenDetailsMaxTokens = 128_000
	if _, err := store.SaveConversationWithEntries(parent.ConversationID, parent, parent.Entries); err != nil {
		t.Fatalf("save parent conversation: %v", err)
	}
	adapter.store = store
	child, err := adapter.buildChildConversation(delegation.TaskRequest{
		ConversationID: parent.ConversationID,
		ModelID:        "worker-model",
	}, localDelegatedIdentity{conversationID: "child"})
	if err != nil {
		t.Fatalf("buildChildConversation() error = %v", err)
	}
	if child.TokenDetailsMaxTokens != 8_192 {
		t.Fatalf("child context window = %d, want delegated model window 8192", child.TokenDetailsMaxTokens)
	}
}

// summaryControlledProvider 主请求总是成功；摘要请求按 failSummaryCalls 控制前 N 次失败。
// 记录每次摘要请求的输入估算，用于验证预压缩与减半重试。
type summaryControlledProvider struct {
	failSummaryCalls int
	summaryCalls     int
	summaryInputs    []int64
}

func (p *summaryControlledProvider) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	if strings.Contains(req.CompileSummary, "compaction summary") {
		p.summaryCalls++
		p.summaryInputs = append(p.summaryInputs, estimateCompiledPromptTokens(CompiledConversation{Messages: req.Messages, Tools: req.Tools}))
		if p.summaryCalls <= p.failSummaryCalls {
			return errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model")
		}
		if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "summary-ok"}); err != nil {
			return err
		}
		return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
	}
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished}); err != nil {
		return err
	}
	return nil
}

// buildOversizedDelegatedHistory 构造估算 token 数超过 minTokens 的消息历史。
// 消息条数足够多，使序列化截断（每条 8K 字符上限）后仍可能超出预算，
// 以验证摘要输入预压缩真正生效。
func buildOversizedDelegatedHistory(minTokens int64) []modeladapter.Message {
	messages := []modeladapter.Message{{Role: "system", Content: "sys"}}
	for estimateModelMessagesTokens(messages) <= minTokens {
		messages = append(messages,
			modeladapter.Message{Role: "user", Content: strings.Repeat("u", 8_000)},
			modeladapter.Message{Role: "assistant", Content: strings.Repeat("a", 8_000)},
		)
	}
	return messages
}

func TestExecuteLearnContextWindowOncePerTask(t *testing.T) {
	provider := &fakeDelegatedProvider{errorsBeforeSuccess: 2}
	adapter := newCompactionTestAdapter(provider)
	adapter.compiler = delegatedOverflowCompiler{}
	var learnCalls int
	var learnedModel string
	var learnedSent int64
	adapter.learnContextWindow = func(_ context.Context, modelID string, sentTokens int64) (int, int, bool) {
		learnCalls++
		learnedModel = modelID
		learnedSent = sentTokens
		return 1_000_000, 75_000, true
	}
	req := delegation.TaskRequest{ID: "learn-1", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("expected recovery, got error: %v", result.Error)
	}
	if learnCalls != 1 {
		t.Fatalf("learnContextWindow called %d times, want exactly once per task (idempotent)", learnCalls)
	}
	if learnedModel != "m1" {
		t.Fatalf("learnContextWindow model = %q, want m1", learnedModel)
	}
	if learnedSent <= 0 {
		t.Fatalf("learnContextWindow sentTokens = %d, want the failed sent input tokens > 0", learnedSent)
	}
}

func TestDelegatedSummaryInputPrecompressedToBudget(t *testing.T) {
	provider := &summaryControlledProvider{}
	adapter := newCompactionTestAdapter(provider)
	req := delegation.TaskRequest{ID: "precompact", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	budget := delegatedContextBudgetForWindow(272_000)
	messages := buildOversizedDelegatedHistory(budget)

	out, changed, err := compactDelegatedMessagesWithSummary(context.Background(), adapter, req, messages, budget, 0)
	if err != nil {
		t.Fatalf("compactDelegatedMessagesWithSummary() error = %v", err)
	}
	if !changed {
		t.Fatal("expected summary compaction to change messages")
	}
	if len(provider.summaryInputs) == 0 {
		t.Fatal("summary LLM was never called")
	}
	if got := provider.summaryInputs[0]; got > budget {
		t.Fatalf("summary input tokens = %d, want <= summary budget %d (precompression)", got, budget)
	}
	if !containsDelegatedText(out, "summary-ok") {
		t.Fatalf("rebuilt messages do not contain the LLM summary: %#v", out)
	}
}

func TestDelegatedSummaryHalvedRetryOnOverflow(t *testing.T) {
	provider := &summaryControlledProvider{failSummaryCalls: 1}
	adapter := newCompactionTestAdapter(provider)
	req := delegation.TaskRequest{ID: "summary-halve", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	budget := delegatedContextBudgetForWindow(272_000)
	messages := buildOversizedDelegatedHistory(budget)

	out, changed, err := compactDelegatedMessagesWithSummary(context.Background(), adapter, req, messages, budget, 0)
	if err != nil {
		t.Fatalf("compactDelegatedMessagesWithSummary() error = %v", err)
	}
	if !changed {
		t.Fatal("expected summary compaction to change messages")
	}
	if provider.summaryCalls != 2 {
		t.Fatalf("summary calls = %d, want 2 (failed full input + halved retry)", provider.summaryCalls)
	}
	if provider.summaryInputs[1] >= provider.summaryInputs[0] {
		t.Fatalf("retry input %d is not smaller than the failed input %d", provider.summaryInputs[1], provider.summaryInputs[0])
	}
	if !containsDelegatedText(out, "summary-ok") {
		t.Fatalf("rebuilt messages do not contain the halved-retry summary: %#v", out)
	}
}

func containsDelegatedText(messages []modeladapter.Message, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}
