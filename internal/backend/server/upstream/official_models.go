package upstream

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
)

// officialModelInfo 描述一个 Cursor 官方模型（混合模式下可选并透传官方执行）。
type officialModelInfo struct {
	ModelID             string  // 官方模型 ID（与 AvailableModels 的 name 一致）
	DisplayName         string  // 模型选择器显示名
	ContextWindowTokens int     // 上下文窗口
	MaxOutputTokens     int     // 最大输出
	Price               float64 // 展示价格（$/M input，仅展示用）
}

// builtinOfficialModels 是内置的官方模型目录（兜底）。
// 运行时若官方账号已登录，会经 RefreshOfficialModelsFromResponse 用官方
// GetUsableModels 的真实模型列表覆盖（动态），此处仅作为未刷新/未登录时的回退。
// 注意：官方模型走官方账号计费（Pro 会员额度/按量），且请求经本地代理透传
// 官方 api2.cursor.sh 执行，官方服务端可能检测异常请求特征，存在账号风控风险。
var builtinOfficialModels = []officialModelInfo{
	{ModelID: "gpt-5.3-codex", DisplayName: "Codex 5.3", ContextWindowTokens: 400000, MaxOutputTokens: 128000, Price: 1.25},
	{ModelID: "gpt-5.2", DisplayName: "GPT-5.2", ContextWindowTokens: 400000, MaxOutputTokens: 128000, Price: 1.25},
	{ModelID: "gpt-5", DisplayName: "GPT-5", ContextWindowTokens: 400000, MaxOutputTokens: 128000, Price: 1.25},
	{ModelID: "claude-opus-4-8", DisplayName: "Opus 4.8", ContextWindowTokens: 400000, MaxOutputTokens: 128000, Price: 15.0},
	{ModelID: "claude-opus-4-1", DisplayName: "Claude Opus 4.1", ContextWindowTokens: 200000, MaxOutputTokens: 64000, Price: 15.0},
	{ModelID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", ContextWindowTokens: 200000, MaxOutputTokens: 64000, Price: 3.0},
	{ModelID: "cursor-grok-4.5", DisplayName: "Cursor Grok 4.5", ContextWindowTokens: 400000, MaxOutputTokens: 128000, Price: 3.0},
	{ModelID: "composer-2.5", DisplayName: "Composer 2.5", ContextWindowTokens: 400000, MaxOutputTokens: 128000, Price: 3.0},
}

// officialModelCatalog 持有当前生效的官方模型目录（动态刷新 + 内置兜底）。
type officialModelCatalog struct {
	mu        sync.RWMutex
	models    []officialModelInfo
	refreshed bool
}

var officialModels = &officialModelCatalog{
	models: append([]officialModelInfo(nil), builtinOfficialModels...),
}

// officialUsableModelsPayload 是官方 GetUsableModels 响应的 JSON 结构（仅解析用）。
type officialUsableModelsPayload struct {
	Models []officialUsableModel `json:"models"`
}

type officialUsableModel struct {
	ModelID        string `json:"modelId"`
	DisplayModelID string `json:"displayModelId"`
	DisplayName    string `json:"displayName"`
	MaxMode        bool   `json:"maxMode"`
}

// officialModelsResponseMaxBytes 解压后的官方模型响应大小上限（防异常膨胀）。
const officialModelsResponseMaxBytes = 8 << 20

// RefreshOfficialModelsFromResponse 从官方 GetUsableModels 响应刷新动态
// 官方模型目录。兼容 JSON 与 binary proto（实测：Accept application/proto 时
// 官方返回 agent.v1.GetUsableModelsResponse 二进制，ModelDetails 字段为
// model_id/display_model_id/display_name/display_name_short/max_mode）。
// 官方可能对带 Accept-Encoding: gzip 的请求返回 gzip 压缩体（透传时请求头显式
// 含 Accept-Encoding，Go Transport 不会自动解压），此处先解压再解析。
// 解析失败返回错误并保持既有目录不变。
func RefreshOfficialModelsFromResponse(body []byte) error {
	// 先处理 gzip 压缩体（魔数 0x1f 0x8b）。
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("解压官方模型响应失败: %w", err)
		}
		defer reader.Close()
		body, err = io.ReadAll(io.LimitReader(reader, officialModelsResponseMaxBytes+1))
		if err != nil {
			return fmt.Errorf("读取解压后的官方模型响应失败: %w", err)
		}
		if len(body) > officialModelsResponseMaxBytes {
			return fmt.Errorf("解压后的官方模型响应超过 %d 字节上限", officialModelsResponseMaxBytes)
		}
	}
	// 先试 JSON（官方 GetUsableModels 在 Accept application/json 时返回
	// JSON：modelId/displayModelId/displayName）。
	var payload officialUsableModelsPayload
	if err := json.Unmarshal(body, &payload); err == nil && len(payload.Models) > 0 {
		return refreshOfficialModelsFromList(payload.Models)
	}
	// 再试 binary proto。必须用 agent.v1（官方实际编码）解析：aiserver.v1 的
	// ModelDetails 字段结构不同（model_name/api_key/…），wire type 不匹配会导致
	// proto.Unmarshal 报错、动态目录永远刷新失败。
	protoMsg := &agentv1.GetUsableModelsResponse{}
	if err := proto.Unmarshal(body, protoMsg); err == nil {
		models := protoMsg.GetModels()
		if len(models) == 0 {
			return nil
		}
		converted := make([]officialUsableModel, 0, len(models))
		for _, model := range models {
			name := strings.TrimSpace(model.GetModelId())
			if name == "" {
				continue
			}
			displayName := strings.TrimSpace(model.GetDisplayName())
			if displayName == "" {
				displayName = name
			}
			converted = append(converted, officialUsableModel{
				ModelID:        name,
				DisplayModelID: name,
				DisplayName:    displayName,
				MaxMode:        model.GetMaxMode(),
			})
		}
		if len(converted) > 0 {
			return refreshOfficialModelsFromList(converted)
		}
		return nil
	}
	return fmt.Errorf("无法解析官方模型响应（JSON 与 proto 均失败）")
}

func refreshOfficialModelsFromList(list []officialUsableModel) error {
	refreshed := make([]officialModelInfo, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, model := range list {
		modelID := strings.TrimSpace(model.ModelID)
		if modelID == "" || modelID == "default" {
			continue
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}
		displayName := strings.TrimSpace(model.DisplayName)
		if displayName == "" {
			displayName = modelID
		}
		refreshed = append(refreshed, officialModelInfo{
			ModelID:             modelID,
			DisplayName:         displayName,
			ContextWindowTokens: defaultOfficialContextWindow(modelID),
			MaxOutputTokens:     defaultOfficialMaxOutput(modelID),
			Price:               defaultOfficialPrice(modelID),
		})
	}
	if len(refreshed) == 0 {
		return nil
	}
	officialModels.mu.Lock()
	officialModels.models = refreshed
	officialModels.refreshed = true
	officialModels.mu.Unlock()
	return nil
}

// defaultOfficialContextWindow 对动态拉取的官方模型给出上下文窗口估计值
// （官方响应不含该字段；1M 模型按 ID 特征识别，其余按 200K 保守值）。
func defaultOfficialContextWindow(modelID string) int {
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "1m") || strings.Contains(lower, "sol") || strings.Contains(lower, "5.2") || strings.Contains(lower, "5.3") || strings.Contains(lower, "grok") {
		return 1000000
	}
	return 200000
}

func defaultOfficialMaxOutput(modelID string) int {
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "1m") || strings.Contains(lower, "sol") {
		return 128000
	}
	return 64000
}

func defaultOfficialPrice(modelID string) float64 {
	lower := strings.ToLower(modelID)
	switch {
	case strings.Contains(lower, "opus"):
		return 15.0
	case strings.Contains(lower, "grok"):
		return 3.0
	case strings.Contains(lower, "codex"):
		return 1.25
	default:
		return 3.0
	}
}

// splitOfficialModelVariant 拆分模型 variant 字符串（如 "claude-sonnet-4-5:thinking"
// 或 "gpt-5:high"）为基础模型 ID；无冒号时原样返回。
func splitOfficialModelVariant(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	if index := strings.IndexByte(modelID, ':'); index > 0 {
		return modelID[:index]
	}
	return modelID
}

// IsOfficialModel 判断模型 ID 的请求是否应走官方透传：命中当前生效的官方
// 模型目录（动态刷新后的真实官方列表 + 内置兜底，忽略 variant 后缀），或为
// auto/default 占位 ID（Cursor 客户端 Auto 模式的 run_request 携带
// model_id="default"，官方 GetUsableModels 也返回 modelId="default" 条目；
// 混合模式下 auto 对话应透传官方解析，而非落入本地 BYOK 渠道）。
func IsOfficialModel(modelID string) bool {
	base := splitOfficialModelVariant(modelID)
	base = strings.TrimSpace(base)
	if base == "" {
		return false
	}
	if base == "default" || base == "auto" {
		return true
	}
	officialModels.mu.RLock()
	defer officialModels.mu.RUnlock()
	for _, model := range officialModels.models {
		if strings.TrimSpace(model.ModelID) == base {
			return true
		}
	}
	return false
}

// OfficialModelEntries 构建官方模型在 AvailableModels 中的条目（与本地自定义
// 模型条目同构）。优先使用动态刷新后的官方列表，未刷新时用内置兜底目录。
func OfficialModelEntries() []map[string]any {
	officialModels.mu.RLock()
	models := append([]officialModelInfo(nil), officialModels.models...)
	officialModels.mu.RUnlock()
	if len(models) == 0 {
		return []map[string]any{}
	}
	output := make([]map[string]any, 0, len(models))
	for _, model := range models {
		contextTokens := model.ContextWindowTokens
		entry := map[string]any{
			"clientDisplayName":                  model.DisplayName,
			"defaultOn":                          false,
			"degradationStatus":                  "DEGRADATION_STATUS_UNSPECIFIED",
			"inputboxShortModelName":             model.DisplayName,
			"isRecommendedForBackgroundComposer": false,
			"name":                               model.ModelID,
			"namedModelSectionIndex":             2,
			"parameterDefinitions":               buildThinkingEffortParameterDefinitions("anthropic"),
			"serverModelName":                    model.ModelID,
			"supportsAgent":                      true,
			"supportsImages":                     true,
			"supportsMaxMode":                    false,
			"supportsNonMaxMode":                 true,
			"supportsPlanMode":                   true,
			"supportsSandboxing":                 true,
			"supportsThinking":                   true,
			"tagline":                            "Cursor 官方模型（需官方账号登录，官方计费）",
			"tooltipData": map[string]any{
				"markdownContent": officialModelTooltip(model),
			},
			"tooltipDataForMaxMode": map[string]any{
				"markdownContent": officialModelTooltip(model),
			},
			"variants": buildOfficialModelVariants(model),
		}
		if contextTokens > 0 && contextTokens <= int(^uint32(0)>>1) {
			entry["contextTokenLimit"] = contextTokens
			entry["contextTokenLimitForMaxMode"] = contextTokens
			entry["autoContextMaxTokens"] = contextTokens
			entry["supportsAutoContext"] = true
		} else {
			entry["supportsAutoContext"] = false
		}
		entry["isLongContextOnly"] = false
		entry["isUserAdded"] = false
		if model.Price > 0 {
			entry["price"] = model.Price
		}
		output = append(output, entry)
	}
	return output
}

// officialModelRefs 返回当前生效官方模型目录的模型 ID 列表（透传官方执行）。
// 混合模式下 AvailableModels 的默认/回退模型配置必须引用官方列表，
// 避免 defaultModel 指向已被模型列表排除的 BYOK 自定义模型。
func officialModelRefs() []string {
	officialModels.mu.RLock()
	models := append([]officialModelInfo(nil), officialModels.models...)
	officialModels.mu.RUnlock()
	refs := make([]string, 0, len(models))
	for _, model := range models {
		if modelID := strings.TrimSpace(model.ModelID); modelID != "" {
			refs = append(refs, modelID)
		}
	}
	return refs
}

// firstOfficialModelRef 返回官方模型目录第一个模型 ID（无则空串），
// 用作混合模式下的默认模型。
func firstOfficialModelRef() string {
	refs := officialModelRefs()
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

// buildOfficialCLIModelDetails 构建官方模型在 GetUsableModels / CLI 默认模型
// 响应中的条目，字段与官方透传响应对齐（modelId/displayModelId/displayName/
// maxMode）；无本地 apiKeyCredentials——官方模型走官方账号透传，不携带本地密钥。
func buildOfficialCLIModelDetails() []map[string]any {
	officialModels.mu.RLock()
	models := append([]officialModelInfo(nil), officialModels.models...)
	officialModels.mu.RUnlock()
	output := make([]map[string]any, 0, len(models))
	for _, model := range models {
		modelID := strings.TrimSpace(model.ModelID)
		if modelID == "" {
			continue
		}
		displayName := strings.TrimSpace(model.DisplayName)
		if displayName == "" {
			displayName = modelID
		}
		output = append(output, map[string]any{
			"modelId":        modelID,
			"displayModelId": modelID,
			"displayName":    displayName,
			"maxMode":        false,
		})
	}
	return output
}

func officialModelTooltip(model officialModelInfo) string {
	var builder strings.Builder
	builder.WriteString("**")
	builder.WriteString(model.DisplayName)
	builder.WriteString("** — Cursor 官方模型\n\n")
	if model.ContextWindowTokens > 0 {
		builder.WriteString("- 上下文窗口：")
		builder.WriteString(formatTokenCount(model.ContextWindowTokens))
		builder.WriteString("\n")
	}
	if model.MaxOutputTokens > 0 {
		builder.WriteString("- 最大输出：")
		builder.WriteString(formatTokenCount(model.MaxOutputTokens))
		builder.WriteString("\n")
	}
	if model.Price > 0 {
		builder.WriteString("- 价格（$ / M input）：")
		builder.WriteString(formatModelPrice(model.Price, "usd"))
		builder.WriteString("\n")
	}
	builder.WriteString("- 计费与风险：走官方账号额度，经本地代理透传官方执行，官方可能风控\n")
	return builder.String()
}

func buildOfficialModelVariants(model officialModelInfo) []map[string]any {
	variant := map[string]any{
		"displayName":                 model.DisplayName,
		"displayNameOutsidePicker":    model.DisplayName,
		"isDefaultNonMaxConfig":       true,
		"isMaxMode":                   false,
		"variantStringRepresentation": model.ModelID,
	}
	return []map[string]any{variant}
}
