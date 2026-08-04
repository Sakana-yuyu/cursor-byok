// sync-tool-catalog 是 Cursor 工具资产同步助手：对比 Cursor 官方 agent_v1.proto
// 中的工具消息（<Name>Args）与本地 prompt/agent/tools.json + tool_catalog.go 白名单，
// 报告「proto 新增工具」清单；--write 时把新增工具以骨架 schema 追加到
// prompt/agent/tools.json，之后仍需人工完善 description/parameters 并把工具名
// 加入 tool_catalog.go 对应 mode 白名单（internal/backend/forwarder 的
// TestToolAssetsConsistentWithModeWhitelists 会校验并提醒）。
//
// 用法（仓库根目录）：
//
//	go run ./cmd/sync-tool-catalog            # 只输出报告
//	go run ./cmd/sync-tool-catalog --write    # 报告并把新增工具骨架写入 agent/tools.json
//
// Cursor 升级后更新 proto/from_extensions/agent_v1.proto 再运行本工具，
// 即可发现新版本的 Agent 原生工具并完成接入，避免升级后工具静默失效。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	agentProtoRelPath = "proto/from_extensions/agent_v1.proto"
	agentToolsRelPath = "prompt/agent/tools.json"
)

// protoToolArgsPattern 匹配 proto 源中的工具参数消息：message <Name>Args { ... }。
// Cursor 的 Agent 工具在 agent_v1.proto 中约定为 <Name>Args / <Name>Result 三件套。
var protoToolArgsPattern = regexp.MustCompile(`(?m)^message ([A-Z][A-Za-z0-9]*)Args\s*\{`)

// toolSkeleton 是新增工具的最小可用 schema 骨架，description/parameters 需人工完善。
func toolSkeleton(name string) map[string]any {
	return map[string]any{
		"function": map[string]any{
			"description": fmt.Sprintf("TODO: 描述 %s 工具的用途与调用时机（参数定义见 agent_v1.proto 的 %sArgs 消息）。", name, name),
			"name":        name,
			"parameters": map[string]any{
				"properties": map[string]any{},
				"type":       "object",
			},
		},
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到 go.mod（当前目录 %s）", dir)
		}
		dir = parent
	}
}

func readProtoToolNames(root string) (map[string]struct{}, error) {
	data, err := os.ReadFile(filepath.Join(root, agentProtoRelPath))
	if err != nil {
		return nil, err
	}
	names := map[string]struct{}{}
	for _, match := range protoToolArgsPattern.FindAllStringSubmatch(string(data), -1) {
		names[match[1]] = struct{}{}
	}
	return names, nil
}

type toolAsset struct {
	path      string
	toolNames map[string]struct{}
}

func readToolAsset(root, relPath string) (toolAsset, error) {
	path := filepath.Join(root, relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return toolAsset{}, err
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return toolAsset{}, fmt.Errorf("解析 %s: %w", relPath, err)
	}
	names := map[string]struct{}{}
	for _, item := range items {
		fn, _ := item["function"].(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		if name != "" {
			names[name] = struct{}{}
		}
	}
	return toolAsset{path: path, toolNames: names}, nil
}

func main() {
	writeMode := flag.Bool("write", false, "把 proto 新增工具以骨架 schema 追加到 prompt/agent/tools.json")
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	protoTools, err := readProtoToolNames(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	agentAsset, err := readToolAsset(root, agentToolsRelPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	var pending []string
	for name := range protoTools {
		if _, ok := agentAsset.toolNames[name]; !ok {
			pending = append(pending, name)
		}
	}
	sort.Strings(pending)

	var stale []string
	for name := range agentAsset.toolNames {
		if _, ok := protoTools[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)

	fmt.Printf("== Cursor 工具同步报告 ==\n")
	fmt.Printf("proto 工具消息（<Name>Args）：%d 个\n", len(protoTools))
	fmt.Printf("agent/tools.json 工具 schema：%d 个\n", len(agentAsset.toolNames))
	fmt.Printf("proto 新增未接入：%d 个\n", len(pending))
	if len(pending) > 0 {
		fmt.Printf("  需要接入的工具：%s\n", strings.Join(pending, ", "))
	}
	fmt.Printf("本地有而 proto 无（可能已废弃/共享参数）：%d 个%s\n", len(stale), map[bool]string{true: "：" + strings.Join(stale, ", "), false: ""}[len(stale) > 0])

	if !*writeMode {
		if len(pending) > 0 {
			fmt.Printf("\n下一步：\n  1. go run ./cmd/sync-tool-catalog --write 生成骨架 schema\n  2. 完善 prompt/agent/tools.json 中新增工具的 description/parameters\n  3. 把工具名加入 internal/backend/forwarder/tool_catalog.go 对应 mode 白名单\n  4. go test ./internal/backend/forwarder/ 验证一致性\n")
		} else {
			fmt.Printf("\n工具资产与 proto 已同步，无需操作。\n")
		}
		return
	}

	if len(pending) == 0 {
		fmt.Printf("\n没有需要写入的新工具。\n")
		return
	}

	// --write：把新增工具骨架追加到 prompt/agent/tools.json。
	data, err := os.ReadFile(agentAsset.path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	for _, name := range pending {
		items = append(items, toolSkeleton(name))
	}
	// 原子写：先写同目录临时文件再 rename，避免中途失败损坏 tools.json。
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false) // 防止 description 中的 < > & 被转义污染 diff
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	tmpPath := agentAsset.path + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	if err := os.Rename(tmpPath, agentAsset.path); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	fmt.Printf("\n已向 %s 追加 %d 个工具骨架（TODO description 待完善）。\n", agentToolsRelPath, len(pending))
	fmt.Printf("接下来：把工具名加入 tool_catalog.go 白名单后运行 go test ./internal/backend/forwarder/ 校验。\n")
}
