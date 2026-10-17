package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const compatibilityAgentInput = `syntax = "proto3";
package agent.v1;
message UserMessage {}
message ConversationStep {}
message TodoItem {}
message ConversationTokenDetails {}
message ConversationPlan {}
message FileState {}
message PlanRegistryEntry {}
message CommunicateUpdateHistoryEntry {}
message CommunicateUpdateTurnState {}
message ShellCommand {}
message ShellOutputNotificationConfig {}
message ModelDetails {
  string model_id = 1;
}
`

func TestCompatibilityRestoresExplicitClosureAndIsIdempotent(t *testing.T) {
	got, err := applyCompatibility(compatibilityAgentInput, "agent_v1.proto")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AgentConversationTurn", "AgentConversationTurnStructure", "ConversationState", "ConversationSummary", "ConversationTurn", "ConversationTurnStructure", "ConversationSummaryArchive", "ShellConversationTurn", "ShellConversationTurnStructure", "ShellOutput"} {
		if count := strings.Count(got, "\nmessage "+name+" {"); count != 1 {
			t.Errorf("顶层类型 %s 数量为 %d", name, count)
		}
	}
	for _, decl := range []string{"optional bool supports_auto_context = 11;", "optional int32 auto_context_max_tokens = 12;", "optional int32 context_token_limit = 15;", "optional int32 context_token_limit_for_max_mode = 16;"} {
		if !strings.Contains(got, decl) {
			t.Error("缺少字段", decl)
		}
	}
	second, err := applyCompatibility(got, "agent_v1.proto")
	if err != nil {
		t.Fatal(err)
	}
	if second != got {
		t.Fatal("重复应用改变结果")
	}
}

func TestCompatibilityPreservesExistingTopLevel(t *testing.T) {
	input := compatibilityAgentInput + "message ConversationSummary { string current_summary = 7; }\n"
	got, err := applyCompatibility(input, "agent_v1.proto")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "string current_summary = 7;") || strings.Contains(got, "string summary = 1;") {
		t.Fatal("不得覆写新版已有顶层类型")
	}
}

func TestCompatibilityDistinguishesNestedAndIndentedTopLevel(t *testing.T) {
	input := compatibilityAgentInput + "message Wrapper {\nmessage ConversationSummary { string nested = 9; }\n}\n  message ShellOutput { string current = 7; }\n"
	got, err := applyCompatibility(input, "agent_v1.proto")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "message ConversationSummary {") != 2 || strings.Count(got, "message ShellOutput {") != 1 || !strings.Contains(got, "string current = 7;") {
		t.Fatal("应依据语法中的顶层身份判断，不能依据缩进或名字前缀")
	}
}

func TestCompatibilityRejectsModelFieldConflicts(t *testing.T) {
	for _, decl := range []string{"string reused = 11;", "optional string supports_auto_context = 11;", "optional bool supports_auto_context = 17;", "bool supports_auto_context = 11;", "reserved 11;"} {
		t.Run(decl, func(t *testing.T) {
			input := strings.Replace(compatibilityAgentInput, "  string model_id = 1;", "  string model_id = 1;\n  "+decl, 1)
			if _, err := applyCompatibility(input, "agent_v1.proto"); err == nil {
				t.Fatal("应拒绝字段冲突")
			}
		})
	}
}

func TestCompatibilityRejectsMissingDependency(t *testing.T) {
	input := strings.Replace(compatibilityAgentInput, "message ShellCommand {}\n", "", 1)
	if _, err := applyCompatibility(input, "agent_v1.proto"); err == nil || !strings.Contains(err.Error(), "ShellCommand") {
		t.Fatalf("应明确缺失依赖: %v", err)
	}
}

func TestCompatibilityRepositoryNestedTypes(t *testing.T) {
	input := `syntax = "proto3";
package aiserver.v1;
message RepositoryInfo {}
`
	got, err := applyCompatibility(input, "aiserver_v1.proto")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "message Synced {") || !strings.Contains(got, "Synced synced = 4;") {
		t.Fatal("仓库状态嵌套协议丢失")
	}
	second, err := applyCompatibility(got, "aiserver_v1.proto")
	if err != nil {
		t.Fatal(err)
	}
	if second != got {
		t.Fatal("重复应用改变结果")
	}
}

func TestCompatibilityActualBaseline(t *testing.T) {
	dir := os.Getenv("CURSOR_COMPAT_BASELINE_DIR")
	if dir == "" {
		t.Skip("未指定只读旧基线")
	}
	for _, name := range []string{"agent_v1.proto", "aiserver_v1.proto"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := applyCompatibility(string(data), name)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(data) {
			t.Errorf("旧编译输入 %s 已含兼容内容，不应改变", name)
		}
	}
}

func TestRepairUsesExactNestedMessagePath(t *testing.T) {
	text := `syntax = "proto3";
package aiserver.v1;
message SimpleFileDiff {}
message ComposerCapabilityRequest {
  message DiffReviewCapability {
    message SimpleFileDiff {}
    repeated bytes diffs = 2;
  }
}
`
	item := repair{"aiserver_v1.proto", "ComposerCapabilityRequest.DiffReviewCapability", "repeated bytes diffs = 2;", "repeated SimpleFileDiff diffs = 2;", "ComposerCapabilityRequest.DiffReviewCapability.SimpleFileDiff"}
	got, changed, err := applyRepair(text, item)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(got, item.Restored) {
		t.Fatal("嵌套字段没有还原")
	}
	withoutNested := strings.Replace(text, "    message SimpleFileDiff {}\n", "", 1)
	if _, _, err := applyRepair(withoutNested, item); err == nil {
		t.Fatal("不得用同名顶层类型掩盖缺失嵌套目标")
	}
}
