package execbridge

import (
	"fmt"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
)

func runtimecoreToolInvocation(toolName string, argsJSON string) runtimecore.ToolInvocation {
	return runtimecore.ToolInvocation{
		CallID:   "call-1",
		ToolName: toolName,
		ArgsJSON: []byte(argsJSON),
	}
}

// 以下用例锁定自上游 cursor-byok Rust 版移植的兼容性修复：
// - fc1e96d6 工具参数别名（Claude 系模型常按 Claude Code 习惯发 file_path/content/query）
// - 9e2d2241 工具名大小写归一 + Bash→Shell 别名
// - 9cb16e11 block_until_ms 必须是整数值毫秒数
// - 0f564c0e MCP 文本被部分截断时必须补 notice
// - c3951a44 ListMcpResources 截断 notice 按实际成因报告单位

// TestCanonicalToolKindBashAliasAndCase 锁定上游 9e2d2241 的语义：
// Anthropic 系模型常按 Claude Code 习惯把 Shell 工具写成 Bash；大小写变体
// 也必须归一到同一 canonical kind，不能报 unsupported exec tool。
func TestCanonicalToolKindBashAliasAndCase(t *testing.T) {
	for name, want := range map[string]string{
		"Shell": "shell", "shell": "shell", "SHELL": "shell",
		"Bash": "shell", "bash": "shell", "BASH": "shell",
		"Read": "read", "read": "read",
		"WriteShellStdin": "write_shell_stdin",
		"GetMcpTools":     "mcp_state",
	} {
		if got := canonicalToolKind(name); got != want {
			t.Fatalf("canonicalToolKind(%q) = %q, want %q", name, got, want)
		}
	}
	if got := canonicalToolKind("not-a-tool"); got != "" {
		t.Fatalf("canonicalToolKind(not-a-tool) = %q, want empty", got)
	}
}

// TestShellBlockUntilMSRequiresInteger 锁定上游 9cb16e11/b85cb378 的语义：
// block_until_ms 只接受整数值毫秒数；小数、负数、超出 int32 proto 范围的值
// 一律协议错误，不能静默截断或回绕。
func TestShellBlockUntilMSRequiresInteger(t *testing.T) {
	bridge := NewBridge()
	// 整数（含数字字符串与整数值浮点）接受。
	for _, argsJSON := range []string{
		`{"command":"echo hi","block_until_ms":15000}`,
		`{"command":"echo hi","block_until_ms":15000.0}`,
		`{"command":"echo hi","blockUntilMS":15000}`,
	} {
		if _, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecoreToolInvocation("Shell", argsJSON)); err != nil {
			t.Fatalf("OpenExec(Shell, %s) error = %v, want accepted", argsJSON, err)
		}
	}
	// 小数、负数、超 int32 范围拒绝。
	for _, argsJSON := range []string{
		`{"command":"echo hi","block_until_ms":1500.5}`,
		`{"command":"echo hi","block_until_ms":-1}`,
		`{"command":"echo hi","block_until_ms":99999999999}`,
	} {
		if _, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecoreToolInvocation("Shell", argsJSON)); err == nil {
			t.Fatalf("OpenExec(Shell, %s) error = nil, want rejection", argsJSON)
		}
	}
}

func TestReadAcceptsFilePathAliases(t *testing.T) {
	for _, alias := range []string{"path", "file_path", "filePath"} {
		argsJSON := fmt.Sprintf(`{%q:"notes/todo.md"}`, alias)
		bridge := NewBridge()
		serverMessage, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecoreToolInvocation("Read", argsJSON))
		if err != nil {
			t.Fatalf("OpenExec(Read, %s) error = %v", alias, err)
		}
		if got := serverMessage.GetExecServerMessage().GetReadArgs().GetPath(); got != "notes/todo.md" {
			t.Fatalf("alias %s path = %q, want notes/todo.md", alias, got)
		}
	}
}

func TestWriteAcceptsContentAliasForContents(t *testing.T) {
	bridge := NewBridge()
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecoreToolInvocation("Write", `{"path":"notes/todo.md","content":"hello"}`))
	if err != nil {
		t.Fatalf("OpenExec(Write, content alias) error = %v", err)
	}
	write := serverMessage.GetExecServerMessage().GetWriteArgs()
	if write == nil {
		t.Fatal("write args arm is not selected")
	}
	if write.GetPath() != "notes/todo.md" {
		t.Fatalf("path = %q, want notes/todo.md", write.GetPath())
	}
	if write.GetFileText() != "hello" {
		t.Fatalf("file_text = %q, want hello (content alias must not write an empty file)", write.GetFileText())
	}
}

func TestWriteAcceptsFilePathAlias(t *testing.T) {
	bridge := NewBridge()
	serverMessage, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecoreToolInvocation("Write", `{"file_path":"notes/todo.md","contents":"hello"}`))
	if err != nil {
		t.Fatalf("OpenExec(Write, file_path alias) error = %v", err)
	}
	if got := serverMessage.GetExecServerMessage().GetWriteArgs().GetPath(); got != "notes/todo.md" {
		t.Fatalf("path = %q, want notes/todo.md", got)
	}
}

func TestDeleteAcceptsFilePathAlias(t *testing.T) {
	for _, alias := range []string{"path", "file_path", "filePath"} {
		argsJSON := fmt.Sprintf(`{%q:"notes/obsolete.md"}`, alias)
		bridge := NewBridge()
		serverMessage, _, err := bridge.OpenExec(OpenExecContext{WorkspaceHint: "/ws"}, runtimecoreToolInvocation("Delete", argsJSON))
		if err != nil {
			t.Fatalf("OpenExec(Delete, %s) error = %v", alias, err)
		}
		if got := serverMessage.GetExecServerMessage().GetDeleteArgs().GetPath(); got != "notes/obsolete.md" {
			t.Fatalf("alias %s path = %q, want notes/obsolete.md", alias, got)
		}
	}
}

// TestTruncateMcpToolResultAddsNoticeWhenTextPartiallyTruncated 锁定上游 0f564c0e 的语义：
// 总预算被某条文本耗尽后，后续被剩余预算截短（或跳过）的条目不能静默丢弃，
// 必须补一条汇总 notice 让模型知道内容被截断。
func TestTruncateMcpToolResultAddsNoticeWhenTextPartiallyTruncated(t *testing.T) {
	totalLimit := mcpReplayTextTotalLimit
	result := &agentv1.McpToolResult{
		Result: &agentv1.McpToolResult_Success{
			Success: &agentv1.McpSuccess{
				Content: []*agentv1.McpToolResultContentItem{
					{Content: &agentv1.McpToolResultContentItem_Text{Text: &agentv1.McpTextContent{Text: strings.Repeat("a", totalLimit-8)}}},
					{Content: &agentv1.McpToolResultContentItem_Text{Text: &agentv1.McpTextContent{Text: strings.Repeat("b", 100)}}},
				},
			},
		},
	}
	truncated := truncateMcpToolResultForReplay(result)
	if truncated == nil {
		t.Fatal("truncated result = nil")
	}
	items := truncated.GetSuccess().GetContent()
	if len(items) != 3 {
		t.Fatalf("content items = %d, want 3 (two kept + one summary notice)", len(items))
	}
	if got := items[1].GetText().GetText(); got != strings.Repeat("b", 8) {
		t.Fatalf("second item text length = %d, want the 8 surviving bytes", len(got))
	}
	notice := items[2].GetText().GetText()
	if !strings.Contains(notice, "[truncated: MCP text result exceeded") {
		t.Fatalf("summary notice missing: %q", notice)
	}
	if !strings.Contains(notice, fmt.Sprintf("showing %d of %d bytes", totalLimit, totalLimit-8+100)) {
		t.Fatalf("summary notice numbers wrong: %q", notice)
	}
}

// TestTruncateListMcpResourcesReportsResourceCountCap 锁定上游 c3951a44 的语义：
// 个数上限触发的截断必须按 resources 个数报告，不能套用字节模板。
func TestTruncateListMcpResourcesReportsResourceCountCap(t *testing.T) {
	result := &agentv1.ListMcpResourcesExecResult{
		Result: &agentv1.ListMcpResourcesExecResult_Success{
			Success: &agentv1.ListMcpResourcesSuccess{
				Resources: makeResources(250, 0),
			},
		},
	}
	truncated := truncateListMcpResourcesResultForReplay(result)
	if truncated == nil {
		t.Fatal("truncated result = nil")
	}
	resources := truncated.GetSuccess().GetResources()
	if len(resources) != mcpResourcesReplayCount+1 {
		t.Fatalf("resources = %d, want %d (cap + notice)", len(resources), mcpResourcesReplayCount+1)
	}
	notice := resources[len(resources)-1].GetDescription()
	if !strings.Contains(notice, fmt.Sprintf("exceeded %d resources; showing %d of %d resources", mcpResourcesReplayCount, mcpResourcesReplayCount, 250)) {
		t.Fatalf("count-cap notice wrong: %q", notice)
	}
	if strings.Contains(notice, "bytes") {
		t.Fatalf("count-cap notice must not report bytes: %q", notice)
	}
}

// TestTruncateListMcpResourcesReportsBytesWhenBudgetCuts 锁定字节预算成因的报告：
// 上限按 bytes、数量按 resources，两个维度都必须准确。
func TestTruncateListMcpResourcesReportsBytesWhenBudgetCuts(t *testing.T) {
	result := &agentv1.ListMcpResourcesExecResult{
		Result: &agentv1.ListMcpResourcesExecResult_Success{
			Success: &agentv1.ListMcpResourcesSuccess{
				Resources: makeResources(300, mcpResourceDescriptionSize),
			},
		},
	}
	truncated := truncateListMcpResourcesResultForReplay(result)
	if truncated == nil {
		t.Fatal("truncated result = nil")
	}
	resources := truncated.GetSuccess().GetResources()
	notice := resources[len(resources)-1].GetDescription()
	if !strings.Contains(notice, fmt.Sprintf("exceeded %d bytes; showing", mcpResourcesReplayLimit)) {
		t.Fatalf("byte-budget notice wrong: %q", notice)
	}
	if !strings.Contains(notice, "of 300 resources") {
		t.Fatalf("byte-budget notice must report resource counts: %q", notice)
	}
}

func makeResources(count int, descriptionBytes int) []*agentv1.ListMcpResourcesExecResult_McpResource {
	resources := make([]*agentv1.ListMcpResourcesExecResult_McpResource, 0, count)
	for index := 0; index < count; index++ {
		resource := &agentv1.ListMcpResourcesExecResult_McpResource{
			Uri: fmt.Sprintf("file:///resources/%d", index),
		}
		if descriptionBytes > 0 {
			resource.Description = stringPtr(strings.Repeat("d", descriptionBytes))
		}
		resources = append(resources, resource)
	}
	return resources
}
