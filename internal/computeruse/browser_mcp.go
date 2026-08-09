// browser_mcp.go 实现 MCP 转发版浏览器执行器：ComputerUse 的浏览器模式不自建浏览器引擎，
// 而是把 action 转发为 MCP 工具调用（如 Playwright MCP），复用项目已有的 MCP 运行时。
// computeruse 包通过 MCPCaller 接口与 MCP 运行时解耦，避免直接依赖 forwarder 包。
package computeruse

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MCPCaller 抽象 MCP 工具调用能力，由 forwarder 层实现 adapter 注入。
// 这样 computeruse 包不直接依赖 forwarder.MCPRuntimeRegistry（避免循环依赖）。
type MCPCaller interface {
	// CallTool 调用指定 MCP server 的工具，返回结果。
	CallTool(ctx context.Context, scope, identifier, toolName string, args map[string]any) (*MCPToolResult, error)
	// FindBrowserServer 查找已连接的浏览器 MCP server（identifier/name 含 playwright 或 browser）。
	FindBrowserServer(scope string) (identifier string, ok bool)
}

// MCPToolResult 是 MCP 工具调用的归一化结果（从 mcp.CallToolResult.Content 提取）。
type MCPToolResult struct {
	ImageBase64 string // 从 ImageContent 提取的截图 base64（无 data: 前缀）
	Text        string // 从 TextContent 提取的文本
	IsError     bool
}

// MCPBrowserExecutor 通过 MCP server（如 Playwright MCP）执行 ComputerUse 动作。
// 无状态：MCP 连接由 MCPCaller（底层 mcpRuntime）管理，执行器本身不持有连接。
type MCPBrowserExecutor struct {
	caller   MCPCaller
	scope    string
	startURL string
}

// NewMCPBrowserExecutor 创建 MCP 浏览器执行器。caller 由 forwarder 注入。
func NewMCPBrowserExecutor(caller MCPCaller, scope, startURL string) *MCPBrowserExecutor {
	if strings.TrimSpace(scope) == "" {
		scope = "user"
	}
	if strings.TrimSpace(startURL) == "" {
		startURL = "about:blank"
	}
	return &MCPBrowserExecutor{caller: caller, scope: scope, startURL: startURL}
}

// Execute 实现 Executor：通过 MCP server 执行动作序列。
func (b *MCPBrowserExecutor) Execute(actions []Action) Result {
	if len(actions) == 0 {
		return Result{Error: "no actions"}
	}
	if b.caller == nil {
		return Result{Error: "MCP caller 未注入"}
	}

	identifier, ok := b.caller.FindBrowserServer(b.scope)
	if !ok {
		return Result{Error: "未找到浏览器 MCP server，请在 mcpServers 配置中添加 Playwright MCP server（启动参数建议含 --caps=vision 以支持坐标操作）"}
	}

	start := time.Now()
	var logLines []string

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 首次执行前导航到初始 URL。
	navigated := false

	for i, action := range actions {
		if !navigated && b.startURL != "about:blank" {
			if _, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_navigate", map[string]any{
				"url": b.startURL,
			}); err != nil {
				return Result{Error: fmt.Sprintf("browser_navigate(%s) failed: %v", b.startURL, err)}
			}
			navigated = true
		}
		if err := b.executeAction(ctx, identifier, action); err != nil {
			return Result{
				Success:     false,
				ActionCount: i,
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       fmt.Sprintf("action %d (%s) failed: %v", i, action.Type, err),
				Log:         strings.Join(logLines, "\n"),
			}
		}
		logLines = append(logLines, fmt.Sprintf("[%d] %s ok", i, describeAction(action)))
	}

	// 末尾截图（与桌面 executor 一致）。
	var screenshot string
	last := actions[len(actions)-1]
	if last.Type == "screenshot" || shouldAutoScreenshot(actions) {
		png, err := b.captureScreenshot(ctx, identifier)
		if err != nil {
			return Result{
				Success:     false,
				ActionCount: len(actions),
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       fmt.Sprintf("actions ok but screenshot failed: %v", err),
				Log:         strings.Join(logLines, "\n"),
			}
		}
		screenshot = png
	}

	return Result{
		Success:          true,
		ScreenshotBase64: screenshot,
		ActionCount:      len(actions),
		DurationMs:       time.Since(start).Milliseconds(),
		Log:              strings.Join(logLines, "\n"),
	}
}

// executeAction 把单个 ComputerUse action 映射到 MCP 工具调用。
func (b *MCPBrowserExecutor) executeAction(ctx context.Context, identifier string, action Action) error {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "mouse_move", "move":
		_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_move_xy", map[string]any{
			"x": action.X, "y": action.Y,
		})
		return err
	case "click":
		_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_click_xy", map[string]any{
			"x": action.X, "y": action.Y,
			"button": normalizeButton(action.Button),
		})
		return err
	case "mouse_down", "mouse_up":
		// Playwright MCP 无独立 down/up，坐标点击已包含按下+抬起。
		return nil
	case "drag":
		return b.doDrag(ctx, identifier, action)
	case "scroll":
		deltaY := float64(normalizeAmount(action.Amount) * 100)
		if normalizeDirection(action.Direction) == "up" {
			deltaY = -deltaY
		}
		_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_wheel", map[string]any{
			"x": action.X, "y": action.Y, "deltaX": 0, "deltaY": deltaY,
		})
		return err
	case "type":
		_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_type", map[string]any{
			"text": action.Text,
		})
		return err
	case "key":
		return b.doKey(ctx, identifier, action.Key)
	case "wait":
		d := action.DurationMs
		if d <= 0 {
			d = 100
		}
		_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_wait_for", map[string]any{
			"time": float64(d) / 1000.0,
		})
		return err
	case "screenshot", "":
		return nil
	case "cursor_position":
		return nil
	default:
		return fmt.Errorf("unsupported action type: %s", action.Type)
	}
}

func (b *MCPBrowserExecutor) doDrag(ctx context.Context, identifier string, action Action) error {
	if len(action.Path) < 1 {
		_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_click_xy", map[string]any{
			"x": action.X, "y": action.Y, "button": normalizeButton(action.Button),
		})
		return err
	}
	start := action.Path[0]
	if _, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_move_xy", map[string]any{
		"x": start.X, "y": start.Y,
	}); err != nil {
		return err
	}
	if _, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_down_xy", map[string]any{
		"x": start.X, "y": start.Y, "button": normalizeButton(action.Button),
	}); err != nil {
		return err
	}
	for _, pt := range action.Path[1:] {
		if _, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_move_xy", map[string]any{
			"x": pt.X, "y": pt.Y,
		}); err != nil {
			return err
		}
	}
	last := action.Path[len(action.Path)-1]
	_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_mouse_up_xy", map[string]any{
		"x": last.X, "y": last.Y, "button": normalizeButton(action.Button),
	})
	return err
}

func (b *MCPBrowserExecutor) doKey(ctx context.Context, identifier, key string) error {
	// 组合键拼接：ctrl+shift+t -> "Control+Shift+T"，Playwright Keyboard.press 一次支持组合。
	keys := parseKeyCombo(key)
	if len(keys) == 0 {
		return fmt.Errorf("无法解析按键: %q", key)
	}
	names := make([]string, 0, len(keys))
	for _, vk := range keys {
		keyName := vkToCDPKeyName(vk)
		if keyName == "" {
			return fmt.Errorf("无法映射按键 vk=0x%X 到 MCP key name", vk)
		}
		names = append(names, keyName)
	}
	_, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_press_key", map[string]any{
		"key": strings.Join(names, "+"),
	})
	return err
}

// captureScreenshot 调 browser_take_screenshot，从 ImageContent 提取 base64。
func (b *MCPBrowserExecutor) captureScreenshot(ctx context.Context, identifier string) (string, error) {
	result, err := b.caller.CallTool(ctx, b.scope, identifier, "browser_take_screenshot", map[string]any{
		"type":  "png",
		"scale": "css",
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("browser_take_screenshot 返回空结果")
	}
	if result.IsError {
		return "", fmt.Errorf("browser_take_screenshot 报错: %s", result.Text)
	}
	if strings.TrimSpace(result.ImageBase64) == "" {
		return "", fmt.Errorf("browser_take_screenshot 未返回图片（可能 MCP server 未支持截图，或需 --caps=vision）")
	}
	return result.ImageBase64, nil
}
