// types.go 定义运行时、公用命令、事件、状态与 pending 结构。
package runtimecore

import (
	"encoding/json"
	"strings"
	"time"
)

// SubagentModelOverrideSelection 表示父 run 对某类 subagent 的模型选择覆盖。
type SubagentModelOverrideSelection struct {
	SubagentType                  string `json:"subagent_type"`
	Selection                     string `json:"selection"`
	ModelID                       string `json:"model_id,omitempty"`
	MaxMode                       bool   `json:"max_mode,omitempty"`
	ParameterCount                int    `json:"parameter_count,omitempty"`
	BuiltInModel                  bool   `json:"built_in_model,omitempty"`
	IsVariantStringRepresentation bool   `json:"is_variant_string_representation,omitempty"`
}

// LookupSubagentModelOverride 按 Task subagent_type 查找运行期模型覆盖。
func LookupSubagentModelOverride(overrides map[string]SubagentModelOverrideSelection, subagentType string) (SubagentModelOverrideSelection, string, bool) {
	if len(overrides) == 0 {
		return SubagentModelOverrideSelection{}, "", false
	}
	for _, key := range subagentModelOverrideLookupKeys(subagentType) {
		if selection, ok := overrides[key]; ok {
			return selection, key, true
		}
	}
	return SubagentModelOverrideSelection{}, "", false
}

func subagentModelOverrideLookupKeys(subagentType string) []string {
	trimmed := strings.TrimSpace(subagentType)
	if trimmed == "" {
		return nil
	}
	keys := []string{trimmed}
	switch trimmed {
	case "generalPurpose":
		keys = append(keys, "explore")
	case "explore":
		keys = append(keys, "generalPurpose")
	case "browserUse":
		keys = append(keys, "browser-use")
	case "browser-use":
		keys = append(keys, "browserUse")
	}
	return keys
}

// CommandKind 表示运行时接收的上行命令类型。
type CommandKind string

const (
	// CommandKindRunRequested 表示收到 `run_request`。
	CommandKindRunRequested CommandKind = "run_requested"
	// CommandKindPrewarmRequested 表示收到 `prewarm_request`。
	CommandKindPrewarmRequested CommandKind = "prewarm_requested"
	// CommandKindCancelRequested 表示收到 `conversation_action.cancel_action`。
	CommandKindCancelRequested CommandKind = "cancel_requested"
	// CommandKindConversationActionRecordOnly 表示收到非取消型的 `conversation_action`，当前阶段只记录不推进状态。
	CommandKindConversationActionRecordOnly CommandKind = "conversation_action_record_only"
	// CommandKindExecClientMessage 表示收到 `exec_client_message`。
	CommandKindExecClientMessage CommandKind = "exec_client_message"
	// CommandKindInteractionResponse 表示收到 `interaction_response`。
	CommandKindInteractionResponse CommandKind = "interaction_response"
	// CommandKindExecClientControlMessage 表示收到 `exec_client_control_message`，当前阶段只记录不推进状态。
	CommandKindExecClientControlMessage CommandKind = "exec_client_control_message"
	// CommandKindClientHeartbeat 表示收到客户端心跳，当前阶段只记录不推进状态。
	CommandKindClientHeartbeat CommandKind = "client_heartbeat"
	// CommandKindKVClientMessage 表示收到 `kv_client_message`，当前阶段只记录不推进状态。
	CommandKindKVClientMessage CommandKind = "kv_client_message"
)

// PendingExec 表示一条尚未收口的执行桥记录。
type PendingExec struct {
	// MessageID 是打开该执行桥时下发给客户端的桥消息编号。
	MessageID uint32
	// ExecID 是执行桥唯一标识。
	ExecID string
	// ProviderPass 表示创建该执行桥时所属的 provider pass。
	ProviderPass int
	// ModelCallID 是触发该执行桥的模型调用标识。
	ModelCallID string
	// ToolCallID 是与该执行桥关联的工具调用标识。
	ToolCallID string
	// ArgsJSON 保存打开该执行桥时的原始参数 JSON，便于恢复 completed ToolCall。
	ArgsJSON []byte
	// ReasoningContent 保存触发该工具调用时的 thinking 文本，供 checkpoint/replay 续跑复用。
	ReasoningContent string
	// ReasoningSignature 保存 provider 对当前 thinking 文本签发的签名。
	ReasoningSignature string
	// ReasoningSignatureSource 保存 reasoning signature 的 provider 语义来源。
	ReasoningSignatureSource string
	// ExecKind 描述执行桥类型，例如 read、write、shellStream。
	ExecKind string
	// StreamState 描述当前流式执行桥的阶段。
	StreamState string
	// OpenedAt 表示执行桥请求发出的时间。
	OpenedAt time.Time
	// FirstChunkAt 表示 shellStream 首个输出块时间。
	FirstChunkAt time.Time
	// ChunkCount 表示 shellStream 已接收的输出块数量。
	ChunkCount int64
	// LastShellActivityAt 记录最近一次 shell 相关上行事件时间，包括输出、start、heartbeat 和 close。
	LastShellActivityAt time.Time
	// LastShellHeartbeatAt 记录最近一次 shell heartbeat 到达时间。
	LastShellHeartbeatAt time.Time
	// ShellForegroundDeadline 表示前台 shell 预计最晚应收到终态的时间点。
	ShellForegroundDeadline time.Time
	// ShellRecoveryScheduled 标记是否已经为该 shell 安排了异常收口协程。
	ShellRecoveryScheduled bool
	// StdoutBuffer 保存当前 shell 已累计的 stdout 文本。
	StdoutBuffer string
	// StderrBuffer 保存当前 shell 已累计的 stderr 文本。
	StderrBuffer string
	// ArtifactPath 保存该 exec 对应的原始桥接工件路径。
	ArtifactPath string
}

// PendingInteraction 表示一条尚未收口的交互桥记录。
type PendingInteraction struct {
	// InteractionID 是交互桥唯一标识。
	InteractionID string
	// ProviderPass 表示创建该交互桥时所属的 provider pass。
	ProviderPass int
	// ModelCallID 是触发该交互桥的模型调用标识。
	ModelCallID string
	// ToolCallID 是与该交互桥关联的工具调用标识。
	ToolCallID string
	// ArgsJSON 保存打开该交互桥时的原始参数 JSON，便于结果回写时恢复结构化状态。
	ArgsJSON []byte
	// ReasoningContent 保存触发该工具调用时的 thinking 文本，供 checkpoint/replay 续跑复用。
	ReasoningContent string
	// ReasoningSignature 保存 provider 对当前 thinking 文本签发的签名。
	ReasoningSignature string
	// ReasoningSignatureSource 保存 reasoning signature 的 provider 语义来源。
	ReasoningSignatureSource string
	// InteractionKind 描述交互类型，例如 ask_question、create_plan。
	InteractionKind string
	// OpenedAt 表示交互请求发出的时间。
	OpenedAt time.Time
	// ArtifactPath 保存该 interaction 对应的原始桥接工件路径。
	ArtifactPath string
}

// ExternalResultSummary 表示 APPLYING_EXTERNAL_RESULT 后继续下一轮编译所需的最小上下文。
type ExternalResultSummary struct {
	// Source 表示结果来源，例如 exec 或 interaction。
	Source string
	// ToolName 表示对应工具名或交互名。
	ToolName string
	// Payload 表示可直接注入 prompt 的结果摘要。
	Payload string
}

// ToolInvocation 表示一次模型产出的工具调用意图。
type ToolInvocation struct {
	// CallID 是模型层工具调用标识。
	CallID string
	// ToolName 表示工具名称，例如 Read、Write、AskQuestion。
	ToolName string
	// ArgsJSON 保存工具参数原始 JSON。
	ArgsJSON []byte
	// ReasoningContent 保存当前工具调用前伴随的 thinking 文本。
	ReasoningContent string
	// ReasoningSignature 保存 provider 对当前 thinking 文本签发的签名。
	ReasoningSignature string
	// ReasoningSignatureSource 保存 reasoning signature 的 provider 语义来源。
	ReasoningSignatureSource string
	// ReasoningProviderItemID 保存 provider 原始 reasoning output item id。
	ReasoningProviderItemID string
	// ReasoningProviderStatus 保存 provider 原始 reasoning output item status。
	ReasoningProviderStatus string
	// ReasoningProviderSummary 保存 provider 原始 reasoning output item summary。
	ReasoningProviderSummary json.RawMessage
	// ProviderItemID 保存 provider 原始 tool/function output item id。
	ProviderItemID string
	// ProviderCallID 保存 provider 原始 tool/function call id。
	ProviderCallID string
	// ProviderStatus 保存 provider 原始 tool/function output item status。
	ProviderStatus string
	// ModelCallID 表示本轮模型调用标识。
	ModelCallID string
}
