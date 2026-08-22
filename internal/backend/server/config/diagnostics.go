// diagnostics.go 提供模型适配器配置的诊断与一键修正能力。
//
// 用于检测用户已导入渠道中「模型族与协议族不匹配」的配置错误——最典型的就是 claude
// 模型被配成 type=openai（导致走 OpenAI 兼容协议，前缀缓存失效）。诊断只读不改；
// 修正由调用方（ProxyService.ApplyDiagnosticFixes）在用户确认后显式落盘。
package config

import (
	"strings"

	"cursor/internal/modelchannel"
	"cursor/internal/modelcontext"
	legacyruntime "cursor/internal/runtime"
)

// DiagnosticSeverity 表示诊断问题的严重程度。
type DiagnosticSeverity string

const (
	// DiagnosticSeverityWarning 表示会导致功能降级（如缓存失效）但不阻断使用的问题。
	DiagnosticSeverityWarning DiagnosticSeverity = "warning"
)

// DiagnosticCategory 表示诊断问题的类别，用于前端分组展示与图标。
type DiagnosticCategory string

const (
	// DiagnosticCategoryProviderMismatch 表示模型族与配置的 provider 协议族不匹配
	// （如 claude 配成 openai）。这是缓存失效的根因。
	DiagnosticCategoryProviderMismatch DiagnosticCategory = "provider_mismatch"
	// DiagnosticCategoryCatalogUncovered 表示该模型不在内置能力目录中，能力未知。
	// 这类问题无法自动修正：需要用户补填能力覆盖（视觉/工具/窗口等），或等目录补录。
	// 运行时按保守策略处理（图片占位、保留渠道配置）。
	DiagnosticCategoryCatalogUncovered DiagnosticCategory = "catalog_uncovered"
)

// DiagnosticIssue 描述一条配置诊断问题及其建议修正。
type DiagnosticIssue struct {
	// Index 是问题 adapter 在 ModelAdapters 切片中的下标，用于定位修正目标。
	Index int `json:"index"`
	// Severity 是问题严重程度。
	Severity DiagnosticSeverity `json:"severity"`
	// Category 是问题类别。
	Category DiagnosticCategory `json:"category"`
	// ChannelID 是问题渠道 ID（稳定标识，避免下标在修正过程中漂移）。
	ChannelID string `json:"channelId"`
	// DisplayName 是渠道展示名，便于用户辨认。
	DisplayName string `json:"displayName"`
	// GroupName 是渠道分组名。
	GroupName string `json:"groupName"`
	// ModelID 是出问题的模型标识。
	ModelID string `json:"modelID"`
	// Field 是出问题的字段名（如 "type"）。
	Field string `json:"field"`
	// CurrentValue 是当前值。
	CurrentValue string `json:"currentValue"`
	// SuggestedValue 是建议值。
	SuggestedValue string `json:"suggestedValue"`
	// Message 是面向用户的可读说明。
	Message string `json:"message"`
}

// DiagnosticResult 是一次诊断扫描的结果。
type DiagnosticResult struct {
	// Total 是已配置的 adapter 总数。
	Total int `json:"total"`
	// Issues 是检测到的问题列表（按 index 升序）。
	Issues []DiagnosticIssue `json:"issues"`
}

// DiagnoseModelAdapters 扫描所有 adapter，检测配置问题。纯只读，不修改入参。
//
// 当前检测规则：
//   - claude-* 模型但 type!=anthropic → 建议改 anthropic（前缀缓存依赖 cache_control）
//   - gemini-* 模型但 type!=gemini   → 建议改 gemini（原生协议）
//   - 模型不在内置能力目录（catalog 未覆盖）→ 提示用户补填能力，无法自动修正
func DiagnoseModelAdapters(adapters []ModelAdapterConfig) DiagnosticResult {
	result := DiagnosticResult{Total: len(adapters)}
	for i := range adapters {
		adapter := &adapters[i]
		// Cursor 账户模型不走第三方 provider 协议或 /models 目录发现；其执行
		// 状态由专用账户网关维护，不能把尚未验证误报成可手工修正的配置缺陷。
		if legacyruntime.NormalizeModelSource(adapter.Source) == legacyruntime.ModelSourceCursorAccount {
			continue
		}
		modelID := strings.TrimSpace(adapter.ModelID)
		if modelID == "" {
			continue
		}
		channelID := strings.TrimSpace(adapter.ID)
		// 仅当 provider 推断与当前 type 不一致，且当前 type 是 openai 时才报问题。
		// 不覆盖用户显式配置的 anthropic/gemini type（即使模型名不匹配也不报，尊重用户意图）。
		if adapter.Type == "openai" {
			suggested := modelchannel.InferProviderType(modelID, adapter.Type)
			if suggested != adapter.Type {
				result.Issues = append(result.Issues, DiagnosticIssue{
					Index:          i,
					Severity:       DiagnosticSeverityWarning,
					Category:       DiagnosticCategoryProviderMismatch,
					ChannelID:      channelID,
					DisplayName:    strings.TrimSpace(adapter.DisplayName),
					GroupName:      strings.TrimSpace(adapter.GroupName),
					ModelID:        modelID,
					Field:          "type",
					CurrentValue:   adapter.Type,
					SuggestedValue: suggested,
					Message:        providerMismatchMessage(adapter.Type, suggested, modelID),
				})
			}
		}
		// 目录覆盖检查：与协议无关，所有 adapter 都检查。未覆盖时给出可操作提示，
		// 但不提供自动修正（能力未知不能瞎猜，交给用户补填或目录补录）。
		if lookup := modelcontext.Lookup(modelID); !lookup.Covered {
			result.Issues = append(result.Issues, DiagnosticIssue{
				Index:       i,
				Severity:    DiagnosticSeverityWarning,
				Category:    DiagnosticCategoryCatalogUncovered,
				ChannelID:   channelID,
				DisplayName: strings.TrimSpace(adapter.DisplayName),
				GroupName:   strings.TrimSpace(adapter.GroupName),
				ModelID:     modelID,
				Field:       "catalog",
				Message:     catalogUncoveredMessage(modelID),
			})
		}
	}
	return result
}

// ApplyDiagnosticFix 对单个问题应用修正，返回修正后的 adapter（不修改原切片）。
// 由 ProxyService.ApplyDiagnosticFixes 按 ChannelID 定位后批量调用。
//
// 修正内容：
//   - type 改为建议值
//   - protocolMode 重置为 auto（让 ClassifyProtocolGroup 按新 type 正确推断）
//   - protocolGroup 重算（anthropic→messages, gemini→gemini_native）
//   - 清理 openai 专属残留字段
func ApplyDiagnosticFix(adapter ModelAdapterConfig, suggestedType string) ModelAdapterConfig {
	fixed := adapter
	suggestedType = strings.ToLower(strings.TrimSpace(suggestedType))
	switch suggestedType {
	case "anthropic":
		fixed.Type = "anthropic"
		fixed.ProtocolMode = modelchannel.ProtocolModeAuto
		fixed.ProtocolGroup = modelchannel.ProtocolGroupAnthropicMessages
		fixed.OpenAIEndpoint = ""
		fixed.OpenAIRequestGroup = ""
	case "gemini":
		fixed.Type = "gemini"
		fixed.ProtocolMode = modelchannel.ProtocolModeAuto
		fixed.ProtocolGroup = modelchannel.ProtocolGroupGeminiNative
		fixed.OpenAIEndpoint = ""
		fixed.OpenAIRequestGroup = ""
	default:
		// 非 anthropic/gemini 的建议不处理（理论上 DiagnoseModelAdapters 不会产生此类建议）。
	}
	return fixed
}

func providerMismatchMessage(current, suggested, modelID string) string {
	switch suggested {
	case "anthropic":
		return modelID + " 是 Claude 模型，但配置为 OpenAI 协议。Claude 的前缀缓存依赖 Anthropic 原生协议（cache_control），建议改为 Anthropic 以启用缓存。"
	case "gemini":
		return modelID + " 是 Gemini 模型，但配置为 OpenAI 协议。建议改为 Gemini 原生协议。"
	default:
		return modelID + " 的协议配置（" + current + "）可能不匹配，建议改为 " + suggested + "。"
	}
}

func catalogUncoveredMessage(modelID string) string {
	return modelID + " 不在内置模型能力目录中，其能力（视觉/工具/上下文窗口等）未知。当前按保守策略运行：图片不会直传给该模型；如需启用视觉等能力，请在模型编辑页手动补充能力配置。"
}
