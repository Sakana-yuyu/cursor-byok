// canvas_diagnostics.go 承载 Cursor Canvas 的 TypeScript 诊断和保存状态回灌。
//
// 原生 Cursor 每次写完受支持的项目目录、agent store 或 workfile 中的 canvas 源文件，
// 都会向 cursor-agent-exec 请求一次诊断，并把结果追加到模型可见的工具结果末尾；
// canvas 技能文档把这行输出当作权威诊断信号，模型据此自我修复 TSX。本地模式此前没有
// 这一步，模型写错了也无从得知。
//
// 严格对齐客户端输出格式：无 error/warning 时输出 "Canvas TypeScript check: no errors."，
// 否则输出 "Canvas TypeScript check: N issue(s) in <path>:" 加逐条诊断，整段以两个换行
// 拼在原工具结果之后。
//
// 降级原则（关键）：canvas 的渲染判定依赖 EditToolCall 的 EditResult 为 success，
// 诊断超时/失败/不可用时跳过类型检查文本，明确的保存状态仍独立反馈，
// 绝不允许把原始写入结果污染成 error。
package forwarder

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/logger"
)

const (
	// canvasDiagnosticsToolName 是内部派发用的伪工具名，对应执行桥的 canvas_diagnostics kind。
	canvasDiagnosticsToolName = "CanvasDiagnostics"
	// canvasDiagnosticsTimeout 对齐客户端 canvas_post_edit_diagnostics_timeout_ms 的默认值。
	// 超时后按「跳过诊断」收口，不影响写入结果。
	canvasDiagnosticsTimeout = 2 * time.Second
)

// canvasFilePathPattern 与客户端 AgentResponseAdapter 判定 canvas 的正则一致。
var (
	canvasFilePathPattern = regexp.MustCompile(`(?i)(^|/)\.cursor/projects/[^/]+/canvases/[^/]+\.canvas\.tsx$`)
	// 原生 agent store 路径限定根位置、存储主体及最多一层目录，且保持大小写敏感。
	canvasAgentStorePathPattern = regexp.MustCompile(`^cursor/stores/(?:user|automation)/canvases/(?:[^/]+/)?[^/]+\.canvas\.tsx$`)
	canvasWorkfilePathPattern   = regexp.MustCompile(`(?i)(?:^|/)[^/]+/files/canvases/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/source\.canvas\.tsx$`)
)

// isCanvasFilePath 判断路径是否是 canvas 源文件。
func isCanvasFilePath(path string) bool {
	normalized := normalizeCanvasPathForMatch(path)
	if normalized == "" {
		return false
	}
	return canvasFilePathPattern.MatchString(normalized) || canvasAgentStorePathPattern.MatchString(normalized) ||
		canvasWorkfilePathPattern.MatchString(normalized)
}

// normalizeCanvasPathForMatch 复刻客户端的路径归一化：反斜杠转正斜杠，
// 丢弃空段与 "."，遇到 ".." 回退一段，再用 "/" 拼接。
func normalizeCanvasPathForMatch(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	segments := strings.Split(strings.ReplaceAll(trimmed, `\`, "/"), "/")
	resolved := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
		case "..":
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
		default:
			resolved = append(resolved, segment)
		}
	}
	return strings.Join(resolved, "/")
}

// shouldCollectCanvasDiagnostics 只在写入成功且落在 canvas 路径时才要求诊断，
// 与客户端一致：失败的编辑不触发诊断。
func shouldCollectCanvasDiagnostics(result *agentv1.EditResult, path string) bool {
	if result.GetSuccess() == nil {
		return false
	}
	return isCanvasFilePath(firstNonEmpty(strings.TrimSpace(result.GetSuccess().GetPath()), strings.TrimSpace(path)))
}

// openCanvasDiagnosticsExec 派发一次 canvas 诊断请求，返回待挂起的执行桥记录。
func (service *Service) openCanvasDiagnosticsExec(stream *ActiveStream, toolCallID string, modelCallID string, path string) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	argsJSON := []byte(fmt.Sprintf(`{"path":%q}`, strings.TrimSpace(path)))
	return service.execBridge.OpenExec(execbridge.OpenExecContext{
		ConversationID: stream.ConversationID,
	}, runtimecore.ToolInvocation{
		CallID:      strings.TrimSpace(toolCallID),
		ToolName:    canvasDiagnosticsToolName,
		ArgsJSON:    argsJSON,
		ModelCallID: strings.TrimSpace(modelCallID),
	})
}

// scheduleCanvasDiagnosticsTimeout 为诊断请求排一个本地超时。到点仍未收到结果时，
// 合成一条 canvas 诊断错误结果投递给 actor，让写入按「跳过诊断」正常收口，
// 避免客户端不回包时整个 Write/PatchEdit 永久挂起。
func (service *Service) scheduleCanvasDiagnosticsTimeout(stream *ActiveStream, pending runtimecore.PendingExec, path string) {
	if service == nil || stream == nil {
		return
	}
	requestID := strings.TrimSpace(stream.RequestID)
	execID := strings.TrimSpace(pending.ExecID)
	messageID := pending.MessageID
	if requestID == "" || execID == "" {
		return
	}
	time.AfterFunc(canvasDiagnosticsTimeout, func() {
		current, ok := snapshotPendingExec(stream, execID)
		if !ok || current.MessageID != messageID {
			return
		}
		stream.mu.Lock()
		terminal := isTerminalStreamStatus(stream.Status)
		stream.mu.Unlock()
		if terminal {
			return
		}
		logger.Infof(
			"forwarder canvas diagnostics timed out request_id=%s tool_call_id=%s exec_id=%s path=%s timeout=%s",
			requestID,
			strings.TrimSpace(current.ToolCallID),
			execID,
			strings.TrimSpace(path),
			canvasDiagnosticsTimeout,
		)
		if err := service.postStreamCommandAsync(stream, streamCommand{
			Kind: streamCommandExecResult,
			Intent: InboundIntent{
				Kind:              "exec_result",
				RequestID:         requestID,
				ConversationID:    stream.ConversationID,
				ExecClientMessage: buildCanvasDiagnosticsTimeoutMessage(execID, messageID, path),
			},
		}); err != nil {
			logger.Errorf("forwarder canvas diagnostics timeout post failed request_id=%s exec_id=%s err=%v", requestID, execID, err)
		}
	})
}

// buildCanvasDiagnosticsTimeoutMessage 合成超时用的诊断错误结果。
func buildCanvasDiagnosticsTimeoutMessage(execID string, messageID uint32, path string) *agentv1.ExecClientMessage {
	return &agentv1.ExecClientMessage{
		Id:     messageID,
		ExecId: execID,
		Message: &agentv1.ExecClientMessage_CanvasDiagnosticsResult{
			CanvasDiagnosticsResult: &agentv1.CanvasDiagnosticsResult{
				Result: &agentv1.CanvasDiagnosticsResult_Error{
					Error: &agentv1.CanvasDiagnosticsError{
						Path:  strings.TrimSpace(path),
						Error: fmt.Sprintf("canvas diagnostics timed out after %s", canvasDiagnosticsTimeout),
					},
				},
			},
		},
	}
}

// formatCanvasDiagnosticsForModel 把诊断结果渲染成客户端同款的模型可见文本。
// 类型检查只取 success 分支，外层保存状态独立显示；旧版仅错误或超时时仍返回空串。
func formatCanvasDiagnosticsForModel(result *agentv1.CanvasDiagnosticsResult) string {
	return appendCanvasDiagnosticsToToolResult(
		formatCanvasTypeScriptDiagnostics(result.GetSuccess()),
		formatCanvasSaveState(result.GetSaveState(), result.GetTitle(), result.GetSaveDetail()),
	)
}

func formatCanvasTypeScriptDiagnostics(success *agentv1.CanvasDiagnosticsSuccess) string {
	if success == nil {
		return ""
	}
	reported := make([]*agentv1.Diagnostic, 0, len(success.GetDiagnostics()))
	for _, diagnostic := range success.GetDiagnostics() {
		switch diagnostic.GetSeverity() {
		case agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR, agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_WARNING:
			reported = append(reported, diagnostic)
		}
	}
	if len(reported) == 0 {
		return "Canvas TypeScript check: no errors."
	}
	noun := "issue"
	if len(reported) > 1 {
		noun = "issues"
	}
	lines := make([]string, 0, len(reported)+1)
	lines = append(lines, fmt.Sprintf("Canvas TypeScript check: %d %s in %s:", len(reported), noun, success.GetPath()))
	for _, diagnostic := range reported {
		severity := "ERROR"
		if diagnostic.GetSeverity() == agentv1.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_WARNING {
			severity = "WARNING"
		}
		source := ""
		if trimmed := strings.TrimSpace(diagnostic.GetSource()); trimmed != "" {
			source = fmt.Sprintf(" (%s)", trimmed)
		}
		lines = append(lines, fmt.Sprintf(
			"  [%s] L%d:%d - %s%s",
			severity,
			diagnostic.GetRange().GetStart().GetLine(),
			diagnostic.GetRange().GetStart().GetColumn(),
			diagnostic.GetMessage(),
			source,
		))
	}
	return strings.Join(lines, "\n")
}

// formatCanvasSaveState 保留原生保存状态的含义，不把异步受理误报为发布完成。
func formatCanvasSaveState(state, title, detail string) string {
	state = sanitizeCanvasSaveText(state, 100)
	if state == "" {
		return ""
	}
	title = sanitizeCanvasSaveText(title, 200)
	detail = sanitizeCanvasSaveText(detail, 500)
	if state == "pending" {
		lines := []string{"Canvas save accepted: validation passed; the canvas publishes asynchronously."}
		if title != "" {
			lines = append(lines, "Canvas title: "+title)
		}
		if detail != "" {
			lines = append(lines, "Canvas note: "+detail)
		}
		return strings.Join(lines, "\n")
	}
	var failureReason string
	switch state {
	case "invalid_path":
		failureReason = "this is not a valid canvas save path; nothing was published."
	case "unavailable":
		failureReason = "the user agent store is unavailable; nothing was published."
	case "too_large":
		failureReason = "the canvas source exceeds the size limit; nothing was published."
	case "typecheck_failed":
		failureReason = "the type check failed; nothing was published."
	case "compile_failed":
		failureReason = "the canvas failed to compile; nothing was published."
	}
	if failureReason != "" {
		if detail != "" {
			failureReason = detail
		}
		return "Canvas not saved: " + failureReason
	}
	text := "Canvas save state: " + state + "."
	if detail != "" {
		text += " " + detail
	}
	return text
}

// sanitizeCanvasSaveText 对齐原生 lce 的空白净化和 UTF-16 长度预算，且不截断 Unicode 字符。
func sanitizeCanvasSaveText(text string, limit int) string {
	text = strings.Join(strings.FieldsFunc(text, func(char rune) bool {
		return char <= '\x1f' || char == '\x7f' || unicode.Is(unicode.Zs, char) ||
			char == '\u2028' || char == '\u2029' || char == '\ufeff'
	}), " ")
	units := 0
	for index, char := range text {
		units += utf16.RuneLen(char)
		if units > limit {
			return text[:index] + "…"
		}
	}
	return text
}

// appendCanvasDiagnosticsToToolResult 把诊断文本拼到模型可见的工具结果末尾，
// 与客户端一致使用空行分隔。诊断为空时原样返回。
func appendCanvasDiagnosticsToToolResult(resultText string, diagnosticsText string) string {
	diagnosticsText = strings.TrimSpace(diagnosticsText)
	if diagnosticsText == "" {
		return resultText
	}
	if strings.TrimSpace(resultText) == "" {
		return diagnosticsText
	}
	return resultText + "\n\n" + diagnosticsText
}
