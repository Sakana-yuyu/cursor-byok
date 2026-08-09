// tool_catalog_test.go 验证工具资产（prompt/*/tools.json）与 mode 白名单的一致性，
// 并在 Cursor proto 更新后输出「新工具待接入」报告，防止升级后工具静默失效。
package forwarder

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"cursor/gen/agentv1"
	"cursor/prompt"
)

// modeWhitelistTests 列出每个静态工具资产对应的白名单。
// 断言：资产中出现的工具名必须被该 mode 的白名单放行，否则模型永远拿不到该工具。
var modeWhitelistTests = []struct {
	mode      prompt.Mode
	whitelist map[string]struct{}
}{
	{prompt.ModeAgent, agentModeToolNames},
	{prompt.ModeAsk, askModeToolNames},
	{prompt.ModePlan, planModeToolNames},
	{prompt.ModeDebug, debugModeToolNames},
	{prompt.ModeMultitask, multitaskModeToolNames},
}

func loadAssetToolNames(t *testing.T, mode prompt.Mode) []string {
	t.Helper()
	rawTools, err := prompt.ReadTools(mode)
	if err != nil {
		t.Fatalf("read %s/tools.json: %v", mode, err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawTools, &items); err != nil {
		t.Fatalf("decode %s/tools.json: %v", mode, err)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, err := extractToolName(item)
		if err != nil {
			t.Fatalf("extract tool name in %s/tools.json: %v", mode, err)
		}
		names = append(names, name)
	}
	return names
}

func TestToolAssetsConsistentWithModeWhitelists(t *testing.T) {
	for _, tc := range modeWhitelistTests {
		t.Run(string(tc.mode), func(t *testing.T) {
			names := loadAssetToolNames(t, tc.mode)
			assetSet := make(map[string]struct{}, len(names))
			for _, name := range names {
				assetSet[name] = struct{}{}
			}
			// 资产 ⊆ 白名单：资产里出现但白名单不放行的工具永远不可用（白写）。
			for _, name := range names {
				if _, ok := tc.whitelist[name]; !ok {
					t.Errorf("工具 %q 已加入 %s/tools.json 资产但未加入 %s 白名单（tool_catalog.go），模型将无法使用该工具", name, tc.mode, tc.mode)
				}
			}
			// 白名单 ⊆ 资产：白名单放行但该 mode 资产没有 schema 的工具，
			// Load 阶段会被 selectToolsByOrderedNames 过滤/报错，同样不可用。
			for name := range tc.whitelist {
				if _, ok := assetSet[name]; !ok {
					t.Errorf("%s 白名单工具 %q 缺少 %s/tools.json schema，模型将无法使用该工具", tc.mode, name, tc.mode)
				}
			}
		})
	}
	// subagent/tools.json 是只读子代理的最小工具集（独立资产），
	// 其中每个工具都必须属于 agent 全量白名单。
	for _, name := range loadAssetToolNames(t, prompt.ModeSubagent) {
		if _, ok := agentModeToolNames[name]; !ok {
			t.Errorf("subagent 资产工具 %q 不在 agent 白名单中，子代理会话将无法使用该工具", name)
		}
	}
}

func TestChildConversationCannotDispatchSubagents(t *testing.T) {
	tools, names, err := NewToolCatalog().Load(agentv1.AgentMode_AGENT_MODE_PLAN, "explore")
	if err != nil {
		t.Fatalf("load child conversation tools: %v", err)
	}
	if len(tools) != len(names) {
		t.Fatalf("loaded tool descriptors = %d, names = %d", len(tools), len(names))
	}
	exposed := make(map[string]struct{}, len(names))
	for _, name := range names {
		exposed[name] = struct{}{}
	}

	for _, toolName := range []string{"Task", "ForceBackgroundSubagent", "SubagentAwait", "send_final_summary"} {
		if _, ok := exposed[toolName]; ok {
			t.Errorf("child tool catalog must not expose %q", toolName)
		}
		if isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_PLAN, "explore", toolName) {
			t.Errorf("child invocation guard must reject %q", toolName)
		}
	}
	for _, toolName := range []string{"Read", "Grep", "Shell"} {
		if _, ok := exposed[toolName]; !ok {
			t.Errorf("child tool catalog should retain %q", toolName)
		}
		if !isToolAllowedInMode(agentv1.AgentMode_AGENT_MODE_PLAN, "explore", toolName) {
			t.Errorf("child invocation guard should allow %q", toolName)
		}
	}
}

// protoToolArgsPattern 匹配 proto 源中的工具参数消息：message <Name>Args { ... }。
// Cursor 的工具在 agent_v1.proto 中约定为 <Name>Args / <Name>Result / <Name>ToolCall 三件套。
var protoToolArgsPattern = regexp.MustCompile(`(?m)^message ([A-Z][A-Za-z0-9]*)Args\s*\{`)

// TestProtoToolArgsSyncReport 对比 Cursor 官方 agent_v1.proto 与本地工具资产/白名单，
// 输出「proto 新增工具待接入」清单。Cursor 升级后更新 proto/from_extensions 再跑本测试，
// 即可发现新工具。报告为提示性质（新工具是否接入是产品决策），不失败。
func TestProtoToolArgsSyncReport(t *testing.T) {
	const protoPath = "../../../proto/from_extensions/agent_v1.proto"
	data, err := os.ReadFile(protoPath)
	if err != nil {
		t.Skipf("跳过 proto 同步检查：%v", err)
	}
	protoTools := map[string]struct{}{}
	for _, match := range protoToolArgsPattern.FindAllStringSubmatch(string(data), -1) {
		protoTools[match[1]] = struct{}{}
	}
	known := knownBuiltInToolNameSet()

	var pending []string
	for name := range protoTools {
		if _, ok := known[name]; !ok {
			pending = append(pending, name)
		}
	}
	if len(pending) > 0 {
		t.Logf("[sync-tool-catalog] proto 新增 %d 个工具未接入：%v（运行 go run ./cmd/sync-tool-catalog --write 生成骨架，再补白名单与 schema）", len(pending), pending)
	} else {
		t.Log("[sync-tool-catalog] proto 中所有 <Name>Args 工具均已接入白名单")
	}

	stale := 0
	for name := range known {
		if _, ok := protoTools[name]; !ok {
			stale++
		}
	}
	if stale > 0 {
		t.Logf("[sync-tool-catalog] %d/%d 已知工具未在 proto 中发现独立 <Name>Args 消息（可能为共享参数或已废弃）", stale, len(known))
	}
}

// knownBuiltInToolNameSet 返回全部内置工具白名单的并集（包内 isKnownBuiltInToolName 的测试副本）。
func knownBuiltInToolNameSet() map[string]struct{} {
	known := map[string]struct{}{}
	for _, names := range []map[string]struct{}{
		agentModeToolNames,
		multitaskModeToolNames,
		debugModeToolNames,
		askModeToolNames,
		planModeToolNames,
	} {
		for name := range names {
			known[name] = struct{}{}
		}
	}
	return known
}
