// exec_tool_result.go 承载工具结果转换与截断：convert/truncate grep 系列与剩余 build 函数。
package execbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

type shellResultArgs struct {
	Command                string                       `json:"command"`
	Description            string                       `json:"description,omitempty"`
	WorkingDirectory       string                       `json:"working_directory,omitempty"`
	Profile                string                       `json:"profile,omitempty"`
	BlockUntilMS           float64                      `json:"block_until_ms,omitempty"`
	BlockUntilMSSet        bool                         `json:"-"`
	NotifyOnOutput         *shellOutputNotificationArgs `json:"notify_on_output,omitempty"`
	RequestedSandboxPolicy *agentv1.SandboxPolicy       `json:"-"`
}

type shellOutputNotificationArgs struct {
	Pattern           string
	Reason            string
	DebounceMS        *float64
	NotificationLimit *int32
}

// decodeShellArgsForResult 解码 shell 参数，供完成态 ToolCall 复用。
func decodeShellArgsForResult(argsJSON []byte) shellResultArgs {
	args, err := decodeShellArgs(argsJSON)
	if err != nil {
		argsMap, _ := decodeArgsMap(argsJSON)
		args.Command = strings.TrimSpace(readStringArg(argsMap, "command"))
		args.Description = strings.TrimSpace(readStringArg(argsMap, "description"))
		args.WorkingDirectory = strings.TrimSpace(readStringArg(argsMap, "working_directory", "workingDirectory"))
		if blockUntilMS, found, err := runtimecore.ReadFloat64Arg(argsMap, "block_until_ms", "blockUntilMS"); err == nil && found {
			args.BlockUntilMS = blockUntilMS
			args.BlockUntilMSSet = true
		}
	}
	return args
}

// shellTimeoutFromArgs 把工具 JSON 中的 block_until_ms 映射回 proto timeout。
func shellTimeoutFromArgs(args shellResultArgs) int32 {
	if !args.BlockUntilMSSet {
		return 30000
	}
	if args.BlockUntilMS <= 0 {
		return 0
	}
	return int32(args.BlockUntilMS)
}

// buildWriteShellStdinCompletedToolCall 构造 WriteShellStdin 对应的完成态 ToolCall。
func buildWriteShellStdinCompletedToolCall(argsJSON []byte, result *agentv1.WriteShellStdinResult) *agentv1.ToolCall {
	args, err := decodeWriteShellStdinArgs(argsJSON)
	if err != nil {
		args = writeShellStdinArgs{ShellID: 0, Chars: ""}
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_WriteShellStdinToolCall{
			WriteShellStdinToolCall: &agentv1.WriteShellStdinToolCall{
				Args: &agentv1.WriteShellStdinArgs{
					ShellId: args.ShellID,
					Chars:   args.Chars,
				},
				Result: result,
			},
		},
	}
}

func summarizeWriteShellStdinResult(result *agentv1.WriteShellStdinResult) string {
	if result == nil {
		return ""
	}
	switch item := result.GetResult().(type) {
	case *agentv1.WriteShellStdinResult_Success:
		if item.Success == nil {
			return "write shell stdin succeeded"
		}
		return fmt.Sprintf(
			"wrote input to shell %d (terminal file length before input: %d)",
			item.Success.GetShellId(),
			item.Success.GetTerminalFileLengthBeforeInputWritten(),
		)
	case *agentv1.WriteShellStdinResult_Error:
		if item.Error == nil {
			return "write shell stdin failed"
		}
		return fmt.Sprintf("write shell stdin failed: %s", strings.TrimSpace(item.Error.GetError()))
	default:
		return "write shell stdin completed"
	}
}

func summarizeForceBackgroundShellResult(result *agentv1.ForceBackgroundShellResult) string {
	if result == nil {
		return ""
	}
	switch result.GetStatus() {
	case agentv1.ForceBackgroundShellStatus_FORCE_BACKGROUND_SHELL_STATUS_ACCEPTED:
		return "force background shell accepted"
	case agentv1.ForceBackgroundShellStatus_FORCE_BACKGROUND_SHELL_STATUS_NOT_FOUND:
		return "force background shell target not found"
	default:
		return "force background shell completed"
	}
}

// buildGrepCompletedToolCall 构造 Grep 对应的完成态 ToolCall。
func buildGrepCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.GrepResult) *agentv1.ToolCall {
	args, err := DecodeGrepToolArgs(argsJSON, toolCallID)
	if err != nil && args == nil {
		args = &agentv1.GrepArgs{ToolCallId: toolCallID}
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_GrepToolCall{
			GrepToolCall: &agentv1.GrepToolCall{
				Args:   args,
				Result: result,
			},
		},
	}
}

// buildLsCompletedToolCall 构造 Ls 对应的完成态 ToolCall。
func buildLsCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.LsResult) *agentv1.ToolCall {
	var input struct {
		Path   string   `json:"path"`
		Ignore []string `json:"ignore,omitempty"`
	}
	_ = json.Unmarshal(argsJSON, &input)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_LsToolCall{
			LsToolCall: &agentv1.LsToolCall{
				Args: &agentv1.LsArgs{
					Path:       strings.TrimSpace(input.Path),
					Ignore:     append([]string(nil), input.Ignore...),
					ToolCallId: toolCallID,
				},
				Result: result,
			},
		},
	}
}

// buildMcpCompletedToolCall 构造 CallMcpTool 对应的完成态 ToolCall。
func buildMcpCompletedToolCall(toolCallID string, argsJSON []byte, result *agentv1.McpToolResult) *agentv1.ToolCall {
	input, _ := runtimecore.DecodeMCPToolPayload(argsJSON)
	serverIdentifier := strings.TrimSpace(input.Server)
	if serverIdentifier == "" {
		serverIdentifier = strings.TrimSpace(input.ProviderIdentifier)
	}
	toolName := strings.TrimSpace(input.ToolName)
	if toolName == "" {
		toolName = runtimecore.InferMCPToolName(serverIdentifier, input.Name)
	}
	if serverIdentifier == "" && strings.TrimSpace(input.Name) != "" {
		serverIdentifier = runtimecore.InferMCPServerIdentifier(input.Name)
	}
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_McpToolCall{
			McpToolCall: &agentv1.McpToolCall{
				Args: &agentv1.McpArgs{
					Name:               canonicalMCPToolLookupName(serverIdentifier, toolName),
					Args:               buildStructValueMap(input.Arguments),
					ToolCallId:         toolCallID,
					ProviderIdentifier: serverIdentifier,
					ToolName:           toolName,
				},
				Result: result,
			},
		},
	}
}

func canonicalMCPToolLookupName(server string, toolName string) string {
	trimmedServer := strings.TrimSpace(server)
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return ""
	}
	if trimmedServer == "" {
		return trimmedToolName
	}
	return trimmedServer + "-" + trimmedToolName
}

// buildListMcpResourcesCompletedToolCall 构造 ListMcpResources 对应的完成态 ToolCall。
func buildListMcpResourcesCompletedToolCall(argsJSON []byte, result *agentv1.ListMcpResourcesExecResult) *agentv1.ToolCall {
	var input struct {
		Server string `json:"server,omitempty"`
	}
	_ = json.Unmarshal(argsJSON, &input)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ListMcpResourcesToolCall{
			ListMcpResourcesToolCall: &agentv1.ListMcpResourcesToolCall{
				Args: &agentv1.ListMcpResourcesExecArgs{
					Server: stringPtr(strings.TrimSpace(input.Server)),
				},
				Result: result,
			},
		},
	}
}

// buildReadMcpResourceCompletedToolCall 构造 FetchMcpResource 对应的完成态 ToolCall。
func buildReadMcpResourceCompletedToolCall(argsJSON []byte, result *agentv1.ReadMcpResourceExecResult) *agentv1.ToolCall {
	var input struct {
		Server       string `json:"server"`
		URI          string `json:"uri"`
		DownloadPath string `json:"downloadPath,omitempty"`
	}
	_ = json.Unmarshal(argsJSON, &input)
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ReadMcpResourceToolCall{
			ReadMcpResourceToolCall: &agentv1.ReadMcpResourceToolCall{
				Args: &agentv1.ReadMcpResourceExecArgs{
					Server:       strings.TrimSpace(input.Server),
					Uri:          strings.TrimSpace(input.URI),
					DownloadPath: stringPtr(strings.TrimSpace(input.DownloadPath)),
				},
				Result: result,
			},
		},
	}
}

// convertReadResultToReadToolResult 把 `ReadResult` 映射为 `ReadToolResult`。
func convertReadResultToReadToolResult(result *agentv1.ReadResult) *agentv1.ReadToolResult {
	if result == nil {
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: "read result missing"},
			},
		}
	}

	switch item := result.GetResult().(type) {
	case *agentv1.ReadResult_Success:
		content := item.Success.GetContent()
		data := item.Success.GetData()
		exceededLimit := item.Success.GetTruncated()
		if content != "" {
			original := content
			content = truncateReplayLines("Read", content, readReplayLineLimit)
			content = truncateReplayText("Read", content, readReplayContentLimit)
			if content != original {
				exceededLimit = true
			}
		}
		toolSuccess := &agentv1.ReadToolSuccess{
			IsEmpty:       strings.TrimSpace(item.Success.GetContent()) == "" && len(item.Success.GetData()) == 0,
			ExceededLimit: exceededLimit,
			TotalLines:    uint32(item.Success.GetTotalLines()),
			FileSize:      uint32(item.Success.GetFileSize()),
			Path:          item.Success.GetPath(),
		}
		if content != "" {
			toolSuccess.Output = &agentv1.ReadToolSuccess_Content{Content: content}
		} else if len(data) > 0 {
			if len(data) > readReplayBinaryLimit {
				toolSuccess.ExceededLimit = true
				toolSuccess.Output = &agentv1.ReadToolSuccess_Content{
					Content: replayTruncationNotice("Read binary data", readReplayBinaryLimit, 0, len(data)),
				}
			} else {
				toolSuccess.Output = &agentv1.ReadToolSuccess_Data{Data: append([]byte(nil), data...)}
			}
		}
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Success{
				Success: toolSuccess,
			},
		}
	case *agentv1.ReadResult_Error:
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: item.Error.GetError()},
			},
		}
	case *agentv1.ReadResult_Rejected:
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: item.Rejected.GetReason()},
			},
		}
	case *agentv1.ReadResult_FileNotFound:
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: summarizeReadResult(result)},
			},
		}
	case *agentv1.ReadResult_PermissionDenied:
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: summarizeReadResult(result)},
			},
		}
	case *agentv1.ReadResult_InvalidFile:
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: summarizeReadResult(result)},
			},
		}
	default:
		return &agentv1.ReadToolResult{
			Result: &agentv1.ReadToolResult_Error{
				Error: &agentv1.ReadToolError{ErrorMessage: "unknown read result"},
			},
		}
	}
}

// convertGrepResultToGlobToolResult 把 grep files mode 结果映射为 GlobToolResult。
func convertGrepResultToGlobToolResult(result *agentv1.GrepResult, args map[string]any) *agentv1.GlobToolResult {
	if result == nil {
		return &agentv1.GlobToolResult{
			Result: &agentv1.GlobToolResult_Error{
				Error: &agentv1.GlobToolError{Error: "glob result missing"},
			},
		}
	}
	switch item := result.GetResult().(type) {
	case *agentv1.GrepResult_Success:
		filesResult := firstGrepFilesResult(item.Success)
		if filesResult == nil {
			return &agentv1.GlobToolResult{
				Result: &agentv1.GlobToolResult_Error{
					Error: &agentv1.GlobToolError{Error: "glob files result missing"},
				},
			}
		}
		return &agentv1.GlobToolResult{
			Result: &agentv1.GlobToolResult_Success{
				Success: &agentv1.GlobToolSuccess{
					Pattern:          readGlobPatternArg(args),
					Path:             readGlobTargetDirectoryArg(args),
					Files:            append([]string(nil), filesResult.GetFiles()...),
					TotalFiles:       filesResult.GetTotalFiles(),
					ClientTruncated:  filesResult.GetClientTruncated(),
					RipgrepTruncated: filesResult.GetRipgrepTruncated(),
				},
			},
		}
	case *agentv1.GrepResult_Error:
		return &agentv1.GlobToolResult{
			Result: &agentv1.GlobToolResult_Error{
				Error: &agentv1.GlobToolError{Error: item.Error.GetError()},
			},
		}
	default:
		return &agentv1.GlobToolResult{
			Result: &agentv1.GlobToolResult_Error{
				Error: &agentv1.GlobToolError{Error: "unknown glob result"},
			},
		}
	}
}

func truncateGlobResultForReplay(result *agentv1.GrepResult) *agentv1.GrepResult {
	if result == nil {
		return nil
	}
	cloned, ok := proto.Clone(result).(*agentv1.GrepResult)
	if !ok || cloned == nil || cloned.GetSuccess() == nil {
		return result
	}
	filesResult := firstGrepFilesResult(cloned.GetSuccess())
	if filesResult == nil {
		return cloned
	}
	files := append([]string(nil), filesResult.GetFiles()...)
	totalFiles := int(filesResult.GetTotalFiles())
	if totalFiles <= 0 {
		totalFiles = len(files)
	}
	if len(files) <= maxGlobReplayFiles {
		if filesResult.GetTotalFiles() <= 0 {
			filesResult.TotalFiles = int32(totalFiles)
		}
		return cloned
	}
	filesResult.Files = append([]string(nil), files[:maxGlobReplayFiles]...)
	filesResult.TotalFiles = int32(totalFiles)
	filesResult.ClientTruncated = true
	return cloned
}

func truncateGrepResultForReplay(result *agentv1.GrepResult) *agentv1.GrepResult {
	if result == nil {
		return nil
	}
	cloned, ok := proto.Clone(result).(*agentv1.GrepResult)
	if !ok || cloned == nil || cloned.GetSuccess() == nil {
		return result
	}
	budget := &grepReplayBudget{
		remainingContentBytes: grepReplayContentLimit,
		remainingMatches:      grepReplayTotalMatches,
	}
	success := cloned.GetSuccess()
	for _, union := range success.GetWorkspaceResults() {
		truncateGrepUnionResultForReplay(union, budget)
	}
	truncateGrepUnionResultForReplay(success.GetActiveEditorResult(), budget)
	return cloned
}

type grepReplayBudget struct {
	remainingContentBytes int
	remainingMatches      int
}

func truncateGrepUnionResultForReplay(union *agentv1.GrepUnionResult, budget *grepReplayBudget) {
	if union == nil || budget == nil {
		return
	}
	if content := union.GetContent(); content != nil {
		truncateGrepContentResultForReplay(content, budget)
		return
	}
	if files := union.GetFiles(); files != nil {
		total := int(files.GetTotalFiles())
		if total <= 0 {
			total = len(files.GetFiles())
		}
		if len(files.Files) > grepReplayListLimit {
			files.Files = append([]string(nil), files.Files[:grepReplayListLimit]...)
			files.ClientTruncated = true
		}
		if files.GetTotalFiles() <= 0 {
			files.TotalFiles = int32(total)
		}
		return
	}
	if counts := union.GetCount(); counts != nil {
		totalFiles := int(counts.GetTotalFiles())
		if totalFiles <= 0 {
			totalFiles = len(counts.GetCounts())
		}
		if len(counts.Counts) > grepReplayListLimit {
			counts.Counts = append([]*agentv1.GrepFileCount(nil), counts.Counts[:grepReplayListLimit]...)
			counts.ClientTruncated = true
		}
		if counts.GetTotalFiles() <= 0 {
			counts.TotalFiles = int32(totalFiles)
		}
	}
}

func truncateGrepContentResultForReplay(content *agentv1.GrepContentResult, budget *grepReplayBudget) {
	if content == nil || budget == nil {
		return
	}
	originalBytes := grepContentBytes(content.GetMatches())
	truncated := false
	newFiles := make([]*agentv1.GrepFileMatch, 0, len(content.GetMatches()))
	for _, fileMatch := range content.GetMatches() {
		if fileMatch == nil {
			continue
		}
		if budget.remainingMatches <= 0 || budget.remainingContentBytes <= 0 {
			truncated = true
			break
		}
		nextFile := &agentv1.GrepFileMatch{File: fileMatch.GetFile()}
		perFile := 0
		for _, match := range fileMatch.GetMatches() {
			if match == nil {
				continue
			}
			if perFile >= grepReplayMatchesPerFile || budget.remainingMatches <= 0 || budget.remainingContentBytes <= 0 {
				truncated = true
				break
			}
			nextMatch := proto.Clone(match).(*agentv1.GrepContentMatch)
			originalContent := nextMatch.GetContent()
			nextMatch.Content = truncateReplayText("Grep match", originalContent, grepReplayMatchLimit)
			if nextMatch.Content != originalContent {
				nextMatch.ContentTruncated = true
				truncated = true
			}
			if len(nextMatch.Content) > budget.remainingContentBytes {
				nextMatch.Content = truncateReplayText("Grep", nextMatch.Content, budget.remainingContentBytes)
				nextMatch.ContentTruncated = true
				truncated = true
			}
			if strings.TrimSpace(nextMatch.Content) == "" {
				truncated = true
				break
			}
			budget.remainingContentBytes -= len(nextMatch.Content)
			budget.remainingMatches--
			perFile++
			nextFile.Matches = append(nextFile.Matches, nextMatch)
		}
		if len(nextFile.Matches) > 0 {
			newFiles = append(newFiles, nextFile)
		}
		if len(fileMatch.GetMatches()) > perFile {
			truncated = true
		}
	}
	if len(newFiles) < len(content.GetMatches()) {
		truncated = true
	}
	if truncated {
		content.ClientTruncated = true
		newFiles = addGrepContentTruncationNotice(newFiles, originalBytes)
	}
	content.Matches = newFiles
}

func addGrepContentTruncationNotice(files []*agentv1.GrepFileMatch, originalBytes int) []*agentv1.GrepFileMatch {
	used := grepContentBytes(files)
	notice := replayTruncationNotice("Grep", grepReplayContentLimit, used, originalBytes)
	match := &agentv1.GrepContentMatch{
		LineNumber:       0,
		Content:          notice,
		ContentTruncated: true,
		IsContextLine:    true,
	}
	if len(files) == 0 {
		return []*agentv1.GrepFileMatch{{File: "[truncated]", Matches: []*agentv1.GrepContentMatch{match}}}
	}
	files[len(files)-1].Matches = append(files[len(files)-1].Matches, match)
	return files
}

func grepContentBytes(files []*agentv1.GrepFileMatch) int {
	used := 0
	for _, file := range files {
		for _, match := range file.GetMatches() {
			used += len(match.GetContent())
		}
	}
	return used
}

// firstGrepFilesResult 取 workspaceResults 中首个 files 结果。
func firstGrepFilesResult(success *agentv1.GrepSuccess) *agentv1.GrepFilesResult {
	if success == nil {
		return nil
	}
	for _, item := range success.GetWorkspaceResults() {
		if item == nil {
			continue
		}
		if files := item.GetFiles(); files != nil {
			return files
		}
	}
	if active := success.GetActiveEditorResult(); active != nil {
		if files := active.GetFiles(); files != nil {
			return files
		}
	}
	return nil
}

func buildEmptyGlobResult(argsJSON []byte) *agentv1.GrepResult {
	args, _ := decodeArgsMap(argsJSON)
	path := readGlobTargetDirectoryArg(args)
	pattern := readGlobPatternArg(args)
	filesResult := &agentv1.GrepFilesResult{}
	success := &agentv1.GrepSuccess{
		Pattern:    pattern,
		Path:       path,
		OutputMode: "files_with_matches",
		WorkspaceResults: map[string]*agentv1.GrepUnionResult{
			path: {
				Result: &agentv1.GrepUnionResult_Files{
					Files: filesResult,
				},
			},
		},
	}
	return &agentv1.GrepResult{
		Result: &agentv1.GrepResult_Success{
			Success: success,
		},
	}
}

// convertWriteResultToEditResult 把 WriteResult 映射为 EditResult。
func convertWriteResultToEditResult(result *agentv1.WriteResult) *agentv1.EditResult {
	if result == nil {
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_Error{
				Error: &agentv1.EditError{Error: "write result missing"},
			},
		}
	}
	switch item := result.GetResult().(type) {
	case *agentv1.WriteResult_Success:
		success := &agentv1.EditSuccess{
			Path:                 item.Success.GetPath(),
			AfterFullFileContent: item.Success.GetFileContentAfterWrite(),
		}
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_Success{Success: success},
		}
	case *agentv1.WriteResult_PermissionDenied:
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_WritePermissionDenied{
				WritePermissionDenied: &agentv1.EditWritePermissionDenied{
					Path:  item.PermissionDenied.GetPath(),
					Error: item.PermissionDenied.GetError(),
				},
			},
		}
	case *agentv1.WriteResult_Rejected:
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_Rejected{
				Rejected: &agentv1.EditRejected{
					Path:   item.Rejected.GetPath(),
					Reason: item.Rejected.GetReason(),
				},
			},
		}
	case *agentv1.WriteResult_Error:
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_Error{
				Error: &agentv1.EditError{
					Path:              item.Error.GetPath(),
					Error:             item.Error.GetError(),
					ModelVisibleError: stringPtr(item.Error.GetError()),
				},
			},
		}
	case *agentv1.WriteResult_NoSpace:
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_Error{
				Error: &agentv1.EditError{
					Path:              item.NoSpace.GetPath(),
					Error:             "no space left",
					ModelVisibleError: stringPtr("no space left"),
				},
			},
		}
	default:
		return &agentv1.EditResult{
			Result: &agentv1.EditResult_Error{
				Error: &agentv1.EditError{Error: "unknown write result"},
			},
		}
	}
}

// convertDiagnosticsResultToReadLintsToolResult 把 DiagnosticsResult 映射为 ReadLintsToolResult。
func convertDiagnosticsResultToReadLintsToolResult(result *agentv1.DiagnosticsResult) *agentv1.ReadLintsToolResult {
	if result == nil {
		return &agentv1.ReadLintsToolResult{
			Result: &agentv1.ReadLintsToolResult_Error{
				Error: &agentv1.ReadLintsToolError{ErrorMessage: "diagnostics result missing"},
			},
		}
	}
	switch item := result.GetResult().(type) {
	case *agentv1.DiagnosticsResult_Success:
		fileDiagnostics := &agentv1.FileDiagnostics{
			Path:             item.Success.GetPath(),
			Diagnostics:      convertDiagnostics(item.Success.GetDiagnostics()),
			DiagnosticsCount: item.Success.GetTotalDiagnostics(),
		}
		return &agentv1.ReadLintsToolResult{
			Result: &agentv1.ReadLintsToolResult_Success{
				Success: &agentv1.ReadLintsToolSuccess{
					FileDiagnostics:  []*agentv1.FileDiagnostics{fileDiagnostics},
					TotalFiles:       1,
					TotalDiagnostics: int32(len(fileDiagnostics.GetDiagnostics())),
				},
			},
		}
	case *agentv1.DiagnosticsResult_Error:
		return &agentv1.ReadLintsToolResult{
			Result: &agentv1.ReadLintsToolResult_Error{
				Error: &agentv1.ReadLintsToolError{ErrorMessage: item.Error.GetError()},
			},
		}
	case *agentv1.DiagnosticsResult_Rejected:
		return &agentv1.ReadLintsToolResult{
			Result: &agentv1.ReadLintsToolResult_Error{
				Error: &agentv1.ReadLintsToolError{ErrorMessage: item.Rejected.GetReason()},
			},
		}
	default:
		return &agentv1.ReadLintsToolResult{
			Result: &agentv1.ReadLintsToolResult_Error{
				Error: &agentv1.ReadLintsToolError{ErrorMessage: "unknown diagnostics result"},
			},
		}
	}
}

// convertDiagnostics 把 Diagnostic 转成 DiagnosticItem。
func convertDiagnostics(items []*agentv1.Diagnostic) []*agentv1.DiagnosticItem {
	if len(items) == 0 {
		return nil
	}
	result := make([]*agentv1.DiagnosticItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, &agentv1.DiagnosticItem{
			Severity: item.GetSeverity(),
			Range: &agentv1.DiagnosticRange{
				Start: item.GetRange().GetStart(),
				End:   item.GetRange().GetEnd(),
			},
			Message: item.GetMessage(),
			Source:  item.GetSource(),
			Code:    item.GetCode(),
			IsStale: item.GetIsStale(),
		})
	}
	return result
}

// convertSubagentResultToTaskResult 把 SubagentResult 映射为 TaskResult。
func convertSubagentResultToTaskResult(result *agentv1.SubagentResult) *agentv1.TaskResult {
	if result == nil {
		return &agentv1.TaskResult{
			Result: &agentv1.TaskResult_Error{
				Error: &agentv1.TaskError{Error: "subagent result missing"},
			},
		}
	}
	switch item := result.GetResult().(type) {
	case *agentv1.SubagentResult_Success:
		steps := make([]*agentv1.ConversationStep, 0, 1)
		if text := strings.TrimSpace(item.Success.GetFinalMessage()); text != "" {
			steps = append(steps, &agentv1.ConversationStep{
				Message: &agentv1.ConversationStep_AssistantMessage{
					AssistantMessage: &agentv1.AssistantMessage{Text: text},
				},
			})
		}
		if len(steps) == 0 {
			if isBackgroundSubagentSuccess(item.Success) {
				return &agentv1.TaskResult{
					Result: &agentv1.TaskResult_Success{
						Success: &agentv1.TaskSuccess{
							AgentId:          stringPtr(strings.TrimSpace(item.Success.GetAgentId())),
							IsBackground:     true,
							BackgroundReason: item.Success.GetBackgroundReason(),
							TranscriptPath:   stringPtr(strings.TrimSpace(item.Success.GetTranscriptPath())),
						},
					},
				}
			}
			return &agentv1.TaskResult{
				Result: &agentv1.TaskResult_Error{
					Error: &agentv1.TaskError{Error: "subagent returned empty response"},
				},
			}
		}
		return &agentv1.TaskResult{
			Result: &agentv1.TaskResult_Success{
				Success: &agentv1.TaskSuccess{
					ConversationSteps: steps,
					AgentId:           stringPtr(strings.TrimSpace(item.Success.GetAgentId())),
				},
			},
		}
	case *agentv1.SubagentResult_Error:
		return &agentv1.TaskResult{
			Result: &agentv1.TaskResult_Error{
				Error: &agentv1.TaskError{Error: item.Error.GetError()},
			},
		}
	default:
		return &agentv1.TaskResult{
			Result: &agentv1.TaskResult_Error{
				Error: &agentv1.TaskError{Error: "unknown subagent result"},
			},
		}
	}
}

func isBackgroundSubagentSuccess(success *agentv1.SubagentSuccess) bool {
	if success == nil {
		return false
	}
	return success.GetBackgroundReason() != agentv1.SubagentBackgroundReason_SUBAGENT_BACKGROUND_REASON_UNSPECIFIED ||
		strings.TrimSpace(success.GetTranscriptPath()) != ""
}

// DecodeGlobToolArgs 解析并归一化 Glob 参数，兼容历史与模型常见别名。
func DecodeGlobToolArgs(raw []byte) (*agentv1.GlobToolArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return nil, err
	}
	return buildGlobToolArgs(args), nil
}

// DecodeReadToolArgs decodes Read args for ToolCall replay/update payloads.
func DecodeReadToolArgs(raw []byte) (*agentv1.ReadToolArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return nil, err
	}
	result := &agentv1.ReadToolArgs{
		Path: strings.TrimSpace(readStringArg(args, "path")),
	}
	if result.Path == "" {
		return result, fmt.Errorf("Read path is required")
	}
	if offset, found, err := runtimecore.ReadInt32Arg(args, "offset"); err != nil {
		return result, err
	} else if found {
		result.Offset = int32Ptr(offset)
	}
	if limit, found, err := runtimecore.ReadUint32Arg(args, "limit"); err != nil {
		return result, err
	} else if found {
		if limit <= 1<<31-1 {
			result.Limit = int32Ptr(int32(limit))
		}
	}
	return result, nil
}

// DecodeGrepToolArgs decodes Grep args for client exec and ToolCall payloads.
func DecodeGrepToolArgs(raw []byte, toolCallID string) (*agentv1.GrepArgs, error) {
	args, err := decodeArgsMap(raw)
	if err != nil {
		return nil, err
	}
	result := &agentv1.GrepArgs{
		Pattern:    strings.TrimSpace(readStringArg(args, "pattern")),
		Path:       stringPtr(strings.TrimSpace(readStringArg(args, "path"))),
		Glob:       stringPtr(strings.TrimSpace(readStringArg(args, "glob"))),
		OutputMode: stringPtr(strings.TrimSpace(readStringArg(args, "output_mode", "outputMode"))),
		Type:       stringPtr(strings.TrimSpace(readStringArg(args, "type"))),
		ToolCallId: strings.TrimSpace(toolCallID),
	}
	if result.Pattern == "" {
		return result, fmt.Errorf("Grep pattern is required")
	}
	if contextBefore, found, err := runtimecore.ReadInt32Arg(args, "-B"); err != nil {
		return result, err
	} else if found {
		result.ContextBefore = int32Ptr(contextBefore)
	}
	if contextAfter, found, err := runtimecore.ReadInt32Arg(args, "-A"); err != nil {
		return result, err
	} else if found {
		result.ContextAfter = int32Ptr(contextAfter)
	}
	if context, found, err := runtimecore.ReadInt32Arg(args, "-C"); err != nil {
		return result, err
	} else if found {
		result.Context = int32Ptr(context)
	}
	caseInsensitive, err := readBoolPtrArg(args, "-i")
	if err != nil {
		return result, err
	}
	result.CaseInsensitive = caseInsensitive
	if headLimit, found, err := runtimecore.ReadInt32Arg(args, "head_limit", "headLimit"); err != nil {
		return result, err
	} else if found {
		result.HeadLimit = int32Ptr(headLimit)
	}
	multiline, err := readBoolPtrArg(args, "multiline")
	if err != nil {
		return result, err
	}
	result.Multiline = multiline
	if offset, found, err := runtimecore.ReadInt32Arg(args, "offset"); err != nil {
		return result, err
	} else if found {
		result.Offset = int32Ptr(offset)
	}
	return result, nil
}

// stringPtr 在需要 optional string 时构造指针值。
func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringPtrIfNonEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// DecodeShellStdout 直接返回 shell stream stdout 的文本内容。
func DecodeShellStdout(stdout *agentv1.ShellStreamStdout) string {
	if stdout == nil {
		return ""
	}
	return stdout.GetData()
}

// int32Ptr 在需要 optional int32 时构造指针值。
func int32Ptr(value int32) *int32 {
	return &value
}

// uint32Ptr 在需要 optional uint32 时构造指针值。
func uint32Ptr(value uint32) *uint32 {
	return &value
}

// uint64Ptr 在需要 optional uint64 时构造指针值。
func uint64Ptr(value uint64) *uint64 {
	return &value
}

// shellAbortReasonPtr 在需要 optional ShellAbortReason 时构造指针值。
func shellAbortReasonPtr(value agentv1.ShellAbortReason) *agentv1.ShellAbortReason {
	return &value
}

// readJSONStringArg 从工具 JSON 参数中读取字符串参数。
func readJSONStringArg(raw []byte, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	args, err := decodeArgsMap(raw)
	if err != nil {
		return ""
	}
	return readStringArg(args, keys...)
}

// summarizeExecuteHookResponse 提取各类 hook 响应对模型的可见文本。
func summarizeExecuteHookResponse(response *agentv1.ExecuteHookResponse) string {
	if response == nil {
		return ""
	}
	switch item := response.GetResponse().(type) {
	case *agentv1.ExecuteHookResponse_PreCompact:
		return strings.TrimSpace(item.PreCompact.GetUserMessage())
	case *agentv1.ExecuteHookResponse_PreToolUse:
		pre := item.PreToolUse
		parts := make([]string, 0, 3)
		if permission := strings.TrimSpace(pre.GetPermission()); permission != "" {
			parts = append(parts, "permission: "+permission)
		}
		if message := strings.TrimSpace(pre.GetUserMessage()); message != "" {
			parts = append(parts, "message: "+message)
		}
		if message := strings.TrimSpace(pre.GetAgentMessage()); message != "" {
			parts = append(parts, "agent message: "+message)
		}
		if len(parts) == 0 {
			return ""
		}
		return "pre-tool-use hook: " + strings.Join(parts, "; ")
	case *agentv1.ExecuteHookResponse_SubagentStart:
		start := item.SubagentStart
		parts := make([]string, 0, 2)
		if permission := strings.TrimSpace(start.GetPermission()); permission != "" {
			parts = append(parts, "permission: "+permission)
		}
		if message := strings.TrimSpace(start.GetUserMessage()); message != "" {
			parts = append(parts, "message: "+message)
		}
		if len(parts) == 0 {
			return ""
		}
		return "subagent-start hook: " + strings.Join(parts, "; ")
	case *agentv1.ExecuteHookResponse_SubagentStop:
		return "subagent-stop hook: " + strings.TrimSpace(item.SubagentStop.GetFollowupMessage())
	case *agentv1.ExecuteHookResponse_PostToolUse:
		return "post-tool-use hook: " + strings.TrimSpace(item.PostToolUse.GetAdditionalContext())
	case *agentv1.ExecuteHookResponse_PostToolUseFailure:
		return "post-tool-use-failure hook"
	default:
		return ""
	}
}

// summarizeFetchResult 生成 Fetch 结果的模型回写摘要。
func summarizeFetchResult(result *agentv1.FetchResult) string {
	if result == nil {
		return ""
	}
	switch item := result.GetResult().(type) {
	case *agentv1.FetchResult_Success:
		success := item.Success
		content := truncateReplayTextMiddle("Fetch content", strings.TrimSpace(success.GetContent()), shellReplayStreamLimit)
		return fmt.Sprintf("fetched %s (status %d, content-type %s):\n%s",
			strings.TrimSpace(success.GetUrl()), success.GetStatusCode(), strings.TrimSpace(success.GetContentType()), content)
	case *agentv1.FetchResult_Error:
		return fmt.Sprintf("fetch failed: %s (url: %s)", strings.TrimSpace(item.Error.GetError()), strings.TrimSpace(item.Error.GetUrl()))
	default:
		return "fetch completed with no result"
	}
}

// summarizeRecordScreenResult 生成 RecordScreen 结果的模型回写摘要。
func summarizeRecordScreenResult(result *agentv1.RecordScreenResult) string {
	if result == nil {
		return ""
	}
	switch item := result.GetResult().(type) {
	case *agentv1.RecordScreenResult_StartSuccess:
		return fmt.Sprintf("screen recording started (prior recording cancelled=%t, save_as_filename ignored=%t)",
			item.StartSuccess.GetWasPriorRecordingCancelled(), item.StartSuccess.GetWasSaveAsFilenameIgnored())
	case *agentv1.RecordScreenResult_SaveSuccess:
		msg := fmt.Sprintf("screen recording saved to %s (duration %d ms)", strings.TrimSpace(item.SaveSuccess.GetPath()), item.SaveSuccess.GetRecordingDurationMs())
		if reason := item.SaveSuccess.GetRequestedFilePathRejectedReason(); reason != agentv1.RequestedFilePathRejectedReason_REQUESTED_FILE_PATH_REJECTED_REASON_UNSPECIFIED {
			msg += fmt.Sprintf("; requested file path rejected: %s", reason)
		}
		return msg
	case *agentv1.RecordScreenResult_DiscardSuccess:
		return "screen recording discarded"
	case *agentv1.RecordScreenResult_Failure:
		return fmt.Sprintf("screen recording failed: %s", strings.TrimSpace(item.Failure.GetError()))
	default:
		return "screen recording completed with no result"
	}
}

// summarizeComputerUseResult 生成 ComputerUse 结果的模型回写摘要。
func summarizeComputerUseResult(result *agentv1.ComputerUseResult) string {
	if result == nil {
		return ""
	}
	switch item := result.GetResult().(type) {
	case *agentv1.ComputerUseResult_Success:
		success := item.Success
		msg := fmt.Sprintf("computer use completed: %d actions in %d ms", success.GetActionCount(), success.GetDurationMs())
		if logText := strings.TrimSpace(success.GetLog()); logText != "" {
			msg += "\nlog: " + logText
		}
		if screenshotPath := strings.TrimSpace(success.GetScreenshotPath()); screenshotPath != "" {
			msg += "\nscreenshot: " + screenshotPath
		}
		if strings.TrimSpace(success.GetScreenshot()) != "" {
			msg += "\nscreenshot captured (inline)"
		}
		return msg
	case *agentv1.ComputerUseResult_Error:
		failed := item.Error
		msg := fmt.Sprintf("computer use failed: %s", strings.TrimSpace(failed.GetError()))
		if logText := strings.TrimSpace(failed.GetLog()); logText != "" {
			msg += "\nlog: " + logText
		}
		return msg
	default:
		return "computer use completed with no result"
	}
}

// summarizeForceBackgroundSubagentResult 生成 ForceBackgroundSubagent 结果的模型回写摘要。
func summarizeForceBackgroundSubagentResult(result *agentv1.ForceBackgroundSubagentResult) string {
	if result == nil {
		return ""
	}
	switch result.GetStatus() {
	case agentv1.ForceBackgroundSubagentStatus_FORCE_BACKGROUND_SUBAGENT_STATUS_ACCEPTED:
		return "subagent moved to background"
	case agentv1.ForceBackgroundSubagentStatus_FORCE_BACKGROUND_SUBAGENT_STATUS_NOT_FOUND:
		return "force background subagent failed: subagent not found"
	default:
		return "force background subagent completed with no result"
	}
}

// recordingModeFromString 把模型侧模式名映射为 RecordingMode 枚举。
func recordingModeFromString(mode string) agentv1.RecordingMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "start_recording", "start", "record":
		return agentv1.RecordingMode_RECORDING_MODE_START_RECORDING
	case "save_recording", "save", "stop":
		return agentv1.RecordingMode_RECORDING_MODE_SAVE_RECORDING
	case "discard_recording", "discard":
		return agentv1.RecordingMode_RECORDING_MODE_DISCARD_RECORDING
	default:
		return agentv1.RecordingMode_RECORDING_MODE_START_RECORDING
	}
}

// DecodeComputerUseActionsForLocal 导出 ComputerUse 动作解码，供 forwarder 本地执行注入复用。
func DecodeComputerUseActionsForLocal(raw []byte) ([]*agentv1.ComputerUseAction, error) {
	return decodeComputerUseActions(raw)
}

// decodeComputerUseActions 解析 ComputerUse 的模型侧动作参数并映射为协议动作。
func decodeComputerUseActions(raw []byte) ([]*agentv1.ComputerUseAction, error) {
	var input struct {
		Actions []computerUseActionInput `json:"actions"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	actions := make([]*agentv1.ComputerUseAction, 0, len(input.Actions))
	for _, item := range input.Actions {
		action, err := buildComputerUseAction(item)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

// computerUseActionInput 是模型侧 ComputerUse 动作的宽松输入格式。
type computerUseActionInput struct {
	Type         string            `json:"type"`
	X            *int32            `json:"x,omitempty"`
	Y            *int32            `json:"y,omitempty"`
	Text         string            `json:"text,omitempty"`
	Key          string            `json:"key,omitempty"`
	DurationMs   *int32            `json:"duration_ms,omitempty"`
	Direction    string            `json:"direction,omitempty"`
	Amount       *int32            `json:"amount,omitempty"`
	Count        *int32            `json:"count,omitempty"`
	Button       string            `json:"button,omitempty"`
	ModifierKeys string            `json:"modifier_keys,omitempty"`
	Path         []coordinateInput `json:"path,omitempty"`
}

// coordinateInput 是坐标的宽松输入格式。
type coordinateInput struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

func buildComputerUseAction(item computerUseActionInput) (*agentv1.ComputerUseAction, error) {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "mouse_move", "move":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_MouseMove{
				MouseMove: &agentv1.MouseMoveAction{Coordinate: computerUseCoordinate(item)},
			},
		}, nil
	case "click":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Click{
				Click: &agentv1.ClickAction{
					Coordinate:   computerUseCoordinate(item),
					Button:       computerUseMouseButton(item.Button),
					Count:        int32Value(item.Count),
					ModifierKeys: stringPtrIfNonEmpty(item.ModifierKeys),
				},
			},
		}, nil
	case "mouse_down", "down":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_MouseDown{
				MouseDown: &agentv1.MouseDownAction{Button: computerUseMouseButton(item.Button)},
			},
		}, nil
	case "mouse_up", "up":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_MouseUp{
				MouseUp: &agentv1.MouseUpAction{Button: computerUseMouseButton(item.Button)},
			},
		}, nil
	case "drag":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Drag{
				Drag: &agentv1.DragAction{
					Path:   computerUsePath(item.Path),
					Button: computerUseMouseButton(item.Button),
				},
			},
		}, nil
	case "scroll":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Scroll{
				Scroll: &agentv1.ScrollAction{
					Coordinate:   computerUseCoordinate(item),
					Direction:    computerUseScrollDirection(item.Direction),
					Amount:       int32Value(item.Amount),
					ModifierKeys: stringPtrIfNonEmpty(item.ModifierKeys),
				},
			},
		}, nil
	case "type":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Type{
				Type: &agentv1.TypeAction{Text: item.Text},
			},
		}, nil
	case "key":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Key{
				Key: &agentv1.KeyAction{
					Key:            item.Key,
					HoldDurationMs: item.DurationMs,
				},
			},
		}, nil
	case "wait":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Wait{
				Wait: &agentv1.WaitAction{DurationMs: int32Value(item.DurationMs)},
			},
		}, nil
	case "screenshot":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_Screenshot{
				Screenshot: &agentv1.ScreenshotAction{},
			},
		}, nil
	case "cursor_position", "cursor":
		return &agentv1.ComputerUseAction{
			Action: &agentv1.ComputerUseAction_CursorPosition{
				CursorPosition: &agentv1.CursorPositionAction{},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported computer use action type: %s", strings.TrimSpace(item.Type))
	}
}

func computerUseCoordinate(item computerUseActionInput) *agentv1.Coordinate {
	if item.X == nil && item.Y == nil {
		return nil
	}
	return &agentv1.Coordinate{X: int32Value(item.X), Y: int32Value(item.Y)}
}

func computerUsePath(path []coordinateInput) []*agentv1.Coordinate {
	if len(path) == 0 {
		return nil
	}
	result := make([]*agentv1.Coordinate, 0, len(path))
	for _, point := range path {
		result = append(result, &agentv1.Coordinate{X: point.X, Y: point.Y})
	}
	return result
}

func computerUseMouseButton(button string) agentv1.MouseButton {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "right":
		return agentv1.MouseButton_MOUSE_BUTTON_RIGHT
	case "middle":
		return agentv1.MouseButton_MOUSE_BUTTON_MIDDLE
	case "back":
		return agentv1.MouseButton_MOUSE_BUTTON_BACK
	case "forward":
		return agentv1.MouseButton_MOUSE_BUTTON_FORWARD
	default:
		return agentv1.MouseButton_MOUSE_BUTTON_LEFT
	}
}

func computerUseScrollDirection(direction string) agentv1.ScrollDirection {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "up":
		return agentv1.ScrollDirection_SCROLL_DIRECTION_UP
	case "down":
		return agentv1.ScrollDirection_SCROLL_DIRECTION_DOWN
	case "left":
		return agentv1.ScrollDirection_SCROLL_DIRECTION_LEFT
	case "right":
		return agentv1.ScrollDirection_SCROLL_DIRECTION_RIGHT
	default:
		return agentv1.ScrollDirection_SCROLL_DIRECTION_DOWN
	}
}

func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

// buildStructValueMap 把普通 JSON 对象映射为 protobuf Struct value 映射。
func buildStructValueMap(items map[string]any) map[string]*structpb.Value {
	if len(items) == 0 {
		return make(map[string]*structpb.Value)
	}
	result := make(map[string]*structpb.Value, len(items))
	for key, value := range items {
		item, err := structpb.NewValue(value)
		if err != nil {
			continue
		}
		result[key] = item
	}
	return result
}

// decodeArgsMap 把工具 JSON 参数解析为通用 map。
func decodeArgsMap(raw []byte) (map[string]any, error) {
	return runtimecore.DecodeArgsMap(raw)
}

func buildGlobToolArgs(args map[string]any) *agentv1.GlobToolArgs {
	return &agentv1.GlobToolArgs{
		TargetDirectory: stringPtr(readGlobTargetDirectoryArg(args)),
		GlobPattern:     readGlobPatternArg(args),
	}
}

func readGlobPatternArg(args map[string]any) string {
	return strings.TrimSpace(readStringArg(args, "glob_pattern", "globPattern", "pattern"))
}

func readGlobTargetDirectoryArg(args map[string]any) string {
	return strings.TrimSpace(readStringArg(args, "target_directory", "targetDirectory", "path"))
}

// readStringArg 从参数映射中按多个候选键读取字符串。
func readStringArg(args map[string]any, keys ...string) string {
	return runtimecore.ReadStringArg(args, keys...)
}

// readBoolArg 从参数映射中按多个候选键读取布尔值。
func readBoolArg(args map[string]any, keys ...string) bool {
	return runtimecore.ReadBoolArg(args, keys...)
}

// hasArgKey 判断参数映射中是否存在任一候选键。
func hasArgKey(args map[string]any, keys ...string) bool {
	return runtimecore.HasArgKey(args, keys...)
}

// readStringSliceArg 读取字符串数组参数。
func readStringSliceArg(args map[string]any, keys ...string) []string {
	return runtimecore.ReadStringSliceArg(args, keys...)
}

func readBoolPtrArg(args map[string]any, keys ...string) (*bool, error) {
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		typed, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%s must be a boolean", key)
		}
		return &typed, nil
	}
	return nil, nil
}
