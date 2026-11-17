package promptengine

import (
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func readLintsToolCall(paths []string, checked string) *agentv1.ToolCall {
	return &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ReadLintsToolCall{
			ReadLintsToolCall: &agentv1.ReadLintsToolCall{
				Args: &agentv1.ReadLintsToolArgs{Paths: paths},
				Result: &agentv1.ReadLintsToolResult{
					Result: &agentv1.ReadLintsToolResult_Success{
						Success: &agentv1.ReadLintsToolSuccess{
							FileDiagnostics: []*agentv1.FileDiagnostics{{Path: checked}},
							TotalFiles:      1,
						},
					},
				},
			},
		},
	}
}

// 多路径 ReadLints 只检查了第一个时，replay 必须点名未检查的路径，避免模型误判为无诊断。
func TestReadLintsReplayNamesUncheckedPaths(t *testing.T) {
	message, ok := BuildToolResultReplayMessage("call-1", readLintsToolCall([]string{"a.ts", "b.ts", "c.ts"}, "a.ts"))
	if !ok {
		t.Fatal("BuildToolResultReplayMessage() ok = false")
	}
	if !strings.Contains(message.Content, "Not checked: b.ts, c.ts") || !strings.Contains(message.Content, "Only a.ts was checked") {
		t.Fatalf("replay content missing unchecked notice: %s", message.Content)
	}
}

func TestReadLintsReplaySinglePathHasNoNotice(t *testing.T) {
	message, ok := BuildToolResultReplayMessage("call-1", readLintsToolCall([]string{"a.ts"}, "a.ts"))
	if !ok {
		t.Fatal("BuildToolResultReplayMessage() ok = false")
	}
	if strings.Contains(message.Content, "Not checked") {
		t.Fatalf("single-path replay should not carry a notice: %s", message.Content)
	}
}
