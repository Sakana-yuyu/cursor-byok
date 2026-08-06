// exec_build_toolcall.go 承载完成态 ToolCall 构建：各工具 buildXxxCompletedToolCall 与 shell 收尾载荷。
package execbridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"


	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func buildReadCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.ReadResult) *agentv1.ToolCall {
	args, err := DecodeReadToolArgs(argsJSON)
	if err != nil || args == nil {
		args = &agentv1.ReadToolArgs{}
	}
	if strings.TrimSpace(args.GetPath()) == "" && result != nil && result.GetSuccess() != nil {
		args.Path = result.GetSuccess().GetPath()
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ReadToolCall{
			ReadToolCall: &agentv1.ReadToolCall{
				Args:   args,
				Result: convertReadResultToReadToolResult(result),
			},
		},
	}
}

// buildDeleteCompletedToolCall 构造 Delete 对应的完成态 ToolCall。
func buildDeleteCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.DeleteResult) *agentv1.ToolCall {
	var args agentv1.DeleteArgs
	_ = json.Unmarshal(argsJSON, &args)
	args.ToolCallId = toolCallID
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_DeleteToolCall{
			DeleteToolCall: &agentv1.DeleteToolCall{
				Args:   &args,
				Result: result,
			},
		},
	}
}

// buildGlobCompletedToolCall 构造 Glob 对应的完成态 ToolCall。
func buildGlobCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.GrepResult) *agentv1.ToolCall {
	args, _ := decodeArgsMap(argsJSON)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_GlobToolCall{
			GlobToolCall: &agentv1.GlobToolCall{
				Args:   buildGlobToolArgs(args),
				Result: convertGrepResultToGlobToolResult(result, args),
			},
		},
	}
}

const maxGlobReplayFiles = 200

const (
	replayKiB = 1024

	readReplayContentLimit     = 64 * replayKiB
	readReplayLineLimit        = 0
	readReplayBinaryLimit      = 32 * replayKiB
	shellReplayStreamLimit     = 16 * replayKiB
	grepReplayContentLimit     = 32 * replayKiB
	grepReplayMatchLimit       = 2 * replayKiB
	grepReplayMatchesPerFile   = 100
	grepReplayTotalMatches     = 300
	grepReplayListLimit        = 300
	mcpReplayTextTotalLimit    = 32 * replayKiB
	mcpReplayTextItemLimit     = 32 * replayKiB
	mcpReplayContentItemLimit  = 20
	mcpReplayStructuredLimit   = 32 * replayKiB
	mcpReplayBinaryLimit       = 32 * replayKiB
	mcpResourcesReplayLimit    = 32 * replayKiB
	mcpResourcesReplayCount    = 200
	mcpResourceDescriptionSize = replayKiB
)

func truncateReplayText(toolName string, text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	original := len(text)
	notice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; showing %d of %d bytes]", toolName, limit, limit, original)
	for {
		keep := limit - len(notice)
		if keep <= 0 {
			return truncateUTF8Bytes(text, limit)
		}
		kept := truncateUTF8Bytes(text, keep)
		nextNotice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; showing %d of %d bytes]", toolName, limit, len(kept), original)
		output := strings.TrimRight(kept, "\n") + nextNotice
		if len(output) <= limit || nextNotice == notice {
			return output
		}
		notice = nextNotice
	}
}

func truncateReplayTextMiddle(toolName string, text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	original := len(text)
	notice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; omitted middle; showing %d of %d bytes]\n\n", toolName, limit, limit, original)
	for {
		keep := limit - len(notice)
		if keep <= 0 {
			return truncateUTF8Bytes(text, limit)
		}
		headLimit := keep / 2
		tailLimit := keep - headLimit
		head := truncateUTF8Bytes(text, headLimit)
		tail := truncateUTF8Suffix(text, tailLimit)
		kept := len(head) + len(tail)
		nextNotice := fmt.Sprintf("\n\n[truncated: %s result exceeded %d bytes; omitted middle; showing %d of %d bytes]\n\n", toolName, limit, kept, original)
		output := head + nextNotice + tail
		if len(output) <= limit || nextNotice == notice {
			return output
		}
		notice = nextNotice
	}
}

func truncateReplayLine(toolName string, text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	original := len(text)
	notice := fmt.Sprintf(" [truncated: %s line exceeded %d bytes; showing %d of %d bytes]", toolName, limit, limit, original)
	for {
		keep := limit - len(notice)
		if keep <= 0 {
			return truncateUTF8Bytes(text, limit)
		}
		kept := truncateUTF8Bytes(text, keep)
		nextNotice := fmt.Sprintf(" [truncated: %s line exceeded %d bytes; showing %d of %d bytes]", toolName, limit, len(kept), original)
		output := kept + nextNotice
		if len(output) <= limit || nextNotice == notice {
			return output
		}
		notice = nextNotice
	}
}

func truncateReplayLines(toolName string, text string, lineLimit int) string {
	if lineLimit <= 0 || text == "" {
		return text
	}
	parts := strings.SplitAfter(text, "\n")
	for index, part := range parts {
		newline := ""
		body := part
		if strings.HasSuffix(part, "\n") {
			body = strings.TrimSuffix(part, "\n")
			newline = "\n"
		}
		parts[index] = truncateReplayLine(toolName, body, lineLimit) + newline
	}
	return strings.Join(parts, "")
}

func truncateUTF8Bytes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if limit > len(text) {
		limit = len(text)
	}
	truncated := text[:limit]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func truncateUTF8Suffix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	if start < 0 {
		start = 0
	}
	suffix := text[start:]
	for !utf8.ValidString(suffix) && start < len(text) {
		start++
		suffix = text[start:]
	}
	return suffix
}

func truncateByteSlice(value []byte, limit int) ([]byte, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return append([]byte(nil), value[:limit]...), true
}

func replayTruncationNotice(toolName string, limit int, kept int, original int) string {
	return fmt.Sprintf("[truncated: %s result exceeded %d bytes; showing %d of %d bytes]", toolName, limit, kept, original)
}

// buildWriteCompletedToolCall 构造 Write 对应的完成态 ToolCall。
func buildWriteCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.WriteResult) *agentv1.ToolCall {
	args, _ := decodeArgsMap(argsJSON)
	streamContent := stringPtr(readStringArg(args, "contents", "content", "stream_content", "streamContent"))
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_EditToolCall{
			EditToolCall: &agentv1.EditToolCall{
				Args: &agentv1.EditArgs{
					Path:          strings.TrimSpace(readStringArg(args, "path")),
					StreamContent: streamContent,
				},
				Result: convertWriteResultToEditResult(result),
			},
		},
	}
}

// buildReadLintsCompletedToolCall 构造 ReadLints 对应的完成态 ToolCall。
func buildReadLintsCompletedToolCall(argsJSON []byte, result *agentv1.DiagnosticsResult) *agentv1.ToolCall {
	args, _ := decodeArgsMap(argsJSON)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ReadLintsToolCall{
			ReadLintsToolCall: &agentv1.ReadLintsToolCall{
				Args: &agentv1.ReadLintsToolArgs{
					Paths: readStringSliceArg(args, "paths"),
				},
				Result: convertDiagnosticsResultToReadLintsToolResult(result),
			},
		},
	}
}

// buildTaskCompletedToolCall 构造 Task 对应的完成态 ToolCall。
func buildTaskCompletedToolCall(argsJSON []byte, result *agentv1.SubagentResult) *agentv1.ToolCall {
	args, _ := decodeArgsMap(argsJSON)
	capability, _ := runtimecore.ResolveTaskSubagentCapabilityFromArgs(args)
	taskArgs := &agentv1.TaskArgs{
		Description:  strings.TrimSpace(readStringArg(args, "description")),
		Prompt:       strings.TrimSpace(readStringArg(args, "prompt")),
		SubagentType: subagentTypeProtoFromString(strings.TrimSpace(readStringArg(args, "subagent_type", "subagentType"))),
		Model:        stringPtr(strings.TrimSpace(readStringArg(args, "model"))),
		Resume:       stringPtr(strings.TrimSpace(readStringArg(args, "resume"))),
		Attachments:  readStringSliceArg(args, "attachments"),
		Mode:         taskModeFromReadonly(capability.Readonly),
	}
	if agentID := strings.TrimSpace(readStringArg(args, "agentId", "agent_id")); agentID != "" {
		taskArgs.AgentId = &agentID
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_TaskToolCall{
			TaskToolCall: &agentv1.TaskToolCall{
				Args:   taskArgs,
				Result: convertSubagentResultToTaskResult(result),
			},
		},
	}
}

func taskModeFromReadonly(readonly bool) agentv1.TaskMode {
	if readonly {
		return agentv1.TaskMode_TASK_MODE_PLAN
	}
	return agentv1.TaskMode_TASK_MODE_AGENT
}

func subagentTypeProtoFromString(raw string) *agentv1.SubagentType {
	switch strings.TrimSpace(raw) {
	case "explore":
		return &agentv1.SubagentType{Type: &agentv1.SubagentType_Explore{Explore: &agentv1.SubagentTypeExplore{}}}
	case "browser-use", "browserUse":
		return &agentv1.SubagentType{Type: &agentv1.SubagentType_BrowserUse{BrowserUse: &agentv1.SubagentTypeBrowserUse{}}}
	case "shell":
		return &agentv1.SubagentType{Type: &agentv1.SubagentType_Shell{Shell: &agentv1.SubagentTypeShell{}}}
	case "":
		return &agentv1.SubagentType{Type: &agentv1.SubagentType_Unspecified{Unspecified: &agentv1.SubagentTypeUnspecified{}}}
	default:
		return &agentv1.SubagentType{
			Type: &agentv1.SubagentType_Custom{
				Custom: &agentv1.SubagentTypeCustom{Name: strings.TrimSpace(raw)},
			},
		}
	}
}

// buildShellCompletedToolCall 构造 Shell 对应的完成态 ToolCall。
func buildShellCompletedToolCall(toolCallID string, argsJSON []byte, stdout string, stderr string, exit *agentv1.ShellStreamExit) *agentv1.ToolCall {
	args := decodeShellArgsForResult(argsJSON)
	shellArgs := &agentv1.ShellArgs{
		Command:          args.Command,
		WorkingDirectory: args.WorkingDirectory,
		Timeout:          shellTimeoutFromArgs(args),
		ToolCallId:       toolCallID,
		Description:      stringPtr(strings.TrimSpace(args.Description)),
	}
	successPayload := buildShellSuccessPayload(args, stdout, stderr, exit)
	isBackground := false
	result := &agentv1.ShellResult{
		IsBackground: &isBackground,
		Result: &agentv1.ShellResult_Success{
			Success: successPayload,
		},
	}
	if exit != nil && exit.GetCode() != 0 {
		failure := &agentv1.ShellFailure{
			Command:           args.Command,
			WorkingDirectory:  args.WorkingDirectory,
			ExitCode:          int32(exit.GetCode()),
			Stdout:            stdout,
			Stderr:            stderr,
			InterleavedOutput: buildShellInterleavedOutput(stdout, stderr),
			Aborted:           exit.GetAborted(),
		}
		if exit.LocalExecutionTimeMs != nil {
			failure.LocalExecutionTimeMs = int32Ptr(exit.GetLocalExecutionTimeMs())
		}
		if exit.AbortReason != nil {
			failure.AbortReason = shellAbortReasonPtr(exit.GetAbortReason())
		}
		if exit.GetOutputLocation() != nil {
			failure.OutputLocation = exit.GetOutputLocation()
		}
		result.Result = &agentv1.ShellResult_Failure{
			Failure: failure,
		}
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ShellToolCall{
			ShellToolCall: &agentv1.ShellToolCall{
				Args:        shellArgs,
				Result:      result,
				Description: stringPtr(strings.TrimSpace(args.Description)),
			},
		},
	}
}

// buildShellBackgroundedToolCall 构造 Shell 被转入后台时的完成态 ToolCall。
func buildShellBackgroundedToolCall(toolCallID string, argsJSON []byte, backgrounded *agentv1.ShellStreamBackgrounded) *agentv1.ToolCall {
	args := decodeShellArgsForResult(argsJSON)
	shellArgs := &agentv1.ShellArgs{
		Command:          args.Command,
		WorkingDirectory: args.WorkingDirectory,
		Timeout:          shellTimeoutFromArgs(args),
		ToolCallId:       toolCallID,
		Description:      stringPtr(strings.TrimSpace(args.Description)),
	}
	successPayload := &agentv1.ShellSuccess{
		Command:           strings.TrimSpace(args.Command),
		WorkingDirectory:  strings.TrimSpace(args.WorkingDirectory),
		ExitCode:          0,
		ShellId:           uint32Ptr(backgrounded.GetShellId()),
		InterleavedOutput: stringPtr(""),
	}
	if workingDirectory := strings.TrimSpace(backgrounded.GetWorkingDirectory()); workingDirectory != "" {
		successPayload.WorkingDirectory = workingDirectory
	}
	if backgrounded.GetPid() != 0 {
		successPayload.Pid = uint32Ptr(backgrounded.GetPid())
	}
	if backgrounded.MsToWait != nil {
		successPayload.MsToWait = int32Ptr(backgrounded.GetMsToWait())
	}
	isBackground := true
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ShellToolCall{
			ShellToolCall: &agentv1.ShellToolCall{
				Args: shellArgs,
				Result: &agentv1.ShellResult{
					IsBackground: &isBackground,
					Pid:          uint32Ptr(backgrounded.GetPid()),
					Result: &agentv1.ShellResult_Success{
						Success: successPayload,
					},
				},
				Description: stringPtr(strings.TrimSpace(args.Description)),
			},
		},
	}
}

// buildShellSuccessPayload 构造 shell 终态结果。
func buildShellSuccessPayload(args shellResultArgs, stdout string, stderr string, exit *agentv1.ShellStreamExit) *agentv1.ShellSuccess {
	stdout, stderr = truncateShellStreamsForReplay(stdout, stderr)
	payload := &agentv1.ShellSuccess{
		Command:           strings.TrimSpace(args.Command),
		WorkingDirectory:  strings.TrimSpace(args.WorkingDirectory),
		Stdout:            stdout,
		Stderr:            stderr,
		InterleavedOutput: buildShellInterleavedOutput(stdout, stderr),
	}
	if exit != nil {
		payload.ExitCode = int32(exit.GetCode())
		if cwd := strings.TrimSpace(exit.GetCwd()); cwd != "" {
			payload.WorkingDirectory = cwd
		}
		if exit.GetOutputLocation() != nil {
			payload.OutputLocation = exit.GetOutputLocation()
		}
		if exit.LocalExecutionTimeMs != nil {
			duration := int32(exit.GetLocalExecutionTimeMs())
			payload.ExecutionTime = duration
			payload.LocalExecutionTimeMs = &duration
		}
	}
	return payload
}

func buildShellInterleavedOutput(stdout string, stderr string) *string {
	combinedLimit := shellReplayStreamLimit * 2
	switch {
	case stdout == "" && stderr == "":
		return nil
	case stdout == "":
		return stringPtr(truncateReplayTextMiddle("Shell interleaved output", stderr, combinedLimit))
	case stderr == "":
		return stringPtr(truncateReplayTextMiddle("Shell interleaved output", stdout, combinedLimit))
	default:
		combined := stdout
		if !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += stderr
		combined = truncateReplayTextMiddle("Shell interleaved output", combined, combinedLimit)
		return &combined
	}
}

func truncateShellStreamsForReplay(stdout string, stderr string) (string, string) {
	return truncateReplayTextMiddle("Shell stdout", stdout, shellReplayStreamLimit),
		truncateReplayTextMiddle("Shell stderr", stderr, shellReplayStreamLimit)
}

// buildShellRejectedToolCall 构造 Shell 被拒绝时的完成态 ToolCall。
func buildShellRejectedToolCall(toolCallID string, argsJSON []byte, rejected *agentv1.ShellRejected) *agentv1.ToolCall {
	args := decodeShellArgsForResult(argsJSON)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ShellToolCall{
			ShellToolCall: &agentv1.ShellToolCall{
				Args: &agentv1.ShellArgs{
					Command:          args.Command,
					WorkingDirectory: args.WorkingDirectory,
					Timeout:          shellTimeoutFromArgs(args),
					ToolCallId:       toolCallID,
					Description:      stringPtr(strings.TrimSpace(args.Description)),
				},
				Result: &agentv1.ShellResult{
					Result: &agentv1.ShellResult_Rejected{
						Rejected: rejected,
					},
				},
				Description: stringPtr(strings.TrimSpace(args.Description)),
			},
		},
	}
}

// buildShellPermissionDeniedToolCall 构造 Shell 权限拒绝时的完成态 ToolCall。
func buildShellPermissionDeniedToolCall(toolCallID string, argsJSON []byte, denied *agentv1.ShellPermissionDenied) *agentv1.ToolCall {
	args := decodeShellArgsForResult(argsJSON)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ShellToolCall{
			ShellToolCall: &agentv1.ShellToolCall{
				Args: &agentv1.ShellArgs{
					Command:          args.Command,
					WorkingDirectory: args.WorkingDirectory,
					Timeout:          shellTimeoutFromArgs(args),
					ToolCallId:       toolCallID,
					Description:      stringPtr(strings.TrimSpace(args.Description)),
				},
				Result: &agentv1.ShellResult{
					Result: &agentv1.ShellResult_PermissionDenied{
						PermissionDenied: denied,
					},
				},
				Description: stringPtr(strings.TrimSpace(args.Description)),
			},
		},
	}
}

// buildSimpleShellCommands 生成最小 simple_commands 列表。
func buildSimpleShellCommands(command string) []string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	return []string{trimmed}
}

// buildShellParsingResultProto 生成最小 shell parsing_result。
func buildShellParsingResultProto(command string) *agentv1.ShellCommandParsingResult {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return nil
	}
	args := make([]*agentv1.ShellCommandParsingResult_ExecutableCommandArg, 0, len(parts)-1)
	for _, part := range parts[1:] {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		args = append(args, &agentv1.ShellCommandParsingResult_ExecutableCommandArg{
			Type:  "word",
			Value: value,
		})
	}
	return &agentv1.ShellCommandParsingResult{
		ExecutableCommands: []*agentv1.ShellCommandParsingResult_ExecutableCommand{
			{
				Name:     strings.TrimSpace(parts[0]),
				Args:     args,
				FullText: trimmed,
			},
		},
	}
}

// summarizeShellTerminalPayload 返回 shell 对模型可消费的终态结果文本。
func summarizeShellTerminalPayload(stdout string, stderr string, exit *agentv1.ShellStreamExit, closedWithoutExit bool) string {
	trimmedStdout := strings.TrimSpace(stdout)
	trimmedStderr := strings.TrimSpace(stderr)
	sections := make([]string, 0, 3)
	if trimmedStdout != "" {
		sections = append(sections, trimmedStdout)
	}
	if trimmedStderr != "" {
		if trimmedStdout != "" {
			sections = append(sections, "<stderr>\n"+trimmedStderr+"\n</stderr>")
		} else {
			sections = append(sections, trimmedStderr)
		}
	}
	if len(sections) > 0 {
		return strings.Join(sections, "\n\n")
	}
	if exit != nil {
		return fmt.Sprintf("shell exited with code=%d cwd=%s", exit.GetCode(), strings.TrimSpace(exit.GetCwd()))
	}
	if closedWithoutExit {
		return "shell stream closed without captured output"
	}
	return "shell completed without captured output"
}
