// interaction_summarize.go 承载交互结果摘要域：各响应转模型可读文本与结果归一化。
package interaction

import (
	"fmt"
	"strings"

	"cursor/gen/agentv1"
)

func summarizeAskQuestionResponse(response *agentv1.AskQuestionInteractionResponse) string {
	if response == nil || response.GetResult() == nil {
		return "ask question response missing"
	}
	switch item := response.GetResult().GetResult().(type) {
	case *agentv1.AskQuestionResult_Success:
		if len(item.Success.GetAnswers()) == 0 {
			return "ask question success"
		}
		return fmt.Sprintf("ask question answers=%d", len(item.Success.GetAnswers()))
	case *agentv1.AskQuestionResult_Error:
		return item.Error.GetErrorMessage()
	case *agentv1.AskQuestionResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.AskQuestionResult_Async:
		return "ask question async accepted"
	default:
		return "unknown ask question response"
	}
}

const createPlanEmptyURIError = "create plan failed: Cursor returned success with empty planUri"

// normalizeCreatePlanResult 兜底客户端 success 但未返回 planUri 的异常形态。
func normalizeCreatePlanResult(response *agentv1.CreatePlanRequestResponse) *agentv1.CreatePlanResult {
	if response == nil || response.GetResult() == nil {
		return nil
	}
	result := response.GetResult()
	if result.GetSuccess() != nil && strings.TrimSpace(result.GetPlanUri()) == "" {
		return &agentv1.CreatePlanResult{
			Result: &agentv1.CreatePlanResult_Error{
				Error: &agentv1.CreatePlanError{Error: createPlanEmptyURIError},
			},
		}
	}
	return result
}

// summarizeCreatePlanResult 生成 CreatePlan 响应摘要。
func summarizeCreatePlanResult(result *agentv1.CreatePlanResult) string {
	if result == nil {
		return "create plan response missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.CreatePlanResult_Success:
		return fmt.Sprintf("create plan success uri=%s", result.GetPlanUri())
	case *agentv1.CreatePlanResult_Error:
		return item.Error.GetError()
	default:
		return "unknown create plan response"
	}
}

// summarizeWebSearchResponse 生成 WebSearch 响应摘要。
func summarizeWebSearchResponse(response *agentv1.WebSearchRequestResponse) string {
	if response == nil {
		return "web search response missing"
	}
	switch item := response.GetResult().(type) {
	case *agentv1.WebSearchRequestResponse_Approved_:
		_ = item
		return "web search approved"
	case *agentv1.WebSearchRequestResponse_Rejected_:
		return item.Rejected.GetReason()
	default:
		return "unknown web search response"
	}
}

// applyWebSearchResponse 把 WebSearch approval 响应转换成最终工具结果。
func (bridge *Bridge) applyWebSearchResponse(response *agentv1.WebSearchRequestResponse, args *agentv1.WebSearchArgs) (*agentv1.WebSearchResult, string) {
	if response == nil {
		return &agentv1.WebSearchResult{
			Result: &agentv1.WebSearchResult_Error{
				Error: &agentv1.WebSearchError{Error: "web search response missing"},
			},
		}, "web search response missing"
	}
	switch item := response.GetResult().(type) {
	case *agentv1.WebSearchRequestResponse_Approved_:
		_ = item
		references, payload, err := bridge.executeWebSearch(strings.TrimSpace(args.GetSearchTerm()))
		if err != nil {
			return &agentv1.WebSearchResult{
				Result: &agentv1.WebSearchResult_Error{
					Error: &agentv1.WebSearchError{Error: err.Error()},
				},
			}, err.Error()
		}
		references, payload = truncateWebSearchReplay(strings.TrimSpace(args.GetSearchTerm()), references, payload)
		return &agentv1.WebSearchResult{
			Result: &agentv1.WebSearchResult_Success{
				Success: &agentv1.WebSearchSuccess{References: references},
			},
		}, payload
	case *agentv1.WebSearchRequestResponse_Rejected_:
		return &agentv1.WebSearchResult{
			Result: &agentv1.WebSearchResult_Rejected{
				Rejected: &agentv1.WebSearchRejected{Reason: item.Rejected.GetReason()},
			},
		}, item.Rejected.GetReason()
	default:
		return &agentv1.WebSearchResult{
			Result: &agentv1.WebSearchResult_Error{
				Error: &agentv1.WebSearchError{Error: "unknown web search response"},
			},
		}, "unknown web search response"
	}
}

// applyWebFetchResponse 把 WebFetch approval 响应转换成最终工具结果。
func (bridge *Bridge) applyWebFetchResponse(response *agentv1.WebFetchRequestResponse, args *agentv1.WebFetchArgs) (*agentv1.WebFetchResult, string) {
	if response == nil {
		return &agentv1.WebFetchResult{
			Result: &agentv1.WebFetchResult_Error{
				Error: &agentv1.WebFetchError{
					Url:   args.GetUrl(),
					Error: "web fetch response missing",
				},
			},
		}, "web fetch response missing"
	}
	switch item := response.GetResult().(type) {
	case *agentv1.WebFetchRequestResponse_Approved_:
		_ = item
		markdown, err := bridge.executeWebFetch(strings.TrimSpace(args.GetUrl()))
		if err != nil {
			return &agentv1.WebFetchResult{
				Result: &agentv1.WebFetchResult_Error{
					Error: &agentv1.WebFetchError{
						Url:   args.GetUrl(),
						Error: err.Error(),
					},
				},
			}, err.Error()
		}
		return &agentv1.WebFetchResult{
			Result: &agentv1.WebFetchResult_Success{
				Success: &agentv1.WebFetchSuccess{
					Url:      args.GetUrl(),
					Markdown: markdown,
				},
			},
		}, markdown
	case *agentv1.WebFetchRequestResponse_Rejected_:
		return &agentv1.WebFetchResult{
			Result: &agentv1.WebFetchResult_Rejected{
				Rejected: &agentv1.WebFetchRejected{Reason: item.Rejected.GetReason()},
			},
		}, item.Rejected.GetReason()
	default:
		return &agentv1.WebFetchResult{
			Result: &agentv1.WebFetchResult_Error{
				Error: &agentv1.WebFetchError{
					Url:   args.GetUrl(),
					Error: "unknown web fetch response",
				},
			},
		}, "unknown web fetch response"
	}
}

// buildSwitchModeResult 把 SwitchMode approval 响应转换成最终工具结果。
func buildSwitchModeResult(response *agentv1.SwitchModeRequestResponse, args *agentv1.SwitchModeArgs) *agentv1.SwitchModeResult {
	if response == nil {
		return &agentv1.SwitchModeResult{
			Result: &agentv1.SwitchModeResult_Error{
				Error: &agentv1.SwitchModeError{Error: "switch mode response missing"},
			},
		}
	}
	switch item := response.GetResult().(type) {
	case *agentv1.SwitchModeRequestResponse_Approved_:
		_ = item
		targetModeID := strings.ToLower(strings.TrimSpace(args.GetTargetModeId()))
		return &agentv1.SwitchModeResult{
			Result: &agentv1.SwitchModeResult_Success{
				Success: &agentv1.SwitchModeSuccess{
					FromModeId: "unknown",
					ToModeId:   targetModeID,
				},
			},
		}
	case *agentv1.SwitchModeRequestResponse_Rejected_:
		return &agentv1.SwitchModeResult{
			Result: &agentv1.SwitchModeResult_Rejected{
				Rejected: &agentv1.SwitchModeRejected{Reason: item.Rejected.GetReason()},
			},
		}
	default:
		return &agentv1.SwitchModeResult{
			Result: &agentv1.SwitchModeResult_Error{
				Error: &agentv1.SwitchModeError{Error: "unknown switch mode response"},
			},
		}
	}
}

// summarizeSwitchModeResponse 生成 SwitchMode 响应摘要。
func summarizeSwitchModeResponse(result *agentv1.SwitchModeResult) string {
	if result == nil {
		return "switch mode result missing"
	}
	switch item := result.GetResult().(type) {
	case *agentv1.SwitchModeResult_Success:
		return fmt.Sprintf("switch mode success to=%s", item.Success.GetToModeId())
	case *agentv1.SwitchModeResult_Rejected:
		return item.Rejected.GetReason()
	case *agentv1.SwitchModeResult_Error:
		return item.Error.GetError()
	default:
		return "unknown switch mode result"
	}
}
