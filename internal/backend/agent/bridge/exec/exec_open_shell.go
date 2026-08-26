// exec_open_shell.go 承载 Shell 工具域：Shell/WriteShellStdin/ForceBackgroundShell 的 args 解码与 open 请求构造。
package execbridge

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func decodeShellArgs(raw []byte) (shellResultArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return shellResultArgs{}, err
	}
	result := shellResultArgs{
		Command:          strings.TrimSpace(readStringArg(args, "command")),
		Description:      strings.TrimSpace(readStringArg(args, "description")),
		WorkingDirectory: strings.TrimSpace(readStringArg(args, "working_directory", "workingDirectory")),
	}
	if result.Profile, err = normalizeShellProfile(readStringArg(args, "profile")); err != nil {
		return result, err
	}
	if result.Command == "" {
		return result, fmt.Errorf("Shell command is required")
	}
	result.RequestedSandboxPolicy = decodeShellRequestedSandboxPolicy(args)
	if blockUntilMS, found, err := runtimecore.ReadFloat64Arg(args, "block_until_ms", "blockUntilMS"); err != nil {
		return result, err
	} else if found {
		result.BlockUntilMS = blockUntilMS
		result.BlockUntilMSSet = true
	}
	notifyOnOutput, err := decodeShellOutputNotificationArgs(args)
	if err != nil {
		return result, err
	}
	result.NotifyOnOutput = notifyOnOutput
	return result, nil
}

func decodeShellRequestedSandboxPolicy(args map[string]any) *agentv1.SandboxPolicy {
	raw, ok := args["required_permissions"]
	if !ok || raw == nil {
		return nil
	}
	permissions, ok := raw.([]any)
	if !ok || len(permissions) == 0 {
		return nil
	}
	fullNetwork := false
	for _, permission := range permissions {
		name, ok := permission.(string)
		if !ok {
			continue
		}
		switch name {
		case "all":
			return &agentv1.SandboxPolicy{
				Type:          agentv1.SandboxPolicy_TYPE_INSECURE_NONE,
				NetworkAccess: boolPtr(true),
			}
		case "full_network":
			fullNetwork = true
		}
	}
	if !fullNetwork {
		return nil
	}
	return &agentv1.SandboxPolicy{
		Type:          agentv1.SandboxPolicy_TYPE_WORKSPACE_READWRITE,
		NetworkAccess: boolPtr(true),
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func decodeShellOutputNotificationArgs(args map[string]any) (*shellOutputNotificationArgs, error) {
	raw, ok := args["notify_on_output"]
	if !ok || raw == nil {
		raw, ok = args["notifyOnOutput"]
	}
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("notify_on_output must be an object")
	}
	pattern := strings.TrimSpace(readStringArg(items, "pattern"))
	reason := strings.TrimSpace(readStringArg(items, "reason"))
	if pattern == "" || reason == "" {
		return nil, nil
	}
	result := &shellOutputNotificationArgs{Pattern: pattern, Reason: reason}
	if debounceMS, found, err := runtimecore.ReadFloat64Arg(items, "debounce_ms", "debounceMs"); err != nil {
		return nil, err
	} else if found {
		result.DebounceMS = &debounceMS
	}
	if limit, found, err := runtimecore.ReadInt32Arg(items, "notification_limit", "notificationLimit"); err != nil {
		return nil, err
	} else if found {
		result.NotificationLimit = &limit
	}
	return result, nil
}

func buildShellOutputNotificationConfig(input *shellOutputNotificationArgs) *agentv1.ShellOutputNotificationConfig {
	if input == nil {
		return nil
	}
	pattern := strings.TrimSpace(input.Pattern)
	reason := strings.TrimSpace(input.Reason)
	if pattern == "" || reason == "" {
		return nil
	}
	var debounce *float64
	if input.DebounceMS != nil {
		value := *input.DebounceMS / 1000
		if value < 5 {
			value = 5
		}
		debounce = &value
	}
	return &agentv1.ShellOutputNotificationConfig{
		Pattern:           pattern,
		Reason:            reason,
		Debounce:          debounce,
		NotificationLimit: input.NotificationLimit,
	}
}

// openShell 构造 Shell 对应的流式执行桥请求。
func (bridge *Bridge) openShell(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	args, err := decodeShellArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode Shell args failed: %w", err)
	}
	timeout := shellTimeoutFromArgs(args)
	effectiveCommand := args.Command
	if args.Profile != "auto" {
		effectiveCommand, err = buildExplicitShellProfileCommand(args.Profile, args.Command)
		if err != nil {
			return nil, runtimecore.PendingExec{}, err
		}
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-shell-%d", time.Now().UnixNano())
	// 新版协议 output_notification 字段是原始 bytes（客户端按 ShellOutputNotificationConfig
	// proto 反序列化）；旧协议是结构化 message。构造时统一 proto.Marshal 成 bytes。
	var outputNotification []byte
	if config := buildShellOutputNotificationConfig(args.NotifyOnOutput); config != nil {
		outputNotification, err = proto.Marshal(config)
		if err != nil {
			return nil, runtimecore.PendingExec{}, fmt.Errorf("marshal shell output notification failed: %w", err)
		}
	}
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ShellStreamArgs{
					ShellStreamArgs: &agentv1.ShellArgs{
						Command:                  effectiveCommand,
						WorkingDirectory:         args.WorkingDirectory,
						Timeout:                  timeout,
						ToolCallId:               toolCall.CallID,
						SimpleCommands:           buildSimpleShellCommands(effectiveCommand),
						ParsingResult:            buildShellParsingResultProto(effectiveCommand),
						FileOutputThresholdBytes: uint64Ptr(40000),
						TimeoutBehavior:          agentv1.TimeoutBehavior_TIMEOUT_BEHAVIOR_BACKGROUND,
						HardTimeout:              int32Ptr(86400000),
						Description:              stringPtr(args.Description),
						OutputNotification:       outputNotification,
						RequestedSandboxPolicy:   args.RequestedSandboxPolicy,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "shell",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

type writeShellStdinArgs struct {
	ShellID uint32
	Chars   string
}

func decodeWriteShellStdinArgs(raw []byte) (writeShellStdinArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return writeShellStdinArgs{}, err
	}
	shellID, found, err := runtimecore.ReadUint32Arg(args, "shell_id", "shellId")
	if err != nil {
		return writeShellStdinArgs{}, err
	}
	if !found || shellID == 0 {
		return writeShellStdinArgs{}, fmt.Errorf("WriteShellStdin shell_id is required")
	}
	rawChars, charsFound := args["chars"]
	if !charsFound || rawChars == nil {
		return writeShellStdinArgs{}, fmt.Errorf("WriteShellStdin chars is required")
	}
	chars, ok := rawChars.(string)
	if !ok {
		return writeShellStdinArgs{}, fmt.Errorf("WriteShellStdin chars must be a string")
	}
	return writeShellStdinArgs{ShellID: shellID, Chars: chars}, nil
}

func (bridge *Bridge) openWriteShellStdin(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	args, err := decodeWriteShellStdinArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode WriteShellStdin args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-write-shell-stdin-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_WriteShellStdinArgs{
					WriteShellStdinArgs: &agentv1.WriteShellStdinArgs{
						ShellId: args.ShellID,
						Chars:   args.Chars,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "write_shell_stdin",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}

type forceBackgroundShellArgs struct {
	ToolCallID string
}

func decodeForceBackgroundShellArgs(raw []byte) (forceBackgroundShellArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return forceBackgroundShellArgs{}, err
	}
	toolCallID := strings.TrimSpace(readStringArg(args, "tool_call_id", "toolCallId"))
	if toolCallID == "" {
		return forceBackgroundShellArgs{}, fmt.Errorf("ForceBackgroundShell tool_call_id is required")
	}
	return forceBackgroundShellArgs{ToolCallID: toolCallID}, nil
}

func (bridge *Bridge) openForceBackgroundShell(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	args, err := decodeForceBackgroundShellArgs(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("decode ForceBackgroundShell args failed: %w", err)
	}
	messageID := bridge.nextID()
	execID := fmt.Sprintf("exec-force-background-shell-%d", time.Now().UnixNano())
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentv1.ExecServerMessage{
				Id:     messageID,
				ExecId: execID,
				Message: &agentv1.ExecServerMessage_ForceBackgroundShellArgs{
					ForceBackgroundShellArgs: &agentv1.ForceBackgroundShellArgs{
						ToolCallId: args.ToolCallID,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingExec{
		MessageID:   messageID,
		ExecID:      execID,
		ArgsJSON:    append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:  toolCall.CallID,
		ExecKind:    "force_background_shell",
		StreamState: "opened",
		OpenedAt:    time.Now().UTC(),
	}, nil
}
