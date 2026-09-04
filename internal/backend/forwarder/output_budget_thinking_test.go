package forwarder

import (
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

// 思考 token 计入 max_tokens（Anthropic/GLM 等思考模型语义）：默认/配置的 4096 会被
// 纯思考耗尽，产生 finish_reason=max_tokens 且零可见输出的回合。以下用例锁定
// resolveProviderOutputBudget 的思考下限、目录 bypass 与截断恢复 floor 行为。

func thinkingBudgetCompiled() CompiledConversation {
	return CompiledConversation{Messages: []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "hello"},
	}}
}

func TestResolveProviderOutputBudgetThinkingFloor(t *testing.T) {
	service := &Service{}
	conversation := &ConversationFile{TokenDetailsMaxTokens: 1_000_000}
	compiled := thinkingBudgetCompiled()

	tests := []struct {
		name           string
		modelName      string
		thinkingEffort string
		want           int
	}{
		// glm-5.3-flash：目录 thinking=true、maxOutput=128000；默认 4096 抬到思考下限。
		{name: "explicit effort raises default to thinking floor", modelName: "glm-5.3-flash", thinkingEffort: "xhigh", want: providerThinkingMinOutputTokens},
		// 推理恒开型模型：effort 未设置也按目录思考标记抬升。
		{name: "catalog thinking raises default without effort", modelName: "glm-5.3-flash", thinkingEffort: "", want: providerThinkingMinOutputTokens},
		// 目录把思考模型 maxOutput 记小（glm-5.2=8192）时不得压破思考下限。
		{name: "small catalog cap on thinking model is bypassed", modelName: "glm-5.2", thinkingEffort: "xhigh", want: providerThinkingMinOutputTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.resolveProviderOutputBudget("model-id", tt.modelName, conversation, compiled, tt.thinkingEffort, 0)
			if got != tt.want {
				t.Fatalf("resolveProviderOutputBudget(%s, effort=%q) = %d, want %d", tt.modelName, tt.thinkingEffort, got, tt.want)
			}
		})
	}
}

func TestResolveProviderOutputBudgetNonThinkingUnchanged(t *testing.T) {
	service := &Service{}
	conversation := &ConversationFile{TokenDetailsMaxTokens: 1_000_000}
	compiled := thinkingBudgetCompiled()

	tests := []struct {
		name           string
		modelName      string
		thinkingEffort string
		want           int
	}{
		// 非思考模型无 effort：默认 4096，目录 k2.7 硬上限（4096）继续生效。
		{name: "plain model keeps safe default", modelName: "kimi-k2.7", thinkingEffort: "", want: providerDefaultMaxOutputTokens},
		// 显式 disabled 必须关闭思考下限（用户主动关思考）。
		{name: "disabled effort keeps safe default", modelName: "glm-5.3-flash", thinkingEffort: "disabled", want: providerDefaultMaxOutputTokens},
		// 非思考模型即使带了 effort，也不受思考下限抬升，且目录硬上限继续压制
		//（Neurons 等中转站对超限 max_tokens 直接 400 的保护不回归）。
		{name: "catalog cap still clamps non thinking model", modelName: "kimi-k2.7", thinkingEffort: "xhigh", want: 4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.resolveProviderOutputBudget("model-id", tt.modelName, conversation, compiled, tt.thinkingEffort, 0)
			if got != tt.want {
				t.Fatalf("resolveProviderOutputBudget(%s, effort=%q) = %d, want %d", tt.modelName, tt.thinkingEffort, got, tt.want)
			}
		})
	}
}

func TestResolveProviderOutputBudgetRecoveryFloor(t *testing.T) {
	service := &Service{}
	compiled := thinkingBudgetCompiled()

	// 截断恢复 floor 高于思考下限时生效。
	conversation := &ConversationFile{TokenDetailsMaxTokens: 1_000_000}
	got, _ := service.resolveProviderOutputBudget("model-id", "glm-5.3-flash", conversation, compiled, "xhigh", 65_536)
	if got != 65_536 {
		t.Fatalf("recovery floor = %d, want 65536", got)
	}

	// floor 低于当前预算时不下调。
	got, _ = service.resolveProviderOutputBudget("model-id", "glm-5.3-flash", conversation, compiled, "xhigh", 100)
	if got != providerThinkingMinOutputTokens {
		t.Fatalf("low recovery floor = %d, want %d", got, providerThinkingMinOutputTokens)
	}

	// floor 不得越过上下文窗口剩余（preflight 会按 input+output+safety 校验）。
	smallWindow := &ConversationFile{TokenDetailsMaxTokens: 40_000}
	want := int(40_000 - estimateCompiledPromptTokens(compiled) - providerOutputSafetyTokens)
	got, _ = service.resolveProviderOutputBudget("model-id", "glm-5.3-flash", smallWindow, compiled, "xhigh", 65_536)
	if got != want {
		t.Fatalf("window clamped recovery floor = %d, want %d", got, want)
	}
}

func TestNextMaxOutputTokensRecoveryFloor(t *testing.T) {
	tests := []struct {
		name        string
		previousMax int
		want        int
	}{
		{name: "unknown previous uses thinking floor", previousMax: 0, want: providerThinkingMinOutputTokens},
		{name: "small previous multiplies up to thinking floor", previousMax: 4096, want: providerThinkingMinOutputTokens},
		{name: "large previous multiplies beyond thinking floor", previousMax: 32_768, want: 131_072},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextMaxOutputTokensRecoveryFloor(tt.previousMax); got != tt.want {
				t.Fatalf("nextMaxOutputTokensRecoveryFloor(%d) = %d, want %d", tt.previousMax, got, tt.want)
			}
		})
	}
}
