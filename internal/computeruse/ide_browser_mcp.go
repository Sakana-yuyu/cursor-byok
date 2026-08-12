package computeruse

import (
	"context"
	"strings"
	"time"
)

// IDEBrowserExecutor 只适配 Cursor 内置 IDE 浏览器 MCP 的标签与锁语义。
type IDEBrowserExecutor struct {
	caller     MCPCaller
	scope      string
	startURL   string
	resolution BrowserMCPResolution
}

func NewIDEBrowserExecutor(caller MCPCaller, scope, startURL string, resolution BrowserMCPResolution) *IDEBrowserExecutor {
	if strings.TrimSpace(scope) == "" {
		scope = "user"
	}
	if strings.TrimSpace(startURL) == "" {
		startURL = "about:blank"
	}
	resolution.ToolNames = normalizeToolNames(resolution.ToolNames)
	return &IDEBrowserExecutor{caller: caller, scope: scope, startURL: startURL, resolution: resolution}
}

func (b *IDEBrowserExecutor) Execute(actions []Action) Result {
	started := time.Now()
	if len(actions) == 0 {
		return Result{Error: "no actions"}
	}
	if b.caller == nil || b.resolution.Profile != CursorIDEBrowserProfile || strings.TrimSpace(b.resolution.Identifier) == "" {
		return Result{Error: "browser_mcp_not_compatible"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if b.startURL != "about:blank" {
		if err := b.call(ctx, "browser_navigate", map[string]any{"url": b.startURL}); err != nil {
			return b.failure(started, 0, "ide_browser_action_failed")
		}
	}
	if err := b.call(ctx, "browser_tabs", map[string]any{"action": "list"}); err != nil {
		return b.failure(started, 0, "ide_browser_no_tab")
	}
	if err := b.call(ctx, "browser_lock", map[string]any{"action": "lock"}); err != nil {
		return b.failure(started, 0, "ide_browser_lock_failed")
	}
	defer b.call(context.Background(), "browser_lock", map[string]any{"action": "unlock"})

	for index, action := range actions {
		if errCode := b.executeAction(ctx, action); errCode != "" {
			return b.failure(started, index, errCode)
		}
	}
	var screenshot string
	if actions[len(actions)-1].Type == "screenshot" || shouldAutoScreenshot(actions) {
		image, err := b.takeScreenshot(ctx)
		if err != nil {
			return b.failure(started, len(actions), "ide_browser_action_failed")
		}
		screenshot = image
	}
	return Result{Success: true, ScreenshotBase64: screenshot, ActionCount: len(actions), DurationMs: time.Since(started).Milliseconds()}
}

func (b *IDEBrowserExecutor) executeAction(ctx context.Context, action Action) string {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "click":
		if err := b.call(ctx, "browser_snapshot", map[string]any{}); err != nil {
			return "ide_browser_snapshot_failed"
		}
		if _, err := b.takeScreenshot(ctx); err != nil {
			return "ide_browser_action_failed"
		}
		if err := b.call(ctx, "browser_mouse_click_xy", map[string]any{"x": action.X, "y": action.Y, "button": normalizeButton(action.Button)}); err != nil {
			return "ide_browser_action_failed"
		}
	case "type":
		if err := b.call(ctx, "browser_type", map[string]any{"text": action.Text}); err != nil {
			return "ide_browser_action_failed"
		}
	case "key":
		if err := b.call(ctx, "browser_press_key", map[string]any{"key": action.Key}); err != nil {
			return "ide_browser_action_failed"
		}
	case "scroll":
		if err := b.call(ctx, "browser_scroll", map[string]any{"direction": normalizeDirection(action.Direction), "amount": normalizeAmount(action.Amount)}); err != nil {
			return "ide_browser_action_failed"
		}
	case "wait":
		duration := action.DurationMs
		if duration <= 0 {
			duration = 100
		}
		time.Sleep(time.Duration(duration) * time.Millisecond)
	case "screenshot", "":
		return ""
	default:
		return "ide_browser_action_unmappable"
	}
	return ""
}

func (b *IDEBrowserExecutor) takeScreenshot(ctx context.Context) (string, error) {
	result, err := b.caller.CallTool(ctx, b.scope, b.resolution.Identifier, "browser_take_screenshot", map[string]any{"type": "png", "scale": "css"})
	if err != nil || result == nil || result.IsError || strings.TrimSpace(result.ImageBase64) == "" {
		return "", err
	}
	return result.ImageBase64, nil
}

func (b *IDEBrowserExecutor) call(ctx context.Context, tool string, args map[string]any) error {
	result, err := b.caller.CallTool(ctx, b.scope, b.resolution.Identifier, tool, args)
	if err != nil {
		return err
	}
	if result == nil || result.IsError {
		return context.Canceled
	}
	return nil
}

func (b *IDEBrowserExecutor) failure(started time.Time, actionCount int, code string) Result {
	return Result{ActionCount: actionCount, DurationMs: time.Since(started).Milliseconds(), Error: code}
}

type unavailableExecutor struct{ code string }

func NewUnavailableExecutor(code string) Executor { return unavailableExecutor{code: code} }

func (e unavailableExecutor) Execute([]Action) Result { return Result{Error: e.code} }
