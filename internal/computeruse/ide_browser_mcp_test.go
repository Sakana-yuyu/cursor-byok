package computeruse

import (
	"context"
	"sync"
	"testing"
)

func TestResolveBrowserProfilePrefersCursorIDEProfile(t *testing.T) {
	profile, err := ResolveBrowserProfile("cursor-ide-browser", []string{
		"browser_tabs", "browser_lock", "browser_snapshot",
		"browser_mouse_click_xy", "browser_take_screenshot",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile != CursorIDEBrowserProfile {
		t.Fatalf("profile = %q, want %q", profile, CursorIDEBrowserProfile)
	}
}

func TestResolveBrowserProfileRejectsNameOnlyMatch(t *testing.T) {
	if _, err := ResolveBrowserProfile("browser-helper", []string{"browser_tabs"}); err == nil {
		t.Fatal("expected incomplete browser profile to be rejected")
	}
}

func TestIDEBrowserExecutorLocksClicksAndUnlocks(t *testing.T) {
	caller := &ideBrowserFakeCaller{imageBase64: "mock-png-base64"}
	executor := NewIDEBrowserExecutor(caller, "user", "about:blank", BrowserMCPResolution{
		Identifier: "cursor-ide-browser",
		Profile:    CursorIDEBrowserProfile,
	})
	result := executor.Execute([]Action{{Type: "click", X: 10, Y: 20}})
	if !result.Success {
		t.Fatal(result.Error)
	}
	assertToolOrder(t, caller.toolCalls(), []string{
		"browser_tabs", "browser_lock", "browser_snapshot", "browser_take_screenshot",
		"browser_mouse_click_xy", "browser_take_screenshot", "browser_lock",
	})
}

func TestIDEBrowserExecutorRejectsUnmappableDrag(t *testing.T) {
	caller := &ideBrowserFakeCaller{imageBase64: "mock-png-base64"}
	executor := NewIDEBrowserExecutor(caller, "user", "about:blank", BrowserMCPResolution{
		Identifier: "cursor-ide-browser",
		Profile:    CursorIDEBrowserProfile,
	})
	result := executor.Execute([]Action{{Type: "drag", Path: []Point{{X: 1, Y: 1}, {X: 2, Y: 2}}}})
	if result.Success || result.Error != "ide_browser_action_unmappable" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

type ideBrowserFakeCaller struct {
	mu          sync.Mutex
	calls       []mcpCallRecord
	imageBase64 string
}

func (f *ideBrowserFakeCaller) CallTool(_ context.Context, _ string, _ string, toolName string, args map[string]any) (*MCPToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, mcpCallRecord{tool: toolName, args: args})
	if toolName == "browser_take_screenshot" {
		return &MCPToolResult{ImageBase64: f.imageBase64}, nil
	}
	return &MCPToolResult{Text: "ok"}, nil
}

func (f *ideBrowserFakeCaller) FindBrowserServer(string) (string, bool) {
	return "cursor-ide-browser", true
}

func (f *ideBrowserFakeCaller) toolCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tools := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		tools = append(tools, call.tool)
	}
	return tools
}

func assertToolOrder(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tool calls = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("tool call %d = %q, want %q", index, got[index], want[index])
		}
	}
}
