package forwarder

import (
	"fmt"
	"strings"
	"time"

	agentv1 "cursor/gen/agentv1"
	"google.golang.org/protobuf/proto"
)

// GoalStatus 是 goal 会话的终态/运行态枚举。只在内存维护（MVP），不持久化。
type GoalStatus string

const (
	GoalStatusRunning        GoalStatus = "running"
	GoalStatusCompleted      GoalStatus = "completed"
	GoalStatusFailed         GoalStatus = "failed"
	GoalStatusBudgetExceeded GoalStatus = "budget_exceeded"
	GoalStatusStopped        GoalStatus = "stopped"
)

// GoalState 挂在 ActiveStream.Goal 上（nil = 非 goal 会话）。
type GoalState struct {
	ConversationID string
	GoalText       string
	Status         GoalStatus
	Strict         bool // /goal --strict：校验不通过时不允许模型自检兜底（借鉴 Reasonix Strict）
	ProviderPasses int
	ToolCalls      int
	SelfChecks     int
	RetryCount     int // 校验子代理未通过的重试次数
	ErrorRetries   int // provider 错误重试次数
	StaleCount     int // 校验连续未通过次数，达到阈值后提示结构性换策略（借鉴 Reasonix stale pivot）
	CostEstimateUSD float64
	StartedAt      time.Time
	UpdatedAt      time.Time
	LastProgress   string
	CompletionText string
	StopReason     string

	consecutiveIdle    int  // 连续无工具调用 pass 计数
	CompletionClaimed  bool // 模型已输出 [goal:complete] 声明
}

func newGoalState(conversationID, goalText string, strict bool) *GoalState {
	now := time.Now().UTC()
	return &GoalState{
		ConversationID: conversationID,
		GoalText:       strings.TrimSpace(goalText),
		Status:         GoalStatusRunning,
		Strict:         strict,
		StartedAt:      now,
		UpdatedAt:      now,
	}
}

// GoalRuntimeConfig 是 forwarder 运行时消费的 goal 配置，由 host 层从
// server/config 的持久化结构转换而来（仿 delegation.RuntimeConfig）。
type GoalRuntimeConfig struct {
	Enabled           bool
	MaxProviderPasses int
	MaxDuration       time.Duration
	MaxCostUSD        float64
	SelfCheckPasses   int
	VerifyMaxRetries  int
	ErrorMaxRetries   int
	ProgressInterval  int
}

func defaultGoalRuntimeConfig() GoalRuntimeConfig {
	return GoalRuntimeConfig{
		Enabled:           false,
		MaxProviderPasses: 30,
		SelfCheckPasses:   2,
		VerifyMaxRetries:  3,
		ErrorMaxRetries:   3,
		ProgressInterval:  5,
	}
}

// goalConfigProvider 由 host 层实现并注入 NewService（resolver 类型断言）。
type goalConfigProvider interface {
	GoalRuntimeConfig() GoalRuntimeConfig
}

// GoalSnapshot 是暴露给前端（wails binding）的稳定快照。
type GoalSnapshot struct {
	ConversationID string  `json:"conversationId"`
	GoalText       string  `json:"goalText"`
	Status         string  `json:"status"`
	ProviderPasses int     `json:"providerPasses"`
	ToolCalls      int     `json:"toolCalls"`
	SelfChecks     int     `json:"selfChecks"`
	RetryCount     int     `json:"retryCount"`
	CostEstimateUSD float64 `json:"costEstimateUsd"`
	LastProgress   string  `json:"lastProgress"`
	CompletionText string  `json:"completionText"`
	StopReason     string  `json:"stopReason"`
	StartedAt      string  `json:"startedAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

// registerGoal 登记 goal 状态（内存 map，保留最近 100 条）。
func (service *Service) registerGoal(conversationID string, state *GoalState) {
	if service == nil || state == nil {
		return
	}
	service.goalsMu.Lock()
	defer service.goalsMu.Unlock()
	if service.goals == nil {
		service.goals = make(map[string]*GoalState)
	}
	service.goals[conversationID] = state
	if len(service.goals) > 100 {
		// 淘汰最旧一条
		var oldestKey string
		var oldest time.Time
		for k, v := range service.goals {
			if oldestKey == "" || v.UpdatedAt.Before(oldest) {
				oldestKey, oldest = k, v.UpdatedAt
			}
		}
		if oldestKey != "" {
			delete(service.goals, oldestKey)
		}
	}
}

// GoalSnapshots 返回全部已登记 goal 的稳定快照（供前端面板轮询）。
func (service *Service) GoalSnapshots() []GoalSnapshot {
	if service == nil {
		return nil
	}
	service.goalsMu.RLock()
	defer service.goalsMu.RUnlock()
	snaps := make([]GoalSnapshot, 0, len(service.goals))
	for _, state := range service.goals {
		snaps = append(snaps, goalSnapshotOf(state))
	}
	return snaps
}

func goalSnapshotOf(state *GoalState) GoalSnapshot {
	return GoalSnapshot{
		ConversationID: state.ConversationID,
		GoalText:       state.GoalText,
		Status:         string(state.Status),
		ProviderPasses: state.ProviderPasses,
		ToolCalls:      state.ToolCalls,
		SelfChecks:     state.SelfChecks,
		RetryCount:     state.RetryCount,
		CostEstimateUSD: state.CostEstimateUSD,
		LastProgress:   state.LastProgress,
		CompletionText: state.CompletionText,
		StopReason:     state.StopReason,
		StartedAt:      state.StartedAt.Format(time.RFC3339),
		UpdatedAt:      state.UpdatedAt.Format(time.RFC3339),
	}
}

// currentGoalConfig 返回 goal 运行时配置；provider 未注入时用默认值。
func (service *Service) currentGoalConfig() GoalRuntimeConfig {
	if service == nil || service.goalConfig == nil {
		return defaultGoalRuntimeConfig()
	}
	return service.goalConfig.GoalRuntimeConfig()
}

// goalCommandPrefixes 是 /goal 文本命令的识别前缀。命中后前缀与可选的 --strict
// flag 被剥离，剩余文本作为 goal 目标写入 GoalState（前端面板复用同一解析）。
// --strict 借鉴 Reasonix 的 /goal --strict：校验不通过不允许覆盖/兜底。
var goalCommandPrefixes = []string{"/goal", "#goal", "goal:"}

// parseGoalCommand 识别 /goal 文本命令。返回 (目标文本, strict, 是否命中)。
func parseGoalCommand(text string) (goalText string, strict bool, isGoal bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	for _, prefix := range goalCommandPrefixes {
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(trimmed[len(prefix):])
			if rest == "" {
				return "", false, false
			}
			restLower := strings.ToLower(rest)
			if strings.HasPrefix(restLower, "--strict") {
				rest = strings.TrimSpace(rest[len("--strict"):])
				if rest == "" {
					return "", true, false
				}
				return rest, true, true
			}
			return rest, false, true
		}
	}
	return "", false, false
}

// replaceUserMessageText 返回替换 text 后的 UserMessage 副本。
func replaceUserMessageText(message *agentv1.UserMessage, text string) *agentv1.UserMessage {
	if message == nil {
		return &agentv1.UserMessage{Text: text}
	}
	cloned := proto.Clone(message).(*agentv1.UserMessage)
	cloned.Text = text
	return cloned
}

// goalSystemPromptFragment 生成 goal 模式的系统指令段，追加在
// customSystemPrompt 位置（systemParts 末尾），避免影响前缀稳定性。
func goalSystemPromptFragment(goal *GoalState, cfg GoalRuntimeConfig) string {
	if goal == nil || strings.TrimSpace(goal.GoalText) == "" {
		return ""
	}
	parts := []string{
		"你当前处于 GOAL 模式。你的目标（GOAL）：",
		goal.GoalText,
		"",
		"执行要求：",
		"0. 先拆解目标：把目标拆成 3-8 个可验证的步骤清单（在回复中列出），逐项完成并标记。",
		"1. 自主决策：持续调用工具推进目标，不要轻易停下或询问用户；只有真正无法继续时才停下并说明卡点。",
		"2. 循环执行：一轮结束（没有工具调用）不代表完成。请自检目标是否达成：未达成则继续执行下一轮，直到目标真正达成。",
		"3. 失败重试：工具执行失败时分析原因、换一种方式重试，不要直接放弃。",
		"4. 进度汇报：每完成一个阶段，用简短一句话汇报当前进度。",
		"5. 完成标准：目标的所有要求都满足后才算完成。完成时输出以 [goal:complete] 开头（单独一行）的最终完成报告，说明你做了什么、验证了什么、结果如何；不要在没有真正完成时输出该标记。",
	}
	if cfg.MaxProviderPasses > 0 {
		parts = append(parts, fmt.Sprintf("6. 预算：本 goal 最多执行 %d 轮 provider 调用，请高效推进，优先完成最关键步骤。", cfg.MaxProviderPasses))
	}
	return strings.Join(parts, "\n")
}

// joinNonEmpty 用空行连接非空片段。
func joinNonEmpty(parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(p))
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}