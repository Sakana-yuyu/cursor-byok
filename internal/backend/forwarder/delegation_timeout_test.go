package forwarder

import (
	"context"
	"strings"
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

// simpleDelegatedProvider 立即以 TurnFinished 结束，并记录收到的 ProviderRequest。
type simpleDelegatedProvider struct {
	requests []ProviderRequest
}

func (p *simpleDelegatedProvider) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	p.requests = append(p.requests, req)
	return sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished})
}

// TestExecuteWorkerRequestCarriesDelegatedStreamIdleTimeout 验证本地委派 worker
// 的 ProviderRequest 携带委派专用流空闲超时：否则 90s 空闲/30s 逐块看门狗会
// 误杀长静默的正常 worker，导致父代理反复重派子代理。
func TestExecuteWorkerRequestCarriesDelegatedStreamIdleTimeout(t *testing.T) {
	provider := &simpleDelegatedProvider{}
	adapter := newCompactionTestAdapter(provider)
	req := delegation.TaskRequest{ID: "t-timeout", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(provider.requests) == 0 {
		t.Fatal("no provider request captured")
	}
	for _, captured := range provider.requests {
		if strings.TrimSpace(captured.CompileSummary) == "compaction summary" {
			continue
		}
		if captured.ProviderStreamIdleTimeout != delegatedProviderStreamIdleTimeout {
			t.Fatalf("worker idle timeout = %s, want %s", captured.ProviderStreamIdleTimeout, delegatedProviderStreamIdleTimeout)
		}
	}
}
