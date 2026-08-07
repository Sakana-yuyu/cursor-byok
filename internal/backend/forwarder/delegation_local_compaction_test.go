package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

type fakeDelegatedCompiler struct{}

func (fakeDelegatedCompiler) Compile(_ *ConversationFile, _ agentv1.AgentMode, _ string, _ string, _ string, _ bool) (CompiledConversation, error) {
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
}

func (f *fakeDelegatedProvider) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	f.callCount++
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

func TestExecuteNonOverflowErrorFailsImmediately(t *testing.T) {
	adapter := newCompactionTestAdapter(overflowlessProvider{})
	req := delegation.TaskRequest{ID: "t3", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error == nil || !strings.Contains(result.Error.Error(), "boom") {
		t.Fatalf("expected immediate non-overflow failure, got %v", result.Error)
	}
}
