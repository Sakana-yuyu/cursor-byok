package computeruse

import (
	"fmt"
	"sort"
	"strings"
)

type BrowserMCPProfile string

const (
	CursorIDEBrowserProfile  BrowserMCPProfile = "cursor_ide_browser"
	CoordinateBrowserProfile BrowserMCPProfile = "coordinate_browser"
)

// BrowserMCPResolution 是仅由运行时 tools/list 描述符生成的浏览器执行选择。
type BrowserMCPResolution struct {
	Identifier string
	Profile    BrowserMCPProfile
	ToolNames  []string
}

// BrowserServerResolver 由拥有 MCP tools/list descriptors 的运行时实现。
// 它是可选接口，保留旧 MCPCaller 的调用兼容性。
type BrowserServerResolver interface {
	ResolveBrowserServer(scope string) (BrowserMCPResolution, error)
}

var cursorIDEBrowserRequiredTools = []string{
	"browser_tabs",
	"browser_lock",
	"browser_snapshot",
	"browser_mouse_click_xy",
	"browser_take_screenshot",
}

var coordinateBrowserRequiredTools = []string{
	"browser_mouse_click_xy",
	"browser_press_key",
	"browser_wait_for",
	"browser_take_screenshot",
}

// ResolveBrowserProfile 按工具集合而不是服务显示名判定可执行协议。
func ResolveBrowserProfile(identifier string, toolNames []string) (BrowserMCPProfile, error) {
	identifier = strings.TrimSpace(identifier)
	tools := make(map[string]struct{}, len(toolNames))
	for _, tool := range toolNames {
		if tool = strings.TrimSpace(tool); tool != "" {
			tools[tool] = struct{}{}
		}
	}
	if identifier == "cursor-ide-browser" {
		if hasTools(tools, cursorIDEBrowserRequiredTools) {
			return CursorIDEBrowserProfile, nil
		}
		return "", fmt.Errorf("browser_mcp_profile_incomplete")
	}
	if hasTools(tools, coordinateBrowserRequiredTools) {
		return CoordinateBrowserProfile, nil
	}
	return "", fmt.Errorf("browser_mcp_profile_incomplete")
}

func hasTools(available map[string]struct{}, required []string) bool {
	for _, name := range required {
		if _, found := available[name]; !found {
			return false
		}
	}
	return true
}

func normalizeToolNames(toolNames []string) []string {
	items := make(map[string]struct{}, len(toolNames))
	for _, name := range toolNames {
		if name = strings.TrimSpace(name); name != "" {
			items[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(items))
	for name := range items {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
