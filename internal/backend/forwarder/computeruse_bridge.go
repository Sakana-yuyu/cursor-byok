// computeruse_bridge.go 把 agentv1.ComputerUseAction（proto oneof）转换为 computeruse.Action，
// 本地执行后构造合成 ExecClientMessage，供 forwarder 注入 handleExecResult。
// 与 service.go 的 exec 派发主链路解耦，集中处理 ComputerUse 本地执行的桥接细节。
package forwarder

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/computeruse"
	"cursor/internal/logger"
	"cursor/internal/safego"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// convertComputerUseActions 把 proto 动作列表转为本地执行器可消费的归一化动作。
func convertComputerUseActions(actions []*agentv1.ComputerUseAction) []computeruse.Action {
	out := make([]computeruse.Action, 0, len(actions))
	for _, action := range actions {
		if action == nil {
			continue
		}
		out = append(out, convertOneComputerUseAction(action))
	}
	return out
}

func convertOneComputerUseAction(action *agentv1.ComputerUseAction) computeruse.Action {
	base := computeruse.Action{Type: "screenshot"}
	switch item := action.GetAction().(type) {
	case *agentv1.ComputerUseAction_MouseMove:
		c := item.MouseMove.GetCoordinate()
		base.Type = "mouse_move"
		base.X, base.Y = int(c.GetX()), int(c.GetY())
	case *agentv1.ComputerUseAction_Click:
		c := item.Click.GetCoordinate()
		base.Type = "click"
		base.X, base.Y = int(c.GetX()), int(c.GetY())
		base.Button = mouseButtonToString(item.Click.GetButton())
		base.Count = int(item.Click.GetCount())
	case *agentv1.ComputerUseAction_MouseDown:
		base.Type = "mouse_down"
		base.Button = mouseButtonToString(item.MouseDown.GetButton())
	case *agentv1.ComputerUseAction_MouseUp:
		base.Type = "mouse_up"
		base.Button = mouseButtonToString(item.MouseUp.GetButton())
	case *agentv1.ComputerUseAction_Drag:
		base.Type = "drag"
		for _, c := range item.Drag.GetPath() {
			base.Path = append(base.Path, computeruse.Point{X: int(c.GetX()), Y: int(c.GetY())})
		}
	case *agentv1.ComputerUseAction_Scroll:
		c := item.Scroll.GetCoordinate()
		base.Type = "scroll"
		base.X, base.Y = int(c.GetX()), int(c.GetY())
		base.Direction = scrollDirectionToString(item.Scroll.GetDirection())
		base.Amount = int(item.Scroll.GetAmount())
	case *agentv1.ComputerUseAction_Type:
		base.Type = "type"
		base.Text = item.Type.GetText()
	case *agentv1.ComputerUseAction_Key:
		base.Type = "key"
		base.Key = item.Key.GetKey()
		base.DurationMs = int(item.Key.GetHoldDurationMs())
	case *agentv1.ComputerUseAction_Wait:
		base.Type = "wait"
		base.DurationMs = int(item.Wait.GetDurationMs())
	case *agentv1.ComputerUseAction_Screenshot:
		base.Type = "screenshot"
	case *agentv1.ComputerUseAction_CursorPosition:
		base.Type = "cursor_position"
	}
	return base
}

func mouseButtonToString(b agentv1.MouseButton) string {
	switch b {
	case agentv1.MouseButton_MOUSE_BUTTON_RIGHT:
		return "right"
	case agentv1.MouseButton_MOUSE_BUTTON_MIDDLE:
		return "middle"
	default:
		return "left"
	}
}

func scrollDirectionToString(d agentv1.ScrollDirection) string {
	switch d {
	case agentv1.ScrollDirection_SCROLL_DIRECTION_UP:
		return "up"
	case agentv1.ScrollDirection_SCROLL_DIRECTION_LEFT:
		return "left"
	case agentv1.ScrollDirection_SCROLL_DIRECTION_RIGHT:
		return "right"
	default:
		return "down"
	}
}

// mcpCallerAdapter 把 MCPRuntimeRegistry 适配为 computeruse.MCPCaller 接口，
// 让 computeruse 包不直接依赖 forwarder.MCPRuntimeRegistry（避免循环依赖）。
type mcpCallerAdapter struct {
	runtime *MCPRuntimeRegistry
}

func (a *mcpCallerAdapter) CallTool(ctx context.Context, scope, identifier, toolName string, args map[string]any) (*computeruse.MCPToolResult, error) {
	if a.runtime == nil {
		return nil, fmt.Errorf("MCP runtime 未初始化")
	}
	result, err := a.runtime.CallTool(ctx, scope, identifier, toolName, args)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &computeruse.MCPToolResult{}, nil
	}
	out := &computeruse.MCPToolResult{IsError: result.IsError}
	for _, content := range result.Content {
		switch content := content.(type) {
		case *mcp.ImageContent:
			out.ImageBase64 = string(content.Data)
		case *mcp.TextContent:
			if out.Text != "" {
				out.Text += "\n"
			}
			out.Text += content.Text
		}
	}
	return out, nil
}

func (a *mcpCallerAdapter) FindBrowserServer(scope string) (string, bool) {
	if a.runtime == nil {
		return "", false
	}
	for _, snap := range a.runtime.Snapshot(scope) {
		name := strings.ToLower(snap.Identifier + " " + snap.Name)
		if strings.Contains(name, "playwright") || strings.Contains(name, "browser") {
			if snap.Status == MCPRuntimeConnected {
				return snap.Identifier, true
			}
		}
	}
	return "", false
}

// ResolveBrowserServer 只根据已连接 MCP 的 tools/list 描述符选择浏览器协议。
// 不依据名称模糊匹配，避免把不兼容服务错误地用于 ComputerUse。
func (a *mcpCallerAdapter) ResolveBrowserServer(scope string) (computeruse.BrowserMCPResolution, error) {
	if a.runtime == nil {
		return computeruse.BrowserMCPResolution{}, fmt.Errorf("browser_mcp_not_connected")
	}

	var coordinate []computeruse.BrowserMCPResolution
	for _, snapshot := range a.runtime.Snapshot(scope) {
		if snapshot.Status != MCPRuntimeConnected {
			continue
		}
		descriptor, ok := a.runtime.Descriptor(scope, snapshot.Identifier)
		if !ok {
			continue
		}
		toolNames := make([]string, 0, len(descriptor.GetTools()))
		for _, tool := range descriptor.GetTools() {
			if tool != nil {
				toolNames = append(toolNames, tool.GetToolName())
			}
		}
		profile, err := computeruse.ResolveBrowserProfile(snapshot.Identifier, toolNames)
		if err != nil {
			continue
		}
		resolution := computeruse.BrowserMCPResolution{
			Identifier: snapshot.Identifier,
			Profile:    profile,
			ToolNames:  toolNames,
		}
		if profile == computeruse.CursorIDEBrowserProfile {
			return resolution, nil
		}
		coordinate = append(coordinate, resolution)
	}
	if len(coordinate) == 1 {
		return coordinate[0], nil
	}
	if len(coordinate) > 1 {
		return computeruse.BrowserMCPResolution{}, fmt.Errorf("browser_mcp_ambiguous")
	}
	return computeruse.BrowserMCPResolution{}, fmt.Errorf("browser_mcp_not_compatible")
}

// resolveComputerUseExecutor 按当前配置选择执行后端。
// desktop=DesktopExecutor（Win32）；browser=MCPBrowserExecutor（转发到浏览器 MCP server）。
func (service *Service) resolveComputerUseExecutor() computeruse.Executor {
	mode := "desktop"
	startURL := "about:blank"
	if service != nil && service.computerUseCfg != nil {
		cfgMode, cfgURL := service.computerUseCfg.ComputerUseMode()
		mode = strings.ToLower(strings.TrimSpace(cfgMode))
		startURL = strings.TrimSpace(cfgURL)
		if startURL == "" {
			startURL = "about:blank"
		}
	}
	if mode == "browser" {
		caller := &mcpCallerAdapter{runtime: service.mcpRuntime}
		resolution, err := caller.ResolveBrowserServer("user")
		if err != nil {
			return computeruse.NewUnavailableExecutor(err.Error())
		}
		switch resolution.Profile {
		case computeruse.CursorIDEBrowserProfile:
			return computeruse.NewIDEBrowserExecutor(caller, "user", startURL, resolution)
		case computeruse.CoordinateBrowserProfile:
			return computeruse.NewMCPBrowserExecutorForServer(caller, "user", startURL, resolution.Identifier)
		default:
			return computeruse.NewUnavailableExecutor("browser_mcp_not_compatible")
		}
	}
	return computeruse.DesktopExecutor{}
}

// maybeDispatchLocalComputerUse 在 ComputerUse 工具派发后，若当前平台支持且 ExecKind
// 为 computer_use，则本地执行动作并把合成结果注入 stream（不依赖 Cursor 客户端回传）。
// 调用点：handleToolInvocation 在注册 pending exec 之后、broker.Publish 之前。
// 返回 true 表示已由本地执行接管（但仍会发送 ExecServerMessage 兼容官方客户端，无害）。
func (service *Service) maybeDispatchLocalComputerUse(
	requestID string,
	pending runtimecore.PendingExec,
	argsJSON []byte,
) bool {
	if strings.TrimSpace(pending.ExecKind) != "computer_use" {
		return false
	}
	if runtime.GOOS != "windows" {
		return false
	}
	// 解码动作（复用 exec bridge 的宽松解析）。
	actions, err := execbridge.DecodeComputerUseActionsForLocal(argsJSON)
	if err != nil {
		logger.Errorf("local computer use decode actions failed exec_id=%s error=%v", pending.ExecID, err)
		return false
	}
	if len(actions) == 0 {
		return false
	}

	// 按配置选择执行后端（desktop/browser），在 goroutine 中执行。
	executor := service.resolveComputerUseExecutor()
	dispatchResult := func(result computeruse.Result) {
		service.dispatchInboundIntent(InboundIntent{
			Kind:              "exec_result",
			RequestID:         requestID,
			ExecClientMessage: buildSyntheticExecClientMessageFromResult(pending, result),
		})
	}
	// panic 兜底必须回填失败终态：这个 goroutine 是该 exec 唯一的结果来源，
	// 崩掉且不回填的话这次工具调用会永久 pending，Cursor 侧表现为一直等待。
	safego.GoWithPanicHandler("forwarder:local-computer-use", func() {
		dispatchResult(executor.Execute(convertComputerUseActions(actions)))
	}, func(error) {
		dispatchResult(computeruse.Result{Error: "local computer use panicked"})
	})
	logger.Infof("local computer use dispatched locally exec_id=%s actions=%d", pending.ExecID, len(actions))
	return true
}

// buildSyntheticExecClientMessageFromResult 由 Execute 结果构造合成 ExecClientMessage。
func buildSyntheticExecClientMessageFromResult(pending runtimecore.PendingExec, result computeruse.Result) *agentv1.ExecClientMessage {
	if !result.Success {
		errResult := &agentv1.ComputerUseError{Error: result.Error}
		if strings.TrimSpace(result.Log) != "" {
			errResult.Log = proto.String(result.Log)
		}
		return &agentv1.ExecClientMessage{
			Id:     pending.MessageID,
			ExecId: pending.ExecID,
			Message: &agentv1.ExecClientMessage_ComputerUseResult{
				ComputerUseResult: &agentv1.ComputerUseResult{
					Result: &agentv1.ComputerUseResult_Error{Error: errResult},
				},
			},
		}
	}

	success := &agentv1.ComputerUseSuccess{
		ActionCount: int32(result.ActionCount),
		DurationMs:  int32(result.DurationMs),
	}
	if strings.TrimSpace(result.ScreenshotBase64) != "" {
		success.Screenshot = proto.String(result.ScreenshotBase64)
	}
	if strings.TrimSpace(result.Log) != "" {
		success.Log = proto.String(result.Log)
	}
	return &agentv1.ExecClientMessage{
		Id:     pending.MessageID,
		ExecId: pending.ExecID,
		Message: &agentv1.ExecClientMessage_ComputerUseResult{
			ComputerUseResult: &agentv1.ComputerUseResult{
				Result: &agentv1.ComputerUseResult_Success{Success: success},
			},
		},
	}
}
