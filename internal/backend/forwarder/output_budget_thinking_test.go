package forwarder

import (
	"context"
	"testing"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	legacyruntime "cursor/internal/runtime"
)

// 思考 token 计入 max_tokens（Anthropic/GLM 等思考模型语义）：协议安全默认值 4096 会被
// 纯思考耗尽，产生 finish_reason=max_tokens 且零可见输出的回合。预算自适应遵循
// 「证据优先、只按已知上限适应、不自由放大」：
//   - 目录已覆盖的思考模型：仅当预算来自协议默认值（渠道未显式配置/未学习到限制）时，
//     抬升到目录记载的最大输出；
//   - 渠道显式值（含 400 降级学习持久化值）是证据，优先于启发式，不被抬升越过；
//   - max_output_tokens 截断恢复：一次性有界抬升，不做倍数放大。

func thinkingBudgetCompiled() CompiledConversation {
	return CompiledConversation{Messages: []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "hello"},
	}}
}

// fixedChannelMaxTokensResolver 模拟带显式 maxTokens 的渠道（含 400 降级学习持久化的值）。
type fixedChannelMaxTokensResolver struct {
	maxTokens int
}

func (r fixedChannelMaxTokensResolver) SelectChannelForModel(context.Context, string) (*legacyruntime.ResolvedChannel, error) {
	return &legacyruntime.ResolvedChannel{MaxTokens: r.maxTokens}, nil
}
func (fixedChannelMaxTokensResolver) ProviderStreamIdleTimeout(context.Context) time.Duration {
	return 0
}
func (fixedChannelMaxTokensResolver) TurnStaleTimeout(context.Context) time.Duration { return 0 }
func (fixedChannelMaxTokensResolver) NativeDelegationProgressTimeout(context.Context) time.Duration {
	return 0
}

func TestResolveProviderOutputBudgetThinkingLiftsToCatalogMax(t *testing.T) {
	service := &Service{}
	conversation := &ConversationFile{TokenDetailsMaxTokens: 1_000_000}
	compiled := thinkingBudgetCompiled()

	tests := []struct {
		name           string
		modelName      string
		thinkingEffort string
		want           int
	}{
		// glm-5.3-flash：目录 thinking=true、maxOutput=128000；协议默认 4096 抬到目录上限。
		{name: "explicit effort lifts default to catalog max", modelName: "glm-5.3-flash", thinkingEffort: "xhigh", want: 128_000},
		// 推理恒开型模型：effort 未设置也按目录思考标记抬升。
		{name: "catalog thinking lifts default without effort", modelName: "glm-5.3-flash", thinkingEffort: "", want: 128_000},
		// 抬升目标是目录记载的最大输出（glm-5.2=8192），不是固定大值。
		{name: "lift bounded by catalog max", modelName: "glm-5.2", thinkingEffort: "xhigh", want: 8_192},
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

func TestResolveProviderOutputBudgetChannelValueBeatsThinkingLift(t *testing.T) {
	conversation := &ConversationFile{TokenDetailsMaxTokens: 1_000_000}
	compiled := thinkingBudgetCompiled()

	tests := []struct {
		name        string
		channelMax  int
		thinkEffort string
		want        int
	}{
		// 渠道显式值（含 400 降级学习持久化的 4096）是证据：思考模型不再抬升越过它，
		// 否则每回合都会 400 -> 降级 -> 下回合又抬升，形成无意义的 ping-pong。
		{name: "learned small channel value suppresses lift", channelMax: 4_096, thinkEffort: "xhigh", want: 4_096},
		{name: "moderate channel value respected", channelMax: 8_192, thinkEffort: "xhigh", want: 8_192},
		// 显式大值仍受目录硬上限压制（catalog clamp 对所有模型生效）。
		{name: "large channel value clamped by catalog", channelMax: 200_000, thinkEffort: "xhigh", want: 128_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{resolver: fixedChannelMaxTokensResolver{maxTokens: tt.channelMax}}
			got, _ := service.resolveProviderOutputBudget("model-id", "glm-5.3-flash", conversation, compiled, tt.thinkEffort, 0)
			if got != tt.want {
				t.Fatalf("resolveProviderOutputBudget(channel=%d) = %d, want %d", tt.channelMax, got, tt.want)
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
		// 显式 disabled 必须关闭思考抬升（用户主动关思考）。
		{name: "disabled effort keeps safe default", modelName: "glm-5.3-flash", thinkingEffort: "disabled", want: providerDefaultMaxOutputTokens},
		// 目录硬上限对带 effort 的非目录思考模型同样压制
		//（Neurons 等中转站对超限 max_tokens 直接 400 的保护不回归）。
		{name: "catalog cap still clamps effort on non thinking model", modelName: "kimi-k2.7", thinkingEffort: "xhigh", want: 4_096},
		// 目录未覆盖的模型：没有已知上限可抬，维持安全默认值，交给截断恢复兜底。
		{name: "unknown model keeps safe default", modelName: "relay-custom-model", thinkingEffort: "xhigh", want: providerDefaultMaxOutputTokens},
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

func TestResolveProviderOutputBudgetRecoveryFloorBounded(t *testing.T) {
	service := &Service{}
	compiled := thinkingBudgetCompiled()

	// 目录未覆盖模型无预算可抬：截断恢复 floor 一次性抬升生效。
	conversation := &ConversationFile{TokenDetailsMaxTokens: 1_000_000}
	got, _ := service.resolveProviderOutputBudget("model-id", "relay-custom-model", conversation, compiled, "xhigh", 65_536)
	if got != 65_536 {
		t.Fatalf("recovery floor = %d, want 65536", got)
	}

	// 目录已覆盖的思考模型已抬到目录上限，较低的 floor 不再下调也不额外放大。
	got, _ = service.resolveProviderOutputBudget("model-id", "glm-5.3-flash", conversation, compiled, "xhigh", 65_536)
	if got != 128_000 {
		t.Fatalf("catalog max wins over lower floor = %d, want 128000", got)
	}

	// floor 低于当前预算时不下调。
	got, _ = service.resolveProviderOutputBudget("model-id", "relay-custom-model", conversation, compiled, "xhigh", 100)
	if got != providerDefaultMaxOutputTokens {
		t.Fatalf("low recovery floor = %d, want %d", got, providerDefaultMaxOutputTokens)
	}

	// floor 不得越过上下文窗口剩余（preflight 会按 input+output+safety 校验）。
	smallWindow := &ConversationFile{TokenDetailsMaxTokens: 40_000}
	want := int(40_000 - estimateCompiledPromptTokens(compiled) - providerOutputSafetyTokens)
	got, _ = service.resolveProviderOutputBudget("model-id", "relay-custom-model", smallWindow, compiled, "xhigh", 65_536)
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
		{name: "unknown previous uses bounded floor", previousMax: 0, want: maxOutputTokensRecoveryMinBudget},
		{name: "small previous lifts to bounded floor", previousMax: 4_096, want: maxOutputTokensRecoveryMinBudget},
		{name: "floor equals bounded minimum", previousMax: 32_768, want: 32_768},
		{name: "large previous never lowered", previousMax: 131_072, want: 131_072},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextMaxOutputTokensRecoveryFloor(tt.previousMax); got != tt.want {
				t.Fatalf("nextMaxOutputTokensRecoveryFloor(%d) = %d, want %d", tt.previousMax, got, tt.want)
			}
		})
	}
}
