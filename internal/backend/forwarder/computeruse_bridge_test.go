package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
	"cursor/internal/computeruse"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResolveBrowserServerPrefersCursorIDEProfile(t *testing.T) {
	runtime := NewMCPRuntimeRegistry()
	runtime.ReplaceScope("user", []MCPServerConfig{{
		Identifier:   "cursor-ide-browser",
		Name:         "Cursor IDE Browser",
		RuntimeScope: "user",
	}})
	entry := runtime.entries[mcpRuntimeEntryKey("user", "cursor-ide-browser")]
	entry.status = MCPRuntimeConnected
	entry.session = &mcp.ClientSession{}
	entry.tools = []*agentv1.McpToolDescriptor{
		{ToolName: "browser_tabs"}, {ToolName: "browser_lock"}, {ToolName: "browser_snapshot"},
		{ToolName: "browser_mouse_click_xy"}, {ToolName: "browser_take_screenshot"},
	}
	adapter := &mcpCallerAdapter{runtime: runtime}
	resolution, err := adapter.ResolveBrowserServer("user")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Profile != computeruse.CursorIDEBrowserProfile {
		t.Fatalf("profile = %q", resolution.Profile)
	}
}
