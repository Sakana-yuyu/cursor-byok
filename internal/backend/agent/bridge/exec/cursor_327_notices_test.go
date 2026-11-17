package execbridge

import (
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func ptrInt32(v int32) *int32    { return &v }
func ptrUint32(v uint32) *uint32 { return &v }
func ptrString(v string) *string { return &v }

// Cursor 3.27 的 MCP 错误恢复信息（schema 变更、可用工具、HTTP 状态）必须进入模型可见文本。
func TestConvertMcpErrorIncludesRecoveryDetails(t *testing.T) {
	result := convertMcpResult(&agentv1.McpResult{Result: &agentv1.McpResult_Error{Error: &agentv1.McpError{
		Error:        "invalid arguments",
		HttpStatus:   ptrInt32(400),
		RetryAfterMs: ptrUint32(1500),
		Recovery: &agentv1.McpRecovery{
			Outcome:              agentv1.McpRecovery_OUTCOME_CHANGED,
			FreshInputSchemaJson: ptrString(`{"type":"object"}`),
			AvailableTools:       []string{"search", "fetch"},
		},
	}}})
	text := result.GetError().GetError()
	for _, want := range []string{"invalid arguments", "HTTP status: 400", "Retry after: 1500ms", "definition has changed", `Updated input schema: {"type":"object"}`, "Available tools: search, fetch"} {
		if !strings.Contains(text, want) {
			t.Fatalf("mcp error text missing %q:\n%s", want, text)
		}
	}
}

func TestConvertMcpToolNotFoundIncludesAvailableTools(t *testing.T) {
	result := convertMcpResult(&agentv1.McpResult{Result: &agentv1.McpResult_ToolNotFound{ToolNotFound: &agentv1.McpToolNotFound{
		Name:     "old_tool",
		Recovery: &agentv1.McpRecovery{Outcome: agentv1.McpRecovery_OUTCOME_REMOVED, AvailableTools: []string{"new_tool"}},
	}}})
	text := result.GetError().GetError()
	if !strings.Contains(text, "tool not found: old_tool") || !strings.Contains(text, "no longer exists") || !strings.Contains(text, "Available tools: new_tool") {
		t.Fatalf("tool-not-found text = %q", text)
	}
}

// 客户端标记 OOM 终止时，shell 结果要告诉模型原因。
func TestShellTerminalPayloadReportsOomKill(t *testing.T) {
	exit := &agentv1.ShellStreamExit{Code: 137, OomKill: &agentv1.ShellOomKill{Kind: agentv1.ShellOomKill_KIND_COMMAND_KILLED, MemoryLimitBytes: 512 * 1024 * 1024}}
	withOutput := summarizeShellTerminalPayload("partial", "", exit, false)
	if !strings.Contains(withOutput, "partial") || !strings.Contains(withOutput, "out-of-memory killer (memory limit 512 MiB)") {
		t.Fatalf("payload with output = %q", withOutput)
	}
	noOutput := summarizeShellTerminalPayload("", "", exit, false)
	if !strings.Contains(noOutput, "code=137") || !strings.Contains(noOutput, "out-of-memory") {
		t.Fatalf("payload without output = %q", noOutput)
	}
	if plain := summarizeShellTerminalPayload("ok", "", &agentv1.ShellStreamExit{Code: 0}, false); strings.Contains(plain, "memory") {
		t.Fatalf("normal exit should not mention OOM: %q", plain)
	}
}
