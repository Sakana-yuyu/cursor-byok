package forwarder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

// shellCircuitLocalBlockLimit 是 Shell 熔断开路后允许的本地拦截次数，
// 达到上限即终止 provider 循环，防止模型在熔断后继续纠缠 Shell。
const shellCircuitLocalBlockLimit = 1

// shellCircuitFingerprintOpenLimit 是同一指纹在单轮内达到的拒绝次数阈值，
// 达到即开路（此后本轮剩余时间 Shell 不可用）。
const shellCircuitFingerprintOpenLimit = 2

// shellCircuitFingerprintClasses 是允许触发开路、也允许被 reset 清零的稳定拒绝类别。
var shellCircuitFingerprintClasses = []string{"permission", "policy", "capability"}

type shellCircuitState struct {
	Open            bool
	RejectionClass  string
	Reason          string
	ParseRejections int
	LocalBlocks     int
	FingerprintHits map[string]int
}

// shellCircuitFingerprint 是确定性拒绝熔断的唯一指纹定义：
// tool_name + canonical_args（规范化 command + 规范化 cwd）+ validation_error_class。
// pre-dispatch 校验拒绝、terminal 拒绝、reset 清零和账本重放都必须经过本函数，
// 保证同一确定性错误在两条路径上落在同一指纹、按同一阈值开路。
func shellCircuitFingerprint(commandHash string, cwdHash string, errorClass string) string {
	return planContentHash(strings.Join([]string{"Shell", commandHash, cwdHash, strings.TrimSpace(errorClass)}, "\x00"))
}

// planContentHash 计算计划文本/指纹串的 sha256 摘要。
func planContentHash(planText string) string {
	text := strings.TrimSpace(planText)
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (service *Service) recordShellRejection(stream *ActiveStream, pending runtimecore.PendingExec, message *agentv1.ExecClientMessage) error {
	if service == nil || stream == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return nil
	}
	reason, rejectionClass := shellTerminalRejection(message)
	if rejectionClass == "" {
		return service.recordShellCircuitSuccess(stream, pending, message)
	}
	commandHash, argsHash, cwdHash := shellInvocationHashes(pending.ArgsJSON)
	providerItemID, providerCallID, providerStatus := providerToolCorrelation(stream, pending.ToolCallID)
	fingerprint := shellCircuitFingerprint(commandHash, cwdHash, rejectionClass)
	circuit := currentTurnShellCircuit(stream)
	count := circuit.FingerprintHits[fingerprint] + 1
	values := map[string]any{
		"source":           "shell_terminal",
		"provider_pass":    pending.ProviderPass,
		"model_call_id":    strings.TrimSpace(pending.ModelCallID),
		"provider_item_id": providerItemID,
		"provider_call_id": providerCallID,
		"provider_status":  providerStatus,
		"tool_call_id":     strings.TrimSpace(pending.ToolCallID),
		"exec_id":          strings.TrimSpace(pending.ExecID),
		"message_id":       pending.MessageID,
		"terminal_variant": shellTerminalVariant(message),
		"rejection_class":  rejectionClass,
		"rejected_reason":  sanitizeShellRejectedReason(reason),
		"fingerprint":      fingerprint,
		"tool_name":        "Shell",
		"command_hash":     commandHash,
		"args_hash":        argsHash,
		"cwd_hash":         cwdHash,
		"count":            count,
	}
	entries := []HistoryEntry{newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_rejection_fingerprint", values)}
	// 只有稳定、可归因的 terminal 拒绝（permission/policy/capability）在同一指纹上达到阈值才开路；
	// Skipped 与 transport 不会以 terminal 拒绝进入此路径，command_parse 由 shouldOpenShellCircuit 排除。
	openCircuit := shouldOpenShellCircuit(circuit, rejectionClass) && count >= shellCircuitFingerprintOpenLimit
	if openCircuit {
		entries = append(entries, newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_circuit_open", map[string]any{
			"source":                "shell_terminal",
			"provider_pass":         pending.ProviderPass,
			"model_call_id":         strings.TrimSpace(pending.ModelCallID),
			"tool_call_id":          strings.TrimSpace(pending.ToolCallID),
			"exec_id":               strings.TrimSpace(pending.ExecID),
			"message_id":            pending.MessageID,
			"rejection_class":       rejectionClass,
			"rejected_reason":       sanitizeShellRejectedReason(reason),
			"parse_rejection_count": circuit.ParseRejections + boolToInt(rejectionClass == "command_parse"),
		}))
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "shell_circuit_decision", map[string]any{
			"tool_call_id":      strings.TrimSpace(pending.ToolCallID),
			"exec_id":           strings.TrimSpace(pending.ExecID),
			"rejection_class":   rejectionClass,
			"rejected_reason":   sanitizeShellRejectedReason(reason),
			"circuit_open":      openCircuit || circuit.Open,
			"parse_rejections":  circuit.ParseRejections + boolToInt(rejectionClass == "command_parse"),
			"command_hash":      commandHash,
			"cwd_hash":          cwdHash,
			"fingerprint_count": count,
		})
	}
	_, err := service.appendConversationEntries(stream, stream.ConversationID, entries)
	return err
}

func shouldOpenShellCircuit(circuit shellCircuitState, rejectionClass string) bool {
	return !circuit.Open && strings.TrimSpace(rejectionClass) != "" && rejectionClass != "command_parse"
}

// recordPreDispatchShellRejection 把 pre-dispatch 校验拒绝纳入与 terminal 拒绝相同的指纹熔断账本
// （同一 metadata 事件、同一阈值、同一 event-sourced 状态），防止模型对同一确定性校验错误无限重试
// ——曾造成 inspect 子代理同一 git 命令被连拒 11 次、UI 刷屏 "Skipped git"。
// 返回熔断是否随本次记录开路。
func (service *Service) recordPreDispatchShellRejection(stream *ActiveStream, invocation runtimecore.ToolInvocation, cause error) (bool, error) {
	if service == nil || stream == nil || cause == nil {
		return false, nil
	}
	reason := cause.Error()
	rejectionClass := classifyShellRejection(reason)
	commandHash, argsHash, cwdHash := shellInvocationHashes(invocation.ArgsJSON)
	fingerprint := shellCircuitFingerprint(commandHash, cwdHash, rejectionClass)
	circuit := currentTurnShellCircuit(stream)
	count := circuit.FingerprintHits[fingerprint] + 1
	values := map[string]any{
		"source":          "pre_dispatch_policy",
		"provider_pass":   currentProviderPass(stream),
		"model_call_id":   strings.TrimSpace(invocation.ModelCallID),
		"tool_call_id":    strings.TrimSpace(invocation.CallID),
		"rejection_class": rejectionClass,
		"rejected_reason": sanitizeShellRejectedReason(reason),
		"fingerprint":     fingerprint,
		"tool_name":       "Shell",
		"command_hash":    commandHash,
		"args_hash":       argsHash,
		"cwd_hash":        cwdHash,
		"count":           count,
	}
	entries := []HistoryEntry{newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_rejection_fingerprint", values)}
	openCircuit := shouldOpenShellCircuit(circuit, rejectionClass) && count >= shellCircuitFingerprintOpenLimit
	if openCircuit {
		entries = append(entries, newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_circuit_open", map[string]any{
			"source":                "pre_dispatch_policy",
			"provider_pass":         currentProviderPass(stream),
			"model_call_id":         strings.TrimSpace(invocation.ModelCallID),
			"tool_call_id":          strings.TrimSpace(invocation.CallID),
			"rejection_class":       rejectionClass,
			"rejected_reason":       sanitizeShellRejectedReason(reason),
			"parse_rejection_count": circuit.ParseRejections + boolToInt(rejectionClass == "command_parse"),
		}))
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "shell_circuit_decision", map[string]any{
			"source":            "pre_dispatch_policy",
			"tool_call_id":      strings.TrimSpace(invocation.CallID),
			"rejection_class":   rejectionClass,
			"rejected_reason":   sanitizeShellRejectedReason(reason),
			"circuit_open":      openCircuit || circuit.Open,
			"command_hash":      commandHash,
			"cwd_hash":          cwdHash,
			"fingerprint_count": count,
		})
	}
	_, err := service.appendConversationEntries(stream, stream.ConversationID, entries)
	return openCircuit, err
}

func shellCircuitFingerprintResetEntry(stream *ActiveStream, pending runtimecore.PendingExec, source string) (HistoryEntry, bool) {
	circuit := currentTurnShellCircuit(stream)
	if circuit.Open {
		return HistoryEntry{}, false
	}
	commandHash, _, cwdHash := shellInvocationHashes(pending.ArgsJSON)
	hits := 0
	for _, class := range shellCircuitFingerprintClasses {
		hits += circuit.FingerprintHits[shellCircuitFingerprint(commandHash, cwdHash, class)]
	}
	if hits == 0 {
		return HistoryEntry{}, false
	}
	return newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_circuit_fingerprint_reset", map[string]any{
		"source":                    source,
		"provider_pass":             pending.ProviderPass,
		"model_call_id":             strings.TrimSpace(pending.ModelCallID),
		"tool_call_id":              strings.TrimSpace(pending.ToolCallID),
		"exec_id":                   strings.TrimSpace(pending.ExecID),
		"command_hash":              commandHash,
		"cwd_hash":                  cwdHash,
		"previous_fingerprint_hits": hits,
	}), true
}

func (service *Service) recordShellCircuitSuccess(stream *ActiveStream, pending runtimecore.PendingExec, message *agentv1.ExecClientMessage) error {
	if !shellTerminalSucceeded(message) {
		return nil
	}
	circuit := currentTurnShellCircuit(stream)
	if circuit.Open {
		return nil
	}
	entries := []HistoryEntry(nil)
	if entry, ok := shellCircuitFingerprintResetEntry(stream, pending, "shell_success"); ok {
		entries = append(entries, entry)
	}
	if circuit.ParseRejections > 0 {
		entries = append(entries, newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_circuit_parse_reset", map[string]any{
			"source":               "shell_success",
			"provider_pass":        pending.ProviderPass,
			"model_call_id":        strings.TrimSpace(pending.ModelCallID),
			"tool_call_id":         strings.TrimSpace(pending.ToolCallID),
			"exec_id":              strings.TrimSpace(pending.ExecID),
			"previous_parse_count": circuit.ParseRejections,
		}))
	}
	if len(entries) == 0 {
		return nil
	}
	_, err := service.appendConversationEntries(stream, stream.ConversationID, entries)
	return err
}

func shellTerminalSucceeded(message *agentv1.ExecClientMessage) bool {
	if message == nil {
		return false
	}
	if legacy := message.GetShellResult(); legacy != nil {
		_, ok := legacy.GetResult().(*agentv1.ShellResult_Success)
		return ok
	}
	if stream := message.GetShellStream(); stream != nil {
		if exit, ok := stream.GetEvent().(*agentv1.ShellStream_Exit); ok && exit.Exit != nil {
			return exit.Exit.GetCode() == 0
		}
	}
	return false
}

func shellTerminalRejection(message *agentv1.ExecClientMessage) (string, string) {
	if message == nil {
		return "", ""
	}
	if legacy := message.GetShellResult(); legacy != nil {
		switch item := legacy.GetResult().(type) {
		case *agentv1.ShellResult_Rejected:
			if item.Rejected != nil {
				return item.Rejected.GetReason(), classifyShellRejection(item.Rejected.GetReason())
			}
		case *agentv1.ShellResult_PermissionDenied:
			if item.PermissionDenied != nil {
				return item.PermissionDenied.GetError(), "permission"
			}
		}
		return "", ""
	}
	stream := message.GetShellStream()
	if stream == nil {
		return "", ""
	}
	switch event := stream.GetEvent().(type) {
	case *agentv1.ShellStream_Rejected:
		if event.Rejected != nil {
			return event.Rejected.GetReason(), classifyShellRejection(event.Rejected.GetReason())
		}
	case *agentv1.ShellStream_PermissionDenied:
		if event.PermissionDenied != nil {
			return event.PermissionDenied.GetError(), "permission"
		}
	}
	return "", ""
}

func classifyShellRejection(reason string) string {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(normalized, "permission"), strings.Contains(normalized, "denied"):
		return "permission"
	case strings.Contains(normalized, "parse"), strings.Contains(normalized, "syntax"):
		return "command_parse"
	case strings.Contains(normalized, "skip"):
		return "capability"
	default:
		return "policy"
	}
}

func shellTerminalVariant(message *agentv1.ExecClientMessage) string {
	if message == nil {
		return "synthetic_protocol_error"
	}
	if legacy := message.GetShellResult(); legacy != nil {
		switch legacy.GetResult().(type) {
		case *agentv1.ShellResult_Rejected:
			return "legacy_rejected"
		case *agentv1.ShellResult_PermissionDenied:
			return "legacy_permission_denied"
		case *agentv1.ShellResult_Success:
			return "legacy_success"
		case *agentv1.ShellResult_Failure:
			return "legacy_failure"
		case *agentv1.ShellResult_Timeout:
			return "legacy_timeout"
		case *agentv1.ShellResult_SpawnError:
			return "legacy_spawn_error"
		default:
			return "legacy_unknown"
		}
	}
	stream := message.GetShellStream()
	if stream == nil {
		return "synthetic_protocol_error"
	}
	switch stream.GetEvent().(type) {
	case *agentv1.ShellStream_Exit:
		return "stream_exit"
	case *agentv1.ShellStream_Rejected:
		return "stream_rejected"
	case *agentv1.ShellStream_PermissionDenied:
		return "stream_permission_denied"
	case *agentv1.ShellStream_Backgrounded:
		return "stream_backgrounded"
	default:
		return "stream_unknown"
	}
}

func sanitizeShellRejectedReason(reason string) string {
	const maxReasonBytes = 512
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return ""
	}
	words := strings.Fields(trimmed)
	redactFollowing := 0
	for index, word := range words {
		if redactFollowing > 0 {
			words[index] = "[redacted]"
			redactFollowing--
			continue
		}
		normalized := strings.ToLower(word)
		switch {
		case strings.HasPrefix(normalized, "authorization:"):
			words[index] = "[redacted]"
			redactFollowing = 2
		case normalized == "bearer":
			words[index] = "[redacted]"
			redactFollowing = 1
		case strings.HasPrefix(normalized, "bearer"), strings.HasPrefix(normalized, "sk-"):
			words[index] = "[redacted]"
		case strings.Contains(normalized, "api_key="), strings.Contains(normalized, "apikey="), strings.Contains(normalized, "token="), strings.Contains(normalized, "secret="), strings.Contains(normalized, "password="):
			if separator := strings.Index(word, "="); separator >= 0 {
				words[index] = word[:separator+1] + "[redacted]"
			}
		}
	}
	result := strings.Join(words, " ")
	if len(result) > maxReasonBytes {
		result = truncateProjectedUTF8(result, maxReasonBytes-len("…")) + "…"
	}
	return result
}

func providerToolCorrelation(stream *ActiveStream, toolCallID string) (string, string, string) {
	if stream == nil || strings.TrimSpace(toolCallID) == "" {
		return "", "", ""
	}
	toolCallID = strings.TrimSpace(toolCallID)
	stream.mu.Lock()
	conversation := stream.CheckpointConversation
	entries := []HistoryEntry(nil)
	if conversation != nil {
		entries = append(entries, conversation.Entries...)
	}
	stream.mu.Unlock()
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if strings.TrimSpace(entry.Kind) != "tool_call" || strings.TrimSpace(entry.ToolCallID) != toolCallID {
			continue
		}
		var payload toolCallEntryPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			return strings.TrimSpace(payload.ProviderItemID), strings.TrimSpace(payload.ProviderCallID), strings.TrimSpace(payload.ProviderStatus)
		}
	}
	return "", "", ""
}

func shellInvocationHashes(argsJSON []byte) (string, string, string) {
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)
	command := readStringMapValue(args, "command")
	cwd := readStringMapValue(args, "working_directory", "workingDirectory")
	normalizedCommand := strings.ToLower(strings.Join(strings.Fields(command), " "))
	normalizedCWD := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cwd), "\\", "/"))
	return planContentHash(normalizedCommand), planContentHash(strings.TrimSpace(string(argsJSON))), planContentHash(normalizedCWD)
}

func currentTurnShellCircuit(stream *ActiveStream) shellCircuitState {
	state := shellCircuitState{FingerprintHits: make(map[string]int)}
	if stream == nil {
		return state
	}
	stream.mu.Lock()
	conversation := stream.CheckpointConversation
	turnSeq := stream.TurnSeq
	entries := []HistoryEntry(nil)
	if conversation != nil {
		entries = append(entries, conversation.Entries...)
	}
	stream.mu.Unlock()
	for _, entry := range entries {
		if entry.TurnSeq != turnSeq || strings.TrimSpace(entry.Kind) != "metadata" {
			continue
		}
		var payload metadataPayload
		if json.Unmarshal(entry.Payload, &payload) != nil {
			continue
		}
		switch strings.TrimSpace(payload.Type) {
		case "shell_rejection_fingerprint":
			fingerprint := strings.TrimSpace(fmt.Sprint(payload.Value["fingerprint"]))
			if fingerprint != "" && fingerprint != "<nil>" {
				state.FingerprintHits[fingerprint]++
			}
			if strings.TrimSpace(fmt.Sprint(payload.Value["rejection_class"])) == "command_parse" {
				state.ParseRejections++
			}
		case "shell_circuit_parse_reset":
			state.ParseRejections = 0
		case "shell_circuit_fingerprint_reset":
			commandHash := strings.TrimSpace(fmt.Sprint(payload.Value["command_hash"]))
			cwdHash := strings.TrimSpace(fmt.Sprint(payload.Value["cwd_hash"]))
			for _, class := range shellCircuitFingerprintClasses {
				delete(state.FingerprintHits, shellCircuitFingerprint(commandHash, cwdHash, class))
			}
		case "shell_circuit_open":
			state.Open = true
			state.RejectionClass = strings.TrimSpace(fmt.Sprint(payload.Value["rejection_class"]))
			state.Reason = strings.TrimSpace(fmt.Sprint(payload.Value["rejected_reason"]))
		case "shell_circuit_local_block":
			state.LocalBlocks++
		}
	}
	return state
}

func (service *Service) recordShellCircuitLocalBlock(stream *ActiveStream, invocation runtimecore.ToolInvocation, circuit shellCircuitState) error {
	if service == nil || stream == nil {
		return nil
	}
	commandHash, argsHash, cwdHash := shellInvocationHashes(invocation.ArgsJSON)
	values := map[string]any{
		"source":          "pre_dispatch",
		"provider_pass":   currentProviderPass(stream),
		"model_call_id":   strings.TrimSpace(invocation.ModelCallID),
		"tool_call_id":    strings.TrimSpace(invocation.CallID),
		"rejection_class": circuit.RejectionClass,
		"rejected_reason": circuit.Reason,
		"command_hash":    commandHash,
		"args_hash":       argsHash,
		"cwd_hash":        cwdHash,
		"block_count":     circuit.LocalBlocks + 1,
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "shell_circuit_local_block", values)
	}
	_, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_circuit_local_block", values),
	})
	return err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}