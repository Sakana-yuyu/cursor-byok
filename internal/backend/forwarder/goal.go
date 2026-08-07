package forwarder

import (
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