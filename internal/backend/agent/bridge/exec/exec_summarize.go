// exec_summarize.go 承载结果摘要与归一化：各工具结果转模型可读文本、MCP 结果转换与 replay 截断。
package execbridge

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"cursor/gen/agentv1"
)

func normalizeReadResultForModel(result *agentv1.ReadResult) *agentv1.ReadResult {
	if result == nil {
		return nil
	}
	cloned, ok := proto.Clone(result).(*agentv1.ReadResult)
	if !ok {
		return result
	}
	success := cloned.GetSuccess()
	if success == nil {
		return cloned
	}
	if output, ok := success.GetOutput().(*agentv1.ReadSuccess_Content); ok {
		normalized := normalizeReadContentLineEndingsToLF(output.Content)
		if normalized != output.Content {
			output.Content = normalized
			success.TotalLines = countLFReadLines(normalized)
		}
	}
	return cloned
}

func normalizeReadContentLineEndingsToLF(content string) string {
	if !strings.ContainsAny(content, "\r\n") {
		return content
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(normalized, "\r", "\n")
}

func countLFReadLines(content string) int32 {
	if content == "" {
		return 0
	}
	count := int32(strings.Count(content, "\n"))
	if !strings.HasSuffix(content, "\n") {
		count++
	}
	return count
}

// summarizeReadResult 生成 Read 结果摘要。
func summarizeReadResult(result *agentv1.ReadResult) string {
	if result == nil {
		return "read result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.ReadResult_Success:
		if item.Success.GetContent() != "" {
			content := truncateReplayLines("Read", item.Success.GetContent(), readReplayLineLimit)
			return truncateReplayText("Read", content, readReplayContentLimit)
		}
		if item.Success.GetData() != nil {
			return fmt.Sprintf("read binary bytes=%d", len(item.Success.GetData()))
		}
		return fmt.Sprintf("read success path=%s", item.Success.GetPath())
	case *agentv1.ReadResult_Error:
		return item.Error.GetError()
	case *agentv1.ReadResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.ReadResult_FileNotFound:
		return fmt.Sprintf("file not found: %s", item.FileNotFound.GetPath())
	case *agentv1.ReadResult_PermissionDenied:
		return fmt.Sprintf("permission denied: %s", item.PermissionDenied.GetPath())
	case *agentv1.ReadResult_InvalidFile:
		return item.InvalidFile.GetReason()
	default:
		return "unknown read result"
	}
}

// summarizeWriteResult 生成 Write 结果摘要。
func summarizeWriteResult(result *agentv1.WriteResult) string {
	if result == nil {
		return "write result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.WriteResult_Success:
		if after := strings.TrimSpace(item.Success.GetFileContentAfterWrite()); after != "" {
			return after
		}
		return fmt.Sprintf("write success path=%s lines=%d", item.Success.GetPath(), item.Success.GetLinesCreated())
	case *agentv1.WriteResult_PermissionDenied:
		return item.PermissionDenied.GetError()
	case *agentv1.WriteResult_NoSpace:
		return fmt.Sprintf("no space left: %s", item.NoSpace.GetPath())
	case *agentv1.WriteResult_Error:
		return item.Error.GetError()
	case *agentv1.WriteResult_Rejected:
		return item.Rejected.GetReason()
	default:
		return "unknown write result"
	}
}

// summarizeDiagnosticsResult 生成 ReadLints 对应的执行结果摘要。
func summarizeDiagnosticsResult(result *agentv1.DiagnosticsResult) string {
	if result == nil {
		return "diagnostics result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.DiagnosticsResult_Success:
		return fmt.Sprintf("diagnostics success path=%s count=%d", item.Success.GetPath(), item.Success.GetTotalDiagnostics())
	case *agentv1.DiagnosticsResult_Error:
		return item.Error.GetError()
	case *agentv1.DiagnosticsResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.DiagnosticsResult_FileNotFound:
		return fmt.Sprintf("diagnostics file not found: %s", item.FileNotFound.GetPath())
	case *agentv1.DiagnosticsResult_PermissionDenied:
		return fmt.Sprintf("diagnostics permission denied: %s", item.PermissionDenied.GetPath())
	default:
		return "unknown diagnostics result"
	}
}

// summarizeSubagentResult 生成 Task 对应的执行结果摘要。
func summarizeSubagentResult(result *agentv1.SubagentResult) string {
	if result == nil {
		return "subagent result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.SubagentResult_Success:
		if text := strings.TrimSpace(item.Success.GetFinalMessage()); text != "" {
			return text
		}
		if isBackgroundSubagentSuccess(item.Success) {
			return fmt.Sprintf("subagent running in background agent_id=%s reason=%s transcript_path=%s",
				strings.TrimSpace(item.Success.GetAgentId()),
				item.Success.GetBackgroundReason().String(),
				strings.TrimSpace(item.Success.GetTranscriptPath()),
			)
		}
		return "subagent returned empty response"
	case *agentv1.SubagentResult_Error:
		return item.Error.GetError()
	default:
		return "unknown subagent result"
	}
}

// summarizeDeleteResult 生成 Delete 结果摘要。
func summarizeDeleteResult(result *agentv1.DeleteResult) string {
	if result == nil {
		return "delete result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.DeleteResult_Success:
		return fmt.Sprintf("delete success path=%s", item.Success.GetPath())
	case *agentv1.DeleteResult_FileNotFound:
		return fmt.Sprintf("file not found: %s", item.FileNotFound.GetPath())
	case *agentv1.DeleteResult_NotFile:
		return fmt.Sprintf("not file: %s", item.NotFile.GetPath())
	case *agentv1.DeleteResult_PermissionDenied:
		return item.PermissionDenied.GetClientVisibleError()
	case *agentv1.DeleteResult_FileBusy:
		return item.FileBusy.GetPath()
	case *agentv1.DeleteResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.DeleteResult_Error:
		return item.Error.GetError()
	default:
		return "unknown delete result"
	}
}

// summarizeGrepResult 生成 Grep 结果摘要。
func summarizeGrepResult(result *agentv1.GrepResult) string {
	if result == nil {
		return "grep result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.GrepResult_Success:
		return fmt.Sprintf("grep success pattern=%s mode=%s", item.Success.GetPattern(), item.Success.GetOutputMode())
	case *agentv1.GrepResult_Error:
		return item.Error.GetError()
	default:
		return "unknown grep result"
	}
}

func summarizeGlobContinuationPayload(result *agentv1.GrepResult, argsJSON []byte) string {
	args, _ := decodeArgsMap(argsJSON)
	pattern := readGlobPatternArg(args)
	target := readGlobTargetDirectoryArg(args)
	if result == nil || result.GetSuccess() == nil {
		return formatGlobNoMatches(pattern, target)
	}
	filesResult := firstGrepFilesResult(result.GetSuccess())
	if filesResult == nil || len(filesResult.GetFiles()) == 0 {
		return formatGlobNoMatches(pattern, target)
	}
	files := filesResult.GetFiles()
	text := strings.Join(files, "\n")
	if total := int(filesResult.GetTotalFiles()); total > len(files) {
		text += fmt.Sprintf("\n...there are still %d files...", total-len(files))
	}
	return text
}

func formatGlobNoMatches(pattern string, target string) string {
	if pattern == "" && target == "" {
		return "no matches"
	}
	if target == "" {
		return fmt.Sprintf("no matches for %s", pattern)
	}
	if pattern == "" {
		return fmt.Sprintf("no matches in %s", target)
	}
	return fmt.Sprintf("no matches for %s in %s", pattern, target)
}

// summarizeLsResult 生成 Ls 结果摘要。
func summarizeLsResult(result *agentv1.LsResult) string {
	if result == nil {
		return "ls result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.LsResult_Success:
		return fmt.Sprintf("ls success path=%s files=%d", item.Success.GetDirectoryTreeRoot().GetAbsPath(), item.Success.GetDirectoryTreeRoot().GetNumFiles())
	case *agentv1.LsResult_Error:
		return item.Error.GetError()
	case *agentv1.LsResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.LsResult_Timeout:
		return fmt.Sprintf("ls timeout path=%s", item.Timeout.GetDirectoryTreeRoot().GetAbsPath())
	default:
		return "unknown ls result"
	}
}

// summarizeMcpResult 生成 MCP 执行结果摘要。
func summarizeMcpResult(result *agentv1.McpToolResult) string {
	if result == nil {
		return "mcp result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.McpToolResult_Success:
		return fmt.Sprintf("mcp success content=%d", len(item.Success.GetContent()))
	case *agentv1.McpToolResult_Error:
		return item.Error.GetError()
	case *agentv1.McpToolResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.McpToolResult_PermissionDenied:
		return item.PermissionDenied.GetError()
	default:
		return "unknown mcp result"
	}
}

// convertMcpResult 把 ExecClientMessage 中的 McpResult 映射为 ToolCall 使用的 McpToolResult。
func convertMcpResult(result *agentv1.McpResult) *agentv1.McpToolResult {
	if result == nil {
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_Error{
				Error: &agentv1.McpToolError{Error: "mcp result missing"},
			},
		}
	}
	switch item := result.GetResult().(type) {
	case *agentv1.McpResult_Success:
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_Success{
				Success: item.Success,
			},
		}
	case *agentv1.McpResult_Error:
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_Error{
				Error: &agentv1.McpToolError{Error: item.Error.GetError()},
			},
		}
	case *agentv1.McpResult_Rejected:
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_Rejected{
				Rejected: item.Rejected,
			},
		}
	case *agentv1.McpResult_PermissionDenied:
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_PermissionDenied{
				PermissionDenied: item.PermissionDenied,
			},
		}
	case *agentv1.McpResult_ToolNotFound:
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_Error{
				Error: &agentv1.McpToolError{
					Error: fmt.Sprintf("tool not found: %s", item.ToolNotFound.GetName()),
				},
			},
		}
	default:
		return &agentv1.McpToolResult{
			Result: &agentv1.McpToolResult_Error{
				Error: &agentv1.McpToolError{Error: "unknown mcp result"},
			},
		}
	}
}

func truncateMcpToolResultForReplay(result *agentv1.McpToolResult) *agentv1.McpToolResult {
	if result == nil {
		return nil
	}
	cloned, ok := proto.Clone(result).(*agentv1.McpToolResult)
	if !ok || cloned == nil || cloned.GetSuccess() == nil {
		return result
	}
	success := cloned.GetSuccess()
	notices := make([]string, 0, 3)
	if structured := success.GetStructuredContent(); structured != nil {
		if encoded, err := protojson.Marshal(structured); err == nil && len(encoded) > mcpReplayStructuredLimit {
			replacement, _ := structpb.NewStruct(map[string]any{
				"_truncated":          true,
				"original_json_bytes": float64(len(encoded)),
				"limit_bytes":         float64(mcpReplayStructuredLimit),
			})
			success.StructuredContent = replacement
			notices = append(notices, replayTruncationNotice("MCP structured_content", mcpReplayStructuredLimit, 0, len(encoded)))
		}
	}
	content := success.GetContent()
	if len(content) > mcpReplayContentItemLimit {
		notices = append(notices, fmt.Sprintf("[truncated: MCP content items exceeded %d items; showing %d of %d items]", mcpReplayContentItemLimit, mcpReplayContentItemLimit, len(content)))
		content = content[:mcpReplayContentItemLimit]
	}
	totalText := 0
	truncatedContent := make([]*agentv1.McpToolResultContentItem, 0, len(content)+len(notices))
	for _, item := range content {
		if item == nil {
			continue
		}
		next := proto.Clone(item).(*agentv1.McpToolResultContentItem)
		if text := next.GetText(); text != nil {
			original := text.GetText()
			nextText := truncateReplayText("MCP content item", original, mcpReplayTextItemLimit)
			remaining := mcpReplayTextTotalLimit - totalText
			if remaining <= 0 {
				notices = append(notices, replayTruncationNotice("MCP text", mcpReplayTextTotalLimit, totalText, totalText+len(original)))
				continue
			}
			nextText = truncateReplayText("MCP text", nextText, remaining)
			text.Text = nextText
			totalText += len(nextText)
			truncatedContent = append(truncatedContent, next)
			continue
		}
		if image := next.GetImage(); image != nil && len(image.GetData()) > mcpReplayBinaryLimit {
			original := len(image.GetData())
			image.Data, _ = truncateByteSlice(image.GetData(), mcpReplayBinaryLimit)
			notices = append(notices, replayTruncationNotice("MCP image data", mcpReplayBinaryLimit, len(image.GetData()), original))
		}
		truncatedContent = append(truncatedContent, next)
	}
	for _, notice := range notices {
		truncatedContent = append(truncatedContent, &agentv1.McpToolResultContentItem{
			Content: &agentv1.McpToolResultContentItem_Text{
				Text: &agentv1.McpTextContent{Text: notice},
			},
		})
	}
	success.Content = truncatedContent
	return cloned
}

// summarizeListMcpResourcesResult 生成 MCP 资源列表结果摘要。
func summarizeListMcpResourcesResult(result *agentv1.ListMcpResourcesExecResult) string {
	if result == nil {
		return "list mcp resources result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.ListMcpResourcesExecResult_Success:
		return fmt.Sprintf("list mcp resources success count=%d", len(item.Success.GetResources()))
	case *agentv1.ListMcpResourcesExecResult_Error:
		return item.Error.GetError()
	case *agentv1.ListMcpResourcesExecResult_Rejected:
		return item.Rejected.GetReason()
	default:
		return "unknown list mcp resources result"
	}
}

// summarizeReadMcpResourceResult 生成读取 MCP 资源结果摘要。
func summarizeReadMcpResourceResult(result *agentv1.ReadMcpResourceExecResult) string {
	if result == nil {
		return "read mcp resource result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.ReadMcpResourceExecResult_Success:
		if text := strings.TrimSpace(item.Success.GetText()); text != "" {
			return text
		}
		if blob := item.Success.GetBlob(); len(blob) > 0 {
			return fmt.Sprintf("read mcp resource blob=%d", len(blob))
		}
		return fmt.Sprintf("read mcp resource success uri=%s", item.Success.GetUri())
	case *agentv1.ReadMcpResourceExecResult_Error:
		return item.Error.GetError()
	case *agentv1.ReadMcpResourceExecResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.ReadMcpResourceExecResult_NotFound:
		return fmt.Sprintf("mcp resource not found: %s", item.NotFound.GetUri())
	default:
		return "unknown read mcp resource result"
	}
}

func truncateListMcpResourcesResultForReplay(result *agentv1.ListMcpResourcesExecResult) *agentv1.ListMcpResourcesExecResult {
	if result == nil {
		return nil
	}
	cloned, ok := proto.Clone(result).(*agentv1.ListMcpResourcesExecResult)
	if !ok || cloned == nil || cloned.GetSuccess() == nil {
		return result
	}
	resources := cloned.GetSuccess().GetResources()
	if len(resources) > mcpResourcesReplayCount {
		resources = resources[:mcpResourcesReplayCount]
	}
	trimmed := make([]*agentv1.ListMcpResourcesExecResult_McpResource, 0, len(resources))
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		next := proto.Clone(resource).(*agentv1.ListMcpResourcesExecResult_McpResource)
		if next.Description != nil {
			description := truncateReplayText("MCP resource description", next.GetDescription(), mcpResourceDescriptionSize)
			next.Description = stringPtr(description)
		}
		trimmed = append(trimmed, next)
	}
	cloned.GetSuccess().Resources = trimmed
	for len(cloned.GetSuccess().Resources) > 0 {
		encoded, err := protojson.Marshal(cloned)
		if err != nil || len(encoded) <= mcpResourcesReplayLimit {
			break
		}
		cloned.GetSuccess().Resources = cloned.GetSuccess().Resources[:len(cloned.GetSuccess().Resources)-1]
	}
	if len(cloned.GetSuccess().Resources) < len(result.GetSuccess().GetResources()) {
		notice := replayTruncationNotice("ListMcpResources", mcpResourcesReplayLimit, len(cloned.GetSuccess().Resources), len(result.GetSuccess().GetResources()))
		cloned.GetSuccess().Resources = append(cloned.GetSuccess().Resources, &agentv1.ListMcpResourcesExecResult_McpResource{
			Uri:         "truncated:list-mcp-resources",
			Name:        stringPtr("truncated"),
			Description: stringPtr(notice),
		})
	}
	return cloned
}

func truncateReadMcpResourceResultForReplay(result *agentv1.ReadMcpResourceExecResult) *agentv1.ReadMcpResourceExecResult {
	if result == nil {
		return nil
	}
	cloned, ok := proto.Clone(result).(*agentv1.ReadMcpResourceExecResult)
	if !ok || cloned == nil || cloned.GetSuccess() == nil {
		return result
	}
	success := cloned.GetSuccess()
	if text := success.GetText(); text != "" {
		success.Content = &agentv1.ReadMcpResourceSuccess_Text{
			Text: truncateReplayText("FetchMcpResource", text, mcpReplayTextTotalLimit),
		}
		return cloned
	}
	if blob := success.GetBlob(); len(blob) > mcpReplayBinaryLimit {
		success.Content = &agentv1.ReadMcpResourceSuccess_Text{
			Text: replayTruncationNotice("FetchMcpResource blob", mcpReplayBinaryLimit, 0, len(blob)),
		}
	}
	return cloned
}
