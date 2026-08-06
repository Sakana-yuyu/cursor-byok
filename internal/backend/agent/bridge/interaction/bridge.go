// bridge.go 实现 MVP 阶段的交互桥协议映射。
package interaction

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	"cursor/internal/backend/agent/core"
	"cursor/internal/netproxy"
)

// InteractionApplyResult 表示一次交互桥结果归一化后的最小产物。
type InteractionApplyResult struct {
	// ToolCallID 表示该结果所属工具调用标识。
	ToolCallID string
	// InteractionID 表示该结果所属交互桥标识。
	InteractionID string
	// IsTerminal 表示交互桥是否已经收口。
	IsTerminal bool
	// ToolResultPayload 表示可继续喂给模型的结果摘要。
	ToolResultPayload string
	// ToolCall 保存可用于发 ToolCallCompletedUpdate 的工具调用对象。
	ToolCall *agentv1.ToolCall
}

// InteractionBridge 定义交互桥接口。
type InteractionBridge interface {
	// OpenQuery 打开一条交互型工具调用。
	OpenQuery(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error)
	// ApplyInteractionResponse 处理交互响应。
	ApplyInteractionResponse(msg *agentv1.InteractionResponse, pending runtimecore.PendingInteraction) (InteractionApplyResult, error)
}

// Bridge 实现 MVP 阶段的交互桥。
type Bridge struct {
	// nextID 生成交互消息编号。
	nextID atomic.Uint32
	// httpClient 负责执行 web search / web fetch 等需要外网的操作。
	httpClient *http.Client
}

// NewBridge 创建一个交互桥实例。
func NewBridge() *Bridge {
	return &Bridge{
		httpClient: netproxy.NewHTTPClient(15 * time.Second),
	}
}

// OpenQuery 打开一条交互型工具调用。
func (bridge *Bridge) OpenQuery(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	switch toolCall.ToolName {
	case "AskQuestion":
		return bridge.openAskQuestion(toolCall)
	case "CreatePlan":
		return bridge.openCreatePlan(toolCall)
	case "WebSearch":
		return bridge.openWebSearch(toolCall)
	case "WebFetch":
		return bridge.openWebFetch(toolCall)
	case "SwitchMode":
		return bridge.openSwitchMode(toolCall)
	case "CreatePr":
		return bridge.openPrManagement(toolCall, "create")
	case "UpdatePr":
		return bridge.openPrManagement(toolCall, "update")
	default:
		return nil, runtimecore.PendingInteraction{}, fmt.Errorf("unsupported interaction tool: %s", toolCall.ToolName)
	}
}

// ApplyInteractionResponse 处理交互响应。
func (bridge *Bridge) ApplyInteractionResponse(msg *agentv1.InteractionResponse, pending runtimecore.PendingInteraction) (InteractionApplyResult, error) {
	if msg == nil {
		return InteractionApplyResult{}, fmt.Errorf("interaction response is required")
	}

	result := InteractionApplyResult{
		ToolCallID:    pending.ToolCallID,
		InteractionID: pending.InteractionID,
		IsTerminal:    true,
	}
	switch pending.InteractionKind {
	case "ask_question":
		var args agentv1.AskQuestionArgs
		_ = json.Unmarshal(pending.ArgsJSON, &args)
		result.ToolResultPayload = summarizeAskQuestionResponse(msg.GetAskQuestionInteractionResponse())
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_AskQuestionToolCall{
				AskQuestionToolCall: &agentv1.AskQuestionToolCall{
					Args:   &args,
					Result: msg.GetAskQuestionInteractionResponse().GetResult(),
				},
			},
		}
		return result, nil
	case "create_plan":
		args, err := runtimecore.DecodeCreatePlanArgsJSON(pending.ArgsJSON)
		if err != nil {
			args = &agentv1.CreatePlanArgs{}
		}
		createPlanResult := normalizeCreatePlanResult(msg.GetCreatePlanRequestResponse())
		result.ToolResultPayload = summarizeCreatePlanResult(createPlanResult)
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_CreatePlanToolCall{
				CreatePlanToolCall: &agentv1.CreatePlanToolCall{
					Args:   args,
					Result: createPlanResult,
				},
			},
		}
		return result, nil
	case "web_search":
		var args agentv1.WebSearchArgs
		_ = json.Unmarshal(pending.ArgsJSON, &args)
		webSearchResult, payload := bridge.applyWebSearchResponse(msg.GetWebSearchRequestResponse(), &args)
		result.ToolResultPayload = payload
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_WebSearchToolCall{
				WebSearchToolCall: &agentv1.WebSearchToolCall{
					Args:   &args,
					Result: webSearchResult,
				},
			},
		}
		return result, nil
	case "web_fetch":
		var args agentv1.WebFetchArgs
		_ = json.Unmarshal(pending.ArgsJSON, &args)
		webFetchResult, payload := bridge.applyWebFetchResponse(msg.GetWebFetchRequestResponse(), &args)
		result.ToolResultPayload = payload
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_WebFetchToolCall{
				WebFetchToolCall: &agentv1.WebFetchToolCall{
					Args:   &args,
					Result: webFetchResult,
				},
			},
		}
		return result, nil
	case "switch_mode":
		var args agentv1.SwitchModeArgs
		_ = json.Unmarshal(pending.ArgsJSON, &args)
		switchModeResult := buildSwitchModeResult(msg.GetSwitchModeRequestResponse(), &args)
		result.ToolResultPayload = summarizeSwitchModeResponse(switchModeResult)
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_SwitchModeToolCall{
				SwitchModeToolCall: &agentv1.SwitchModeToolCall{
					Args:   &args,
					Result: switchModeResult,
				},
			},
		}
		return result, nil
	case "pr_management":
		var args agentv1.PrManagementArgs
		_ = json.Unmarshal(pending.ArgsJSON, &args)
		prResult := msg.GetPrManagementResult()
		result.ToolResultPayload = summarizePrManagementResult(prResult)
		result.ToolCall = &agentv1.ToolCall{
			Tool: &agentv1.ToolCall_PrManagementToolCall{
				PrManagementToolCall: &agentv1.PrManagementToolCall{
					Args:   &args,
					Result: prResult,
				},
			},
		}
		return result, nil
	default:
		return InteractionApplyResult{}, fmt.Errorf("unsupported pending interaction kind: %s", pending.InteractionKind)
	}
}

// nextMessageID 返回下一个交互消息编号。
func (bridge *Bridge) nextMessageID() uint32 {
	current := bridge.nextID.Add(1)
	if current == 0 {
		current = bridge.nextID.Add(1)
	}
	return current
}



