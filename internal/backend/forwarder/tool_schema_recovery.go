// tool_schema_recovery.go 承载 provider 明确拒绝某工具 schema/descriptor（400）时的
// 自救：把被点名的工具从后续 provider 请求中隔离（quarantine），重试一次挽救回合。
// 镜像 max_tokens_recovery.go 的恢复模式：检测 -> 设恢复态 -> requestProviderAction(resume)。
package forwarder

import (
	"encoding/json"
	"strings"
	"time"
)

// claimToolSchema400Recovery 决定并记录一次 tool-schema 400 恢复：
// 要求错误明确命名了 provider 工具，且该工具确实在本 pass advertise 的集合里；
// 同时该 (request, turnSeq, tool_schema) 命名空间本回合尚未被占用。
// 成功时把命名工具记入隔离集并返回其名。返回 claimed=false 时调用方按终态处理。
func (service *Service) claimToolSchema400Recovery(stream *ActiveStream, requestID string, turnSeq int64, cause error) (string, bool) {
	name, ok := providerToolSchema400ToolName(cause)
	if !ok {
		return "", false
	}
	var passToolNames []string
	if stream != nil {
		stream.mu.Lock()
		passToolNames = append([]string(nil), stream.ProviderPassToolNames...)
		stream.mu.Unlock()
	}
	if !providerPassAdvertisedTool(passToolNames, name) {
		return "", false
	}
	if !service.claimProvider400Recovery(provider400RecoveryToolSchema, requestID, turnSeq) {
		return "", false
	}
	recordProviderToolQuarantine(stream, name)
	return name, true
}

// snapshotProviderToolQuarantine 返回本回合已隔离的 provider 侧工具名快照。
func snapshotProviderToolQuarantine(stream *ActiveStream) []string {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return append([]string(nil), stream.ProviderToolQuarantine...)
}

// recordProviderToolQuarantine 把被 provider 拒绝的工具名记入本回合隔离集（去重）。
func recordProviderToolQuarantine(stream *ActiveStream, name string) {
	if stream == nil || strings.TrimSpace(name) == "" {
		return
	}
	normalized := normalizeToolNameForMatch(name)
	if normalized == "" {
		return
	}
	stream.mu.Lock()
	for _, existing := range stream.ProviderToolQuarantine {
		if normalizeToolNameForMatch(existing) == normalized {
			stream.mu.Unlock()
			return
		}
	}
	stream.ProviderToolQuarantine = append(stream.ProviderToolQuarantine, strings.TrimSpace(name))
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
}

// providerPassAdvertisedTool 校验 provider 错误中命名的工具确实在本 pass 的
// advertised（下发）集合里，防止把误报的引号名（如 "parameter 'temperature'"）
// 当作隔离对象。匹配用 normalizeToolNameForMatch 兼容 provider 侧转义名。
func providerPassAdvertisedTool(passTools []string, providerName string) bool {
	target := normalizeToolNameForMatch(providerName)
	if target == "" {
		return false
	}
	for _, advertised := range passTools {
		if normalizeToolNameForMatch(advertised) == target {
			return true
		}
	}
	return false
}

// filterToolDescriptorsByNameSet 从工具描述符列表中剔除命中的工具。
// 匹配优先精确名；仅当没有任何描述符精确命中时才退回归一化匹配（兼容
// "mcp tool/unsafe" ↔ "mcp_tool_unsafe"），避免因归一化碰撞误删健康工具。
// 无法解析名称的描述符保留原样，避免误删。
func filterToolDescriptorsByNameSet(tools []json.RawMessage, names []string) []json.RawMessage {
	if len(tools) == 0 || len(names) == 0 {
		return tools
	}
	exact := make(map[string]struct{}, len(names))
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			exact[trimmed] = struct{}{}
		}
		n := normalizeToolNameForMatch(name)
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}
	hasExactMatch := false
	for _, tool := range tools {
		if extracted, err := extractToolName(tool); err == nil {
			if _, ok := exact[extracted]; ok {
				hasExactMatch = true
				break
			}
		}
	}
	filtered := make([]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		extracted, err := extractToolName(tool)
		if err == nil {
			if _, ok := exact[extracted]; ok {
				continue
			}
			if !hasExactMatch && matchNormalizedToolName(extracted, normalized) {
				continue
			}
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func matchNormalizedToolName(name string, normalized []string) bool {
	target := normalizeToolNameForMatch(name)
	if target == "" {
		return false
	}
	for _, candidate := range normalized {
		if target == candidate {
			return true
		}
	}
	return false
}

// toolDescriptorNames 提取工具描述符列表的 canonical 名称，供本 pass advertised 快照。
func toolDescriptorNames(tools []json.RawMessage) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		name, err := extractToolName(tool)
		if err == nil {
			names = append(names, name)
		}
	}
	return names
}
