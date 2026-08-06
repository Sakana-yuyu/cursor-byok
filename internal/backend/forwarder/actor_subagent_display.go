// actor_subagent_display.go 承载 Task 工具调用的展示层改写：subagent 类型名归一、
// override 生效模型计算与 toolCall 克隆改写（仅影响 Cursor 客户端显示）。
package forwarder

import (
	"strings"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func (service *Service) rewriteTaskToolCallModelForDisplay(stream *ActiveStream, toolCall *agentv1.ToolCall) *agentv1.ToolCall {
	if service == nil || stream == nil || toolCall == nil {
		return toolCall
	}
	taskToolCall := toolCall.GetTaskToolCall()
	if taskToolCall == nil || taskToolCall.GetArgs() == nil {
		return toolCall
	}
	subagentType := taskSubagentTypeNameForDisplay(taskToolCall.GetArgs().GetSubagentType())
	stream.mu.Lock()
	parentModelID := strings.TrimSpace(stream.ModelID)
	overrides := cloneSubagentModelOverrides(stream.SubagentModelOverrides)
	stream.mu.Unlock()
	effectiveModelID := effectiveTaskDisplayModelID(subagentType, parentModelID, overrides)
	if effectiveModelID == "" {
		return toolCall
	}
	cloned, ok := proto.Clone(toolCall).(*agentv1.ToolCall)
	if !ok || cloned == nil {
		return toolCall
	}
	clonedTaskToolCall := cloned.GetTaskToolCall()
	if clonedTaskToolCall == nil || clonedTaskToolCall.GetArgs() == nil {
		return toolCall
	}
	clonedTaskToolCall.Args.Model = &effectiveModelID
	return cloned
}

func taskSubagentTypeNameForDisplay(subagentType *agentv1.SubagentType) string {
	if subagentType == nil || subagentType.GetType() == nil {
		return ""
	}
	switch item := subagentType.GetType().(type) {
	case *agentv1.SubagentType_Explore:
		return "explore"
	case *agentv1.SubagentType_BrowserUse:
		return "browser-use"
	case *agentv1.SubagentType_Shell:
		return "shell"
	case *agentv1.SubagentType_Custom:
		return strings.TrimSpace(item.Custom.GetName())
	default:
		return ""
	}
}

func effectiveTaskDisplayModelID(subagentType string, parentModelID string, overrides map[string]runtimecore.SubagentModelOverrideSelection) string {
	if override, _, ok := runtimecore.LookupSubagentModelOverride(overrides, subagentType); ok {
		switch strings.TrimSpace(override.Selection) {
		case "model":
			return strings.TrimSpace(override.ModelID)
		case "inherit":
			return strings.TrimSpace(parentModelID)
		case "disabled":
			return ""
		}
	}
	// 没有 override 时 fallback 到父进程模型，与 openTask 的行为保持一致。
	return strings.TrimSpace(parentModelID)
}
