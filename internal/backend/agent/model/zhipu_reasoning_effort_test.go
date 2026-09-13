package modeladapter

import "testing"

// TestGLMSupportsReasoningEffort 验证 GLM 版本判定：reasoning_effort 自 GLM-5.2
// 起受支持；禁用思考自 GLM-5.3 起不被上游接受（官方“请确保开启思考”）。
func TestGLMSupportsReasoningEffort(t *testing.T) {
	tests := []struct {
		modelID    string
		wantEffort bool
		wantForced bool
		wantMajor  int
		wantMinor  int
	}{
		{"glm-4.5", false, false, 4, 5},
		{"glm-4.5-air", false, false, 4, 5},
		{"glm-4.6", false, false, 4, 6},
		{"glm-4.6v-flash", false, false, 4, 6},
		{"glm-4.7-flashx", false, false, 4, 7},
		{"glm-5", false, false, 5, 0},
		{"glm-5v-turbo", false, false, 5, 0},
		{"glm-5.1", false, false, 5, 1},
		{"glm-5.2", true, false, 5, 2},
		{"glm-5.3", true, true, 5, 3},
		{"glm-5.3-flash", true, true, 5, 3},
		{"GLM-5.3-Flash", true, true, 5, 3},
		{"glm-5.4", true, true, 5, 4},
		{"glm-6", true, true, 6, 0},
		{"deepseek-v4", false, false, 0, 0},
	}
	for _, tt := range tests {
		major, minor := glmModelVersion(tt.modelID)
		if major != tt.wantMajor || minor != tt.wantMinor {
			t.Errorf("glmModelVersion(%q) = %d,%d want %d,%d", tt.modelID, major, minor, tt.wantMajor, tt.wantMinor)
		}
		if got := glmSupportsReasoningEffort(tt.modelID); got != tt.wantEffort {
			t.Errorf("glmSupportsReasoningEffort(%q) = %v, want %v", tt.modelID, got, tt.wantEffort)
		}
		if got := glmForcesThinking(tt.modelID); got != tt.wantForced {
			t.Errorf("glmForcesThinking(%q) = %v, want %v", tt.modelID, got, tt.wantForced)
		}
	}
}

// TestZhipuReasoningEffortMapping 验证运行时思考强度到 GLM reasoning_effort
// 的映射：GLM-5.2 全档位透传；GLM-5.3 仅 low/high/max（medium 归 high、
// xhigh 归 max），对齐官方 Coding Plan 映射表。
func TestZhipuReasoningEffortMapping(t *testing.T) {
	tests := []struct {
		effort  string
		modelID string
		want    string
	}{
		{"low", "glm-5.2", "low"},
		{"medium", "glm-5.2", "medium"},
		{"high", "glm-5.2", "high"},
		{"xhigh", "glm-5.2", "xhigh"},
		{"max", "glm-5.2", "max"},
		{"low", "glm-5.3", "low"},
		{"medium", "glm-5.3", "high"},
		{"high", "glm-5.3", "high"},
		{"xhigh", "glm-5.3", "max"},
		{"max", "glm-5.3", "max"},
		{"", "glm-5.3", "max"},
		{"weird", "glm-5.3", "max"},
	}
	for _, tt := range tests {
		if got := zhipuReasoningEffort(tt.effort, tt.modelID); got != tt.want {
			t.Errorf("zhipuReasoningEffort(%q, %q) = %q, want %q", tt.effort, tt.modelID, got, tt.want)
		}
	}
}

// TestZhipuChatCompatibilityReasoningEffort 验证 zhipu quirk 分支：
// GLM-5.2+ 透传（映射后的）reasoning_effort 并保持 thinking enabled；
// 旧 GLM 模型仍删除 reasoning_effort（官方不支持的参数会导致 400）。
func TestZhipuChatCompatibilityReasoningEffort(t *testing.T) {
	tests := []struct {
		name         string
		modelID      string
		effort       string
		wantEffort   any // nil = 期望键不存在
		wantThinking string
	}{
		{"glm-5.2 passthrough", "glm-5.2", "high", "high", "enabled"},
		{"glm-5.2 xhigh passthrough", "glm-5.2", "xhigh", "xhigh", "enabled"},
		{"glm-5.3 medium maps high", "glm-5.3", "medium", "high", "enabled"},
		{"glm-5.3 xhigh maps max", "glm-5.3", "xhigh", "max", "enabled"},
		{"glm-5.1 drops effort", "glm-5.1", "high", nil, "enabled"},
		{"glm-4.6 drops effort", "glm-4.6", "max", nil, "enabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"model":            tt.modelID,
				"reasoning_effort": tt.effort,
			}
			applyOpenAIChatCompletionsCompatibility(body, "https://open.bigmodel.cn/api/paas/v4", tt.modelID, false)
			if tt.wantEffort == nil {
				if _, ok := body["reasoning_effort"]; ok {
					t.Fatalf("reasoning_effort = %v, want deleted", body["reasoning_effort"])
				}
			} else if got := body["reasoning_effort"]; got != tt.wantEffort {
				t.Fatalf("reasoning_effort = %v, want %v", got, tt.wantEffort)
			}
			thinking, ok := body["thinking"].(map[string]any)
			if !ok || thinking["type"] != tt.wantThinking {
				t.Fatalf("thinking = %v, want type %q", body["thinking"], tt.wantThinking)
			}
		})
	}
}

// TestApplyOpenAIThinkingDisableGLMForced 验证禁用思考请求在 GLM-5.3+ 上
// 降级为 enabled + reasoning_effort=low（官方迁移指引），旧 GLM 保持 disabled。
func TestApplyOpenAIThinkingDisableGLMForced(t *testing.T) {
	req := StreamRequest{ThinkingEffort: "disabled", RequestKnobs: map[string]any{}}
	body := map[string]any{
		"model": "glm-5.3",
	}
	applyOpenAIThinkingDisable(body, req, "https://open.bigmodel.cn/api/paas/v4", "glm-5.3", "/chat/completions")
	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %v, want type enabled", body["thinking"])
	}
	if body["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort = %v, want low", body["reasoning_effort"])
	}

	legacy := map[string]any{
		"model": "glm-4.6",
	}
	legacyReq := StreamRequest{ThinkingEffort: "disabled", RequestKnobs: map[string]any{}}
	applyOpenAIThinkingDisable(legacy, legacyReq, "https://open.bigmodel.cn/api/paas/v4", "glm-4.6", "/chat/completions")
	legacyThinking, ok := legacy["thinking"].(map[string]any)
	if !ok || legacyThinking["type"] != "disabled" {
		t.Fatalf("thinking = %v, want type disabled", legacy["thinking"])
	}
	if _, ok := legacy["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort = %v, want deleted", legacy["reasoning_effort"])
	}
}

// TestAnthropicProviderCompatibilityGLMForced 验证 anthropic 协议路径上
// GLM-5.3 的禁用思考同样降级为 enabled（zhipu_glm 供应商模板走 anthropic 端点）。
func TestAnthropicProviderCompatibilityGLMForced(t *testing.T) {
	req := StreamRequest{ThinkingEffort: "disabled", RequestKnobs: map[string]any{}}
	body := map[string]any{
		"model":    "glm-5.3",
		"thinking": map[string]any{"type": "disabled"},
	}
	applyAnthropicProviderCompatibility(body, req, "https://open.bigmodel.cn/api/anthropic", "glm-5.3")
	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %v, want type enabled", body["thinking"])
	}

	// 非 GLM 渠道不受影响：disabled 保持原样
	plain := map[string]any{
		"model":    "claude-sonnet-5",
		"thinking": map[string]any{"type": "disabled"},
	}
	plainReq := StreamRequest{ThinkingEffort: "disabled", RequestKnobs: map[string]any{}}
	applyAnthropicProviderCompatibility(plain, plainReq, "https://api.anthropic.com", "claude-sonnet-5")
	plainThinking, ok := plain["thinking"].(map[string]any)
	if !ok || plainThinking["type"] != "disabled" {
		t.Fatalf("thinking = %v, want type disabled", plain["thinking"])
	}
}
