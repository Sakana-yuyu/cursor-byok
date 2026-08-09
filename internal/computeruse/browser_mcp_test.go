package computeruse

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// fakeMCPCaller 记录所有 MCP 调用，供断言映射行为。
type fakeMCPCaller struct {
	mu        sync.Mutex
	calls     []mcpCallRecord
	findOK    bool
	findID    string
	imgBase64 string
}

type mcpCallRecord struct {
	tool string
	args map[string]any
}

// newFakeMCPCaller 创建带默认截图值的 fake：末尾自动截图需要返回图片才算成功。
func newFakeMCPCaller() *fakeMCPCaller {
	return &fakeMCPCaller{findOK: true, findID: "playwright", imgBase64: "mock-png-base64"}
}

func (f *fakeMCPCaller) CallTool(_ context.Context, scope, identifier, toolName string, args map[string]any) (*MCPToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, mcpCallRecord{tool: toolName, args: args})
	if toolName == "browser_take_screenshot" {
		return &MCPToolResult{ImageBase64: f.imgBase64, Text: "screenshot"}, nil
	}
	return &MCPToolResult{Text: "ok"}, nil
}

func (f *fakeMCPCaller) FindBrowserServer(_ string) (string, bool) {
	return f.findID, f.findOK
}

func (f *fakeMCPCaller) toolCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		names = append(names, c.tool)
	}
	return names
}

func (f *fakeMCPCaller) findTool(tool string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.tool == tool {
			return c.args, true
		}
	}
	return nil, false
}

func TestMCPBrowserExecutor_NoBrowserServer(t *testing.T) {
	ex := NewMCPBrowserExecutor(&fakeMCPCaller{findOK: false}, "user", "http://localhost:5173")
	r := ex.Execute([]Action{{Type: "click", X: 10, Y: 20}})
	if r.Success {
		t.Fatal("expected failure when no browser MCP server configured")
	}
	if !strings.Contains(r.Error, "未找到浏览器 MCP server") {
		t.Fatalf("expected server-not-found error, got: %q", r.Error)
	}
}

func TestMCPBrowserExecutor_NilCaller(t *testing.T) {
	ex := NewMCPBrowserExecutor(nil, "user", "")
	r := ex.Execute([]Action{{Type: "click", X: 10, Y: 20}})
	if r.Success {
		t.Fatal("expected failure when caller is nil")
	}
}

func TestMCPBrowserExecutor_EmptyActions(t *testing.T) {
	ex := NewMCPBrowserExecutor(newFakeMCPCaller(), "user", "")
	r := ex.Execute(nil)
	if r.Success {
		t.Fatal("expected failure for empty actions")
	}
}

func TestMCPBrowserExecutor_ActionMapping(t *testing.T) {
	fake := newFakeMCPCaller()
	ex := NewMCPBrowserExecutor(fake, "user", "http://localhost:5173")
	r := ex.Execute([]Action{
		{Type: "click", X: 10, Y: 20, Button: "left"},
		{Type: "type", Text: "hello"},
		{Type: "key", Key: "ctrl+shift+t"},
		{Type: "wait", DurationMs: 500},
	})
	if !r.Success {
		t.Fatalf("expected success, got error: %s", r.Error)
	}
	if r.ActionCount != 4 {
		t.Fatalf("expected 4 actions, got %d", r.ActionCount)
	}

	// 首次执行先导航到 startURL，末尾自动截图。
	want := []string{
		"browser_navigate",
		"browser_mouse_click_xy",
		"browser_type",
		"browser_press_key",
		"browser_wait_for",
		"browser_take_screenshot",
	}
	got := fake.toolCalls()
	if len(got) != len(want) {
		t.Fatalf("expected calls %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d: expected %s, got %s", i, want[i], got[i])
		}
	}

	if args, ok := fake.findTool("browser_mouse_click_xy"); !ok {
		t.Fatal("missing browser_mouse_click_xy call")
	} else if args["x"] != 10 || args["y"] != 20 || args["button"] != "left" {
		t.Fatalf("unexpected click args: %v", args)
	}
	if args, ok := fake.findTool("browser_type"); !ok {
		t.Fatal("missing browser_type call")
	} else if args["text"] != "hello" {
		t.Fatalf("unexpected type args: %v", args)
	}
	if args, ok := fake.findTool("browser_press_key"); !ok {
		t.Fatal("missing browser_press_key call")
	} else if args["key"] != "Control+Shift+T" {
		t.Fatalf("expected combined key 'Control+Shift+T', got %v", args["key"])
	}
	if args, ok := fake.findTool("browser_navigate"); !ok {
		t.Fatal("missing browser_navigate call")
	} else if args["url"] != "http://localhost:5173" {
		t.Fatalf("unexpected navigate args: %v", args)
	}
}

func TestMCPBrowserExecutor_NoNavigateForAboutBlank(t *testing.T) {
	fake := newFakeMCPCaller()
	ex := NewMCPBrowserExecutor(fake, "user", "")
	r := ex.Execute([]Action{{Type: "click", X: 1, Y: 2}})
	if !r.Success {
		t.Fatalf("expected success, got: %s", r.Error)
	}
	if _, ok := fake.findTool("browser_navigate"); ok {
		t.Fatal("must not navigate when startURL is about:blank")
	}
}

func TestMCPBrowserExecutor_ScreenshotFromImage(t *testing.T) {
	fake := newFakeMCPCaller()
	fake.imgBase64 = "iVBORw0KGgoAAAANSUhEUg=="
	ex := NewMCPBrowserExecutor(fake, "user", "")
	r := ex.Execute([]Action{{Type: "click", X: 1, Y: 2}})
	if !r.Success {
		t.Fatalf("expected success, got: %s", r.Error)
	}
	if r.ScreenshotBase64 != "iVBORw0KGgoAAAANSUhEUg==" {
		t.Fatalf("expected screenshot base64 from ImageContent, got %q", r.ScreenshotBase64)
	}
}

func TestMCPBrowserExecutor_ScrollDirection(t *testing.T) {
	fake := newFakeMCPCaller()
	ex := NewMCPBrowserExecutor(fake, "user", "")
	r := ex.Execute([]Action{{Type: "scroll", X: 5, Y: 5, Direction: "up", Amount: 3}})
	if !r.Success {
		t.Fatalf("expected success, got: %s", r.Error)
	}
	args, ok := fake.findTool("browser_mouse_wheel")
	if !ok {
		t.Fatal("missing browser_mouse_wheel call")
	}
	if args["deltaY"] != float64(-300) {
		t.Fatalf("expected deltaY -300 for scroll up, got %v", args["deltaY"])
	}
}

func TestVKToCDPKeyName(t *testing.T) {
	cases := map[uint16]string{
		0x0D: "Enter",
		0x11: "Control",
		0x12: "Alt",
		0x10: "Shift",
		0x5B: "Meta",
		0x09: "Tab",
		0x1B: "Escape",
		0x08: "Backspace",
		0x2E: "Delete",
		0x2D: "Insert",
		0x24: "Home",
		0x23: "End",
		0x21: "PageUp",
		0x22: "PageDown",
		0x26: "ArrowUp",
		0x28: "ArrowDown",
		0x25: "ArrowLeft",
		0x27: "ArrowRight",
		0x20: "Space",
		0x70: "F1",
		0x7B: "F12",
		'A':  "A",
		'9':  "9",
	}
	for vk, want := range cases {
		if got := vkToCDPKeyName(vk); got != want {
			t.Errorf("vkToCDPKeyName(0x%X): expected %q, got %q", vk, want, got)
		}
	}
	if got := vkToCDPKeyName(0x9999); got != "" {
		t.Errorf("unknown vk: expected empty, got %q", got)
	}
}
