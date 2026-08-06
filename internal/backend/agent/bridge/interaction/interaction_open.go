// interaction_open.go 承载交互 open 域：AskQuestion/CreatePlan/WebSearch/WebFetch/SwitchMode/PrManagement 查询构造与参数解码。
package interaction

import (
	"encoding/json"
	"fmt"
	"strings"

	"cursor/gen/agentv1"
	"cursor/internal/backend/agent/core"
)

// openAskQuestion 构造 AskQuestion 交互查询。
func (bridge *Bridge) openAskQuestion(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	var args agentv1.AskQuestionArgs
	if err := json.Unmarshal(toolCall.ArgsJSON, &args); err != nil {
		return nil, runtimecore.PendingInteraction{}, fmt.Errorf("decode AskQuestion args failed: %w", err)
	}
	messageID := bridge.nextMessageID()
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_InteractionQuery{
			InteractionQuery: &agentv1.InteractionQuery{
				Id: messageID,
				Query: &agentv1.InteractionQuery_AskQuestionInteractionQuery{
					AskQuestionInteractionQuery: &agentv1.AskQuestionInteractionQuery{
						Args:       &args,
						ToolCallId: toolCall.CallID,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingInteraction{
		InteractionID:   fmt.Sprintf("%d", messageID),
		ArgsJSON:        append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:      toolCall.CallID,
		InteractionKind: "ask_question",
	}, nil
}

// openCreatePlan 构造 CreatePlan 交互查询。
func (bridge *Bridge) openCreatePlan(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	args, err := runtimecore.DecodeCreatePlanArgsJSON(toolCall.ArgsJSON)
	if err != nil {
		return nil, runtimecore.PendingInteraction{}, fmt.Errorf("decode CreatePlan args failed: %w", err)
	}
	messageID := bridge.nextMessageID()
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_InteractionQuery{
			InteractionQuery: &agentv1.InteractionQuery{
				Id: messageID,
				Query: &agentv1.InteractionQuery_CreatePlanRequestQuery{
					CreatePlanRequestQuery: &agentv1.CreatePlanRequestQuery{
						Args:       args,
						ToolCallId: toolCall.CallID,
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingInteraction{
		InteractionID:   fmt.Sprintf("%d", messageID),
		ArgsJSON:        append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:      toolCall.CallID,
		InteractionKind: "create_plan",
	}, nil
}

// openWebSearch 构造 WebSearch 交互查询。
func (bridge *Bridge) openWebSearch(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	var input struct {
		SearchTerm string `json:"search_term"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &input); err != nil {
		return nil, runtimecore.PendingInteraction{}, fmt.Errorf("decode WebSearch args failed: %w", err)
	}
	messageID := bridge.nextMessageID()
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_InteractionQuery{
			InteractionQuery: &agentv1.InteractionQuery{
				Id: messageID,
				Query: &agentv1.InteractionQuery_WebSearchRequestQuery{
					WebSearchRequestQuery: &agentv1.WebSearchRequestQuery{
						Args: &agentv1.WebSearchArgs{
							SearchTerm: input.SearchTerm,
							ToolCallId: toolCall.CallID,
						},
					},
				},
			},
		},
	}
	return serverMessage, runtimecore.PendingInteraction{
		InteractionID:   fmt.Sprintf("%d", messageID),
		ArgsJSON:        append([]byte(nil), toolCall.ArgsJSON...),
		ToolCallID:      toolCall.CallID,
		InteractionKind: "web_search",
	}, nil
}

// openWebFetch 构造 WebFetch 交互查询。
func (bridge *Bridge) openWebFetch(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(toolCall.ArgsJSON, &input); err != nil {
		return nil, runtimecore.PendingInteraction{}, fmt.Errorf("decode WebFetch args failed: %w", err)
	}
	messageID := bridge.nextMessageID()
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_InteractionQuery{
			InteractionQuery: &agentv1.InteractionQuery{
				Id: messageID,
				Query: &agentv1.InteractionQuery_WebFetchRequestQuery{
					WebFetchRequestQuery: &agentv1.WebFetchRequestQuery{
						Args: &agentv1.WebFetchArgs{
							Url:        input.URL,
							ToolCallId: toolCall.CallID,
						},
					},
				},
			},
		},
	}
	argsPayload, _ := json.Marshal(agentv1.WebFetchArgs{
		Url:        input.URL,
		ToolCallId: toolCall.CallID,
	})
	return serverMessage, runtimecore.PendingInteraction{
		InteractionID:   fmt.Sprintf("%d", messageID),
		ArgsJSON:        argsPayload,
		ToolCallID:      toolCall.CallID,
		InteractionKind: "web_fetch",
	}, nil
}

// openSwitchMode 构造 SwitchMode 交互查询。
func (bridge *Bridge) openSwitchMode(toolCall runtimecore.ToolInvocation) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	var args agentv1.SwitchModeArgs
	if err := json.Unmarshal(toolCall.ArgsJSON, &args); err != nil {
		return nil, runtimecore.PendingInteraction{}, fmt.Errorf("decode SwitchMode args failed: %w", err)
	}
	if err := validateSwitchModeTargetID(args.GetTargetModeId()); err != nil {
		return nil, runtimecore.PendingInteraction{}, err
	}
	args.ToolCallId = toolCall.CallID
	messageID := bridge.nextMessageID()
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_InteractionQuery{
			InteractionQuery: &agentv1.InteractionQuery{
				Id: messageID,
				Query: &agentv1.InteractionQuery_SwitchModeRequestQuery{
					SwitchModeRequestQuery: &agentv1.SwitchModeRequestQuery{
						Args: &args,
					},
				},
			},
		},
	}
	argsPayload, _ := json.Marshal(&args)
	return serverMessage, runtimecore.PendingInteraction{
		InteractionID:   fmt.Sprintf("%d", messageID),
		ArgsJSON:        argsPayload,
		ToolCallID:      toolCall.CallID,
		InteractionKind: "switch_mode",
	}, nil
}

func validateSwitchModeTargetID(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "agent", "ask", "plan":
		return nil
	default:
		return fmt.Errorf("unsupported target mode id: %q", strings.TrimSpace(raw))
	}
}

// openPrManagement 构造 CreatePr/UpdatePr 的 PR 管理交互查询。
func (bridge *Bridge) openPrManagement(toolCall runtimecore.ToolInvocation, actionType string) (*agentv1.AgentServerMessage, runtimecore.PendingInteraction, error) {
	args, err := decodePrManagementArgs(toolCall.ArgsJSON, actionType)
	if err != nil {
		return nil, runtimecore.PendingInteraction{}, err
	}
	args.ToolCallId = toolCall.CallID
	messageID := bridge.nextMessageID()
	serverMessage := &agentv1.AgentServerMessage{
		Message: &agentv1.AgentServerMessage_InteractionQuery{
			InteractionQuery: &agentv1.InteractionQuery{
				Id: messageID,
				Query: &agentv1.InteractionQuery_PrManagementRequestQuery{
					PrManagementRequestQuery: &agentv1.PrManagementRequestQuery{
						Args: args,
					},
				},
			},
		},
	}
	argsPayload, _ := json.Marshal(args)
	return serverMessage, runtimecore.PendingInteraction{
		InteractionID:   fmt.Sprintf("%d", messageID),
		ArgsJSON:        argsPayload,
		ToolCallID:      toolCall.CallID,
		InteractionKind: "pr_management",
	}, nil
}

// decodePrManagementArgs 解析 CreatePr/UpdatePr 的模型侧参数。
func decodePrManagementArgs(raw []byte, actionType string) (*agentv1.PrManagementArgs, error) {
	var input struct {
		Title         string   `json:"title"`
		Body          string   `json:"body"`
		BaseBranch    string   `json:"base_branch"`
		BaseBranchC   string   `json:"baseBranch"`
		Draft         bool     `json:"draft"`
		BranchName    string   `json:"branch_name"`
		BranchNameC   string   `json:"branchName"`
		AddLabels     []string `json:"add_labels"`
		AddLabelsC    []string `json:"addLabels"`
		RemoveLabels  []string `json:"remove_labels"`
		RemoveLabelsC []string `json:"removeLabels"`
		RepoURL       string   `json:"repo_url"`
		RepoURLC      string   `json:"repoUrl"`
		PrURL         string   `json:"pr_url"`
		PrURLC        string   `json:"prUrl"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decode PR management args failed: %w", err)
	}
	args := &agentv1.PrManagementArgs{}
	switch strings.TrimSpace(actionType) {
	case "create":
		if strings.TrimSpace(input.Title) == "" {
			return nil, fmt.Errorf("CreatePr title is required")
		}
		args.Action = &agentv1.PrManagementArgs_CreatePr{
			CreatePr: &agentv1.CreatePrAction{
				Title:      strings.TrimSpace(input.Title),
				Body:       strings.TrimSpace(input.Body),
				BaseBranch: stringPtrIfNonEmpty(firstNonEmptyString(input.BaseBranch, input.BaseBranchC)),
				Draft:      boolPtrIfTrue(input.Draft),
				BranchName: firstNonEmptyString(input.BranchName, input.BranchNameC),
				AddLabels:  firstNonEmptyStrings(input.AddLabels, input.AddLabelsC),
				RepoUrl:    stringPtrIfNonEmpty(firstNonEmptyString(input.RepoURL, input.RepoURLC)),
			},
		}
	case "update":
		args.Action = &agentv1.PrManagementArgs_UpdatePr{
			UpdatePr: &agentv1.UpdatePrAction{
				PrUrl:        stringPtrIfNonEmpty(firstNonEmptyString(input.PrURL, input.PrURLC)),
				Title:        stringPtrIfNonEmpty(input.Title),
				Body:         stringPtrIfNonEmpty(input.Body),
				BaseBranch:   stringPtrIfNonEmpty(firstNonEmptyString(input.BaseBranch, input.BaseBranchC)),
				BranchName:   stringPtrIfNonEmpty(firstNonEmptyString(input.BranchName, input.BranchNameC)),
				AddLabels:    firstNonEmptyStrings(input.AddLabels, input.AddLabelsC),
				RemoveLabels: firstNonEmptyStrings(input.RemoveLabels, input.RemoveLabelsC),
				RepoUrl:      stringPtrIfNonEmpty(firstNonEmptyString(input.RepoURL, input.RepoURLC)),
			},
		}
	default:
		return nil, fmt.Errorf("unsupported PR management action: %s", strings.TrimSpace(actionType))
	}
	return args, nil
}

// summarizePrManagementResult 生成 PR 管理响应摘要。
func summarizePrManagementResult(result *agentv1.PrManagementResult) string {
	if result == nil {
		return ""
	}
	switch item := result.GetResult().(type) {
	case *agentv1.PrManagementResult_Success:
		success := item.Success
		msg := fmt.Sprintf("PR %d created: %s", success.GetPrNumber(), strings.TrimSpace(success.GetPrUrl()))
		if text := strings.TrimSpace(success.GetMessage()); text != "" {
			msg += " (" + text + ")"
		}
		return msg
	case *agentv1.PrManagementResult_Registered:
		registered := item.Registered
		msg := fmt.Sprintf("PR registered: %s", strings.TrimSpace(registered.GetTitle()))
		if text := strings.TrimSpace(registered.GetMessage()); text != "" {
			msg += " — " + text
		}
		return msg
	case *agentv1.PrManagementResult_NeedsConfirmation:
		needs := item.NeedsConfirmation
		msg := strings.TrimSpace(needs.GetMessage())
		if discovered := strings.TrimSpace(needs.GetDiscoveredPrUrl()); discovered != "" {
			msg += fmt.Sprintf(" (existing PR: %s #%d %s)", strings.TrimSpace(needs.GetDiscoveredPrTitle()), needs.GetDiscoveredPrNumber(), discovered)
		}
		return msg
	case *agentv1.PrManagementResult_Rejected:
		return "PR rejected: " + strings.TrimSpace(item.Rejected.GetReason())
	case *agentv1.PrManagementResult_Error:
		return "PR management error: " + strings.TrimSpace(item.Error.GetError())
	default:
		return "PR management completed with no result"
	}
}

// firstNonEmptyStrings 返回第一个非空字符串切片。
func firstNonEmptyStrings(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

// stringPtrIfNonEmpty 在需要 optional string 时构造指针值，空串返回 nil。
func stringPtrIfNonEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// firstNonEmptyString 返回第一个非空字符串。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// boolPtrIfTrue 在需要 optional bool 时构造指针值。
func boolPtrIfTrue(value bool) *bool {
	if value {
		return &value
	}
	return nil
}
