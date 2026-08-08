package forwarder

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentv1 "cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
	"cursor/internal/historymetrics"
	"cursor/internal/logger"
	"github.com/google/uuid"
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

// applyGoalCommandIfEnabled 在 goal 开关（goal.enabled）开启时识别 /goal 与
// /goal --strict 文本命令：命中后标记 GoalMode 并剥离前缀；开关关闭时原样
// 保留消息内容，按普通对话处理。
func applyGoalCommandIfEnabled(intent *InboundIntent, enabled bool) {
	if intent == nil || intent.GoalMode || !enabled {
		return
	}
	if goalText, strict, isGoal := parseGoalCommand(userMessageText(intent.UserMessage)); isGoal {
		intent.GoalMode = true
		intent.GoalText = goalText
		intent.GoalStrict = strict
		// 剥离前缀，避免 goal 目标文本被当作指令重复注入。
		intent.UserMessage = replaceUserMessageText(intent.UserMessage, goalText)
	}
}

// replaceUserMessageText 返回替换 text 后的 UserMessage 副本。
func replaceUserMessageText(message *agentv1.UserMessage, text string) *agentv1.UserMessage {
	if message == nil {
		return &agentv1.UserMessage{Text: text}
	}
	cloned, ok := proto.Clone(message).(*agentv1.UserMessage)
	if !ok {
		// Clone 理论上返回同类型；失败时退回构造新消息，避免运行时 panic
		// 冒泡到 forwarder 导致所有活跃对话掉线。
		return &agentv1.UserMessage{Text: text}
	}
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

// goalBudgetExceeded 检查 goal 是否超出预算（passes / 时长）。
// 返回 (是否超限, 原因)。MaxCostUSD 的费用检查在 handleGoalPassFinished 里
// 依赖 cost 估算结果（见 updateGoalCostEstimate）。
func goalBudgetExceeded(goal *GoalState, cfg GoalRuntimeConfig) (bool, string) {
	if goal == nil {
		return false, ""
	}
	if cfg.MaxProviderPasses > 0 && goal.ProviderPasses >= cfg.MaxProviderPasses {
		return true, fmt.Sprintf("达到 provider pass 上限 %d", cfg.MaxProviderPasses)
	}
	if cfg.MaxDuration > 0 && !goal.StartedAt.IsZero() && time.Since(goal.StartedAt) >= cfg.MaxDuration {
		return true, fmt.Sprintf("达到时长上限 %s", cfg.MaxDuration)
	}
	return false, ""
}

// goalCompletionMarker 是模型声明"目标已完成"的显式标记。借鉴 Reasonix 的
// [goal:complete] 完成声明拦截：后端不轻信"无工具调用"，只认显式声明；
// 声明后仍需校验子代理证据审计才放行（非 strict 且无校验能力时退化为自检）。
const goalCompletionMarker = "[goal:complete]"

// goalStalePivotThreshold 是校验连续未通过 / 连续停顿达到该次数后，提示结构性
// 换策略的阈值。借鉴 Reasonix AutoResearch 的 stale_count pivot：换入口点、
// 任务分解或验证方式，而不是重复同一做法。
const goalStalePivotThreshold = 2

const promptContextSourceGoalIdle = "goal_idle"
const promptContextSourceGoalVerifyFeedback = "goal_verify_feedback"
const promptContextSourceGoalErrorRetry = "goal_error_retry"
const promptContextSourceGoalBudget = "goal_budget"

// goalIdleReminder 是停顿提醒：无工具调用且未声明完成时，要求模型继续推进
// 或说明卡点；连续多轮停顿则要求改变策略（文案随 idleCount 升级）。
func goalIdleReminder(goal *GoalState, idleCount int) PromptContextMessage {
	body := fmt.Sprintf("目标：%s\n\n检测到你已连续 %d 轮没有调用工具。若目标尚未达成，请继续调用工具执行；若遇到卡点，请明确说明卡点并尝试换一种方式突破。", goal.GoalText, idleCount)
	if idleCount >= goalStalePivotThreshold {
		body += "\n连续多轮无进展：请结构性换策略（改变入口点、任务分解或验证方式），不要重复同样的做法。"
	}
	return newPromptContextReminder(promptContextSourceGoalIdle, body)
}

// goalVerifyFeedbackReminder 是校验未通过反馈：列出校验子代理的未达成理由，
// 要求继续执行（Reasonix goal intercept 的"列出未完成项"角色）。
func goalVerifyFeedbackReminder(goal *GoalState, feedback string) PromptContextMessage {
	body := fmt.Sprintf("目标：%s\n\n校验子代理判定目标尚未达成，理由：%s\n请根据该反馈继续执行，直到目标真正达成。", goal.GoalText, feedback)
	return newPromptContextReminder(promptContextSourceGoalVerifyFeedback, body)
}

// appendGoalPromptContext 追加一条 goal 提示到 conversation 并落库（幂等：同一
// source 与 turn 去重，避免无限注入）。返回是否实际追加。
func (service *Service) appendGoalPromptContext(stream *ActiveStream, conversationID string, turnSeq int64, requestID, source, text string) (bool, error) {
	if service == nil || stream == nil || strings.TrimSpace(text) == "" {
		return false, nil
	}
	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return false, err
	}
	if conversation == nil {
		return false, nil
	}
	if currentTurnHasPromptContextSource(conversation, turnSeq, source) {
		return false, nil
	}
	msg := PromptContextMessage{
		Source:  source,
		Message: modeladapter.Message{Role: "user", Content: text},
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newPromptContextEntry(turnSeq, requestID, msg),
	}); err != nil {
		return false, err
	}
	return true, nil
}

// updateGoalCostEstimate 用本 pass 的 token 用量估算费用并累加。
// 没有 pricing provider 时保持 0（费用检查自然跳过）。
func (service *Service) updateGoalCostEstimate(stream *ActiveStream, goal *GoalState) {
	if service == nil || stream == nil || goal == nil || service.usageCostEstimator == nil {
		return
	}
	cost, ok := service.usageCostEstimator.Cost(stream, stream.ProviderUsage)
	if ok {
		goal.CostEstimateUSD += cost
	}
}

// goalUsageCostEstimator 估算单个 provider pass 的美元费用；返回 ok=false 表示
// 无定价来源（费用检查跳过）。
type goalUsageCostEstimator interface {
	Cost(stream *ActiveStream, usage turnUsageSnapshot) (float64, bool)
}

// goalVerifyPrompt 构造校验子代理的任务提示：只读检查目标是否真正达成，
// 输出必须以 VERIFIED / NOT_VERIFIED 开头。
func goalVerifyPrompt(goal *GoalState) string {
	return fmt.Sprintf(`你是只读校验子代理。请检查以下 GOAL 是否已经真正达成：

GOAL：%s

检查要求：
1. 只读检查代码/结果/证据，不要修改任何文件。
2. 逐项核对 GOAL 的要求是否全部满足；检查是否有遗漏、假完成或未验证的部分。
3. 输出格式（严格）：
   第一行：VERIFIED 或 NOT_VERIFIED
   其余行：简短理由（验证了什么、还差什么）。

请基于真实证据判断，不要轻信主代理的自我声明。`, goal.GoalText)
}

// parseVerifyDecision 解析校验子代理输出首行判定。
func parseVerifyDecision(output string) (verified bool, report string) {
	lines := strings.SplitN(strings.TrimSpace(output), "\n", 2)
	if len(lines) == 0 {
		return false, ""
	}
	head := strings.ToUpper(strings.TrimSpace(lines[0]))
	switch head {
	case "VERIFIED":
		verified = true
	case "NOT_VERIFIED":
		verified = false
	default:
		// 未按格式输出时保守判定为未通过，并把全文作为理由。
		return false, strings.TrimSpace(output)
	}
	report = ""
	if len(lines) == 2 {
		report = strings.TrimSpace(lines[1])
	}
	return verified, report
}

// goalVerifyCompletion 同步跑一个只读校验子代理确认 goal 是否达成。
// service.multitaskDelegation 不可用时退化为"模型自检通过"（返回 verified=true），
// 并在日志中说明——保证 goal 在无委派配置时仍能收口而不是卡死。
func (service *Service) goalVerifyCompletion(stream *ActiveStream, goal *GoalState) (bool, string, error) {
	if service == nil || stream == nil || goal == nil {
		return false, "", nil
	}
	if service.multitaskDelegation == nil || service.multitaskDelegation.scheduler == nil {
		// strict 模式不兜底：没有真实校验子代理就按"未通过"处理（借鉴 Reasonix
		// Strict 模式不允许覆盖拦截）；普通模式退化为模型自检通过，保证可收口。
		if goal.Strict {
			return false, "（strict 模式要求真实校验子代理，但委派不可用）", nil
		}
		logger.Infof("forwarder goal verify skipped (delegation unavailable) request_id=%s", strings.TrimSpace(stream.RequestID))
		return true, "（无校验子代理可用，采用模型自检结论）", nil
	}
	taskID := "goal-verify-" + uuid.NewString()
	_, err := service.multitaskDelegation.scheduler.Submit(delegation.TaskRequest{
		ID:             taskID,
		ParentRequest:  stream.RequestID,
		ConversationID: stream.ConversationID,
		SubagentType:   "generalPurpose",
		Readonly:       true,
		Prompt:         goalVerifyPrompt(goal),
		Description:    "goal 完成校验",
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		Timeout:        3 * time.Minute,
		Contract: &delegation.SupervisionTaskContract{
			DoneCriteria: []string{"输出 VERIFIED 或 NOT_VERIFIED 结论与理由"},
		},
	})
	if err != nil {
		return false, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := service.multitaskDelegation.scheduler.WaitForTerminal(ctx, []string{taskID}); err != nil {
		return false, "", err
	}
	result, ok := service.multitaskDelegation.scheduler.Result(taskID)
	if !ok {
		return false, "", fmt.Errorf("goal verify task %s has no result", taskID)
	}
	verified, report := parseVerifyDecision(result.Output)
	return verified, report, nil
}

// defaultUsageCostEstimator 用 historymetrics 定价表估算；无表时返回 ok=false。
// NewService 里始终赋值（nil 安全），保证 updateGoalCostEstimate 恒可调用。
type defaultUsageCostEstimator struct {
	lookup *historymetrics.PriceLookup
}

func (e *defaultUsageCostEstimator) Cost(stream *ActiveStream, usage turnUsageSnapshot) (float64, bool) {
	if e == nil || e.lookup == nil || stream == nil || !usage.UsagePresent {
		return 0, false
	}
	cost, ok, _, _ := e.lookup.Cost(stream.ModelName, usage.Provider, usage.BaseURL, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
	if !ok || cost == nil {
		return 0, false
	}
	return *cost, true
}

// newPricingLookupFromConfig 从 resolver 提供的 pricing 配置构建定价表；
// 拿不到时返回 nil（费用估算与检查自动跳过）。
func newPricingLookupFromConfig(resolver modeladapter.ChannelResolver) *historymetrics.PriceLookup {
	provider, ok := resolver.(interface{ PricingRates() []historymetrics.PriceRate })
	if !ok {
		return nil
	}
	rates := provider.PricingRates()
	if len(rates) == 0 {
		return nil
	}
	return historymetrics.NewPriceLookup(rates)
}

// truncateText 按 rune 截断文本并加省略号。
func truncateText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "…"
}

// handleGoalPassFinished 在 provider pass 无工具调用收尾点接管 goal 循环：
// 返回 (true, nil) 表示已挂起继续（调用方 return）；(false, nil) 表示走正常收口
// （完成 / 预算超限 / 失败）；(false, err) 表示循环级错误。
func (service *Service) handleGoalPassFinished(stream *ActiveStream, conversationID string, turnSeq int64, requestID, modelCallID string, providerPass int, finishReason, accumulatedText string, hadToolInvocation bool) (bool, error) {
	goal := stream.Goal
	if goal == nil {
		return false, nil
	}
	goal.ProviderPasses = providerPass
	goal.ToolCalls += stream.ToolInvocationCount
	goal.UpdatedAt = time.Now().UTC()

	cfg := service.currentGoalConfig()

	// 预算检查（含费用估算：本 pass 的 token 用量计入累计费用后检查 MaxCostUSD）。
	service.updateGoalCostEstimate(stream, goal)
	if exceeded, reason := goalBudgetExceeded(goal, cfg); exceeded {
		goal.Status = GoalStatusBudgetExceeded
		goal.StopReason = reason
		goal.CompletionText = fmt.Sprintf("goal 因预算上限停止：%s", reason)
		service.emitGoalCompletion(stream, goal, "budget")
		return false, nil
	}
	if cfg.MaxCostUSD > 0 && goal.CostEstimateUSD >= cfg.MaxCostUSD {
		goal.Status = GoalStatusBudgetExceeded
		goal.StopReason = fmt.Sprintf("达到费用上限 $%.4f", cfg.MaxCostUSD)
		goal.CompletionText = goal.StopReason
		service.emitGoalCompletion(stream, goal, "budget")
		return false, nil
	}

	// 有工具调用：现有循环逻辑（actor.go）继续 resume，这里让行。
	if hadToolInvocation || shouldResumeAfterToolResults(finishReason) {
		return false, nil
	}

	// 无工具调用：模型要么显式声明完成（[goal:complete]），要么停顿。
	// 借鉴 Reasonix 的完成声明拦截：不轻信"无工具调用"，只认显式声明；
	// 停顿则注入 idle 提醒（连续多轮升级为换策略提示）。
	goal.consecutiveIdle++
	goal.LastProgress = truncateText(accumulatedText, 120)
	completionClaimed := strings.Contains(strings.ToLower(accumulatedText), goalCompletionMarker)

	if completionClaimed {
		goal.CompletionClaimed = true
		verified, report, err := service.goalVerifyCompletion(stream, goal)
		if err != nil {
			return false, err
		}
		if verified {
			goal.Status = GoalStatusCompleted
			goal.CompletionText = truncateText(report, 2000)
			goal.StopReason = ""
			service.emitGoalCompletion(stream, goal, "completed")
			return false, nil
		}
		if goal.RetryCount >= cfg.VerifyMaxRetries {
			goal.Status = GoalStatusFailed
			goal.StopReason = fmt.Sprintf("校验子代理连续 %d 次判定目标未达成", goal.RetryCount)
			goal.CompletionText = truncateText(report, 2000)
			service.emitGoalCompletion(stream, goal, "failed")
			return false, nil
		}
		goal.RetryCount++
		goal.consecutiveIdle = 0
		feedback := report
		if goal.RetryCount >= goalStalePivotThreshold {
			goal.StaleCount++
			feedback = fmt.Sprintf("%s\n\n已连续 %d 次未通过校验：请结构性换策略（改变入口点、任务分解或验证方式），不要重复同一做法。", feedback, goal.StaleCount)
		}
		appended, err := service.appendGoalPromptContext(stream, conversationID, turnSeq, requestID, promptContextSourceGoalVerifyFeedback, goalVerifyFeedbackReminder(goal, feedback).Message.Content)
		if err != nil {
			return false, err
		}
		if !appended {
			return false, nil // 已注入过，避免死循环，走收口
		}
		service.emitGoalProgress(stream, goal)
		if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
			return true, err
		}
		if err := service.publishCheckpoint(requestID, conversationID); err != nil {
			return true, err
		}
		return true, service.requestProviderAction(stream, providerActionResume)
	}

	appended, err := service.appendGoalPromptContext(stream, conversationID, turnSeq, requestID, promptContextSourceGoalIdle, goalIdleReminder(goal, goal.consecutiveIdle).Message.Content)
	if err != nil {
		return false, err
	}
	if !appended {
		return false, nil
	}
	// 每 ProgressInterval 个 pass 推一次进度摘要。
	if cfg.ProgressInterval > 0 && goal.ProviderPasses%cfg.ProgressInterval == 0 {
		service.emitGoalProgress(stream, goal)
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return true, err
	}
	if err := service.publishCheckpoint(requestID, conversationID); err != nil {
		return true, err
	}
	return true, service.requestProviderAction(stream, providerActionResume)
}

// emitGoalProgress 向对话流注入一条 goal 进度摘要（summary 事件，Cursor 左侧摘要区可见）。
func (service *Service) emitGoalProgress(stream *ActiveStream, goal *GoalState) error {
	if service == nil || stream == nil || goal == nil {
		return nil
	}
	text := fmt.Sprintf("⏳ Goal 进度：pass %d | 工具调用 %d | 自检 %d 次%s", goal.ProviderPasses, goal.ToolCalls, goal.SelfChecks, progressSuffix(goal))
	return service.publishSummaryEvents(stream, text)
}

func progressSuffix(goal *GoalState) string {
	if strings.TrimSpace(goal.LastProgress) == "" {
		return ""
	}
	return " | 最近进展：" + goal.LastProgress
}

// emitGoalCompletion 在 goal 收口时向对话流注入最终汇报（可见 assistant 文本）。
func (service *Service) emitGoalCompletion(stream *ActiveStream, goal *GoalState, kind string) error {
	if service == nil || stream == nil || goal == nil {
		return nil
	}
	var text string
	switch kind {
	case "completed":
		text = fmt.Sprintf("✅ Goal 已完成：%s\n\n%s", goal.GoalText, firstNonEmpty(goal.CompletionText, "目标已达成。"))
	case "budget":
		text = fmt.Sprintf("⏹️ Goal 因预算上限停止：%s\n%s", goal.GoalText, firstNonEmpty(goal.StopReason, "预算超限。"))
	case "failed":
		text = fmt.Sprintf("❌ Goal 失败：%s\n%s", goal.GoalText, firstNonEmpty(goal.StopReason, "多次校验未通过。"))
	default:
		text = fmt.Sprintf("Goal 状态更新：%s", goal.StopReason)
	}
	return service.broker.Publish(stream.RequestID, StreamEvent{Message: buildTextDeltaMessage(text)})
}

// publishSummaryEvents 按 SummaryStarted → Summary → SummaryCompleted 顺序推送。
func (service *Service) publishSummaryEvents(stream *ActiveStream, text string) error {
	if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: buildSummaryStartedMessage()}); err != nil {
		return err
	}
	if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: buildSummaryMessage(text)}); err != nil {
		return err
	}
	return service.broker.Publish(stream.RequestID, StreamEvent{Message: buildSummaryCompletedMessage(stream.RequestID)})
}

// goalErrorRetryReminder 是 provider 错误重试时注入的提示文案。
func goalErrorRetryReminder(errText string) PromptContextMessage {
	return newPromptContextReminder(promptContextSourceGoalErrorRetry,
		fmt.Sprintf("上一轮 provider 调用出错：%s\n请分析错误原因，换一种方式继续执行，直到完成目标。", truncateText(errText, 300)))
}

// errTextOf 提取错误文本。
func errTextOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// StartGoalStream 以 goal 模式启动一个新会话（前端 Goal 面板入口）。
// 复用 handleRunIntent 路径：构造最小 run intent 并标记 GoalMode。
// 返回 conversationID；modelID 为空时报错（前端必须选择模型）。
func (service *Service) StartGoalStream(goalText, modelID string) (string, error) {
	if service == nil {
		return "", fmt.Errorf("service unavailable")
	}
	if strings.TrimSpace(goalText) == "" {
		return "", fmt.Errorf("goal text is required")
	}
	if strings.TrimSpace(modelID) == "" {
		return "", fmt.Errorf("model id is required")
	}
	conversationID := uuid.NewString()
	intent := InboundIntent{
		RequestID:      uuid.NewString(),
		ConversationID: conversationID,
		UserMessage:    &agentv1.UserMessage{Text: strings.TrimSpace(goalText)},
		ModelID:        strings.TrimSpace(modelID),
		ModelName:      strings.TrimSpace(modelID),
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		GoalMode:       true,
		GoalText:       strings.TrimSpace(goalText),
	}
	if err := service.handleRunIntent(intent); err != nil {
		return "", err
	}
	return conversationID, nil
}

// StopGoalStream 停止指定会话的 goal 执行（复用取消路径）。
func (service *Service) StopGoalStream(conversationID string) error {
	if service == nil || strings.TrimSpace(conversationID) == "" {
		return fmt.Errorf("conversation id is required")
	}
	var stream *ActiveStream
	for _, requestID := range service.broker.OtherConversationRequestIDs(conversationID, "") {
		if candidate, ok := service.broker.Get(requestID); ok {
			stream = candidate
			break
		}
	}
	if stream == nil {
		// 无活动流：把内存里的 goal 状态标记为 stopped。
		service.goalsMu.Lock()
		if goal, ok := service.goals[conversationID]; ok && goal.Status == GoalStatusRunning {
			goal.Status = GoalStatusStopped
			goal.StopReason = "用户手动停止"
			goal.UpdatedAt = time.Now().UTC()
		}
		service.goalsMu.Unlock()
		return nil
	}
	return service.handleCancelIntent(InboundIntent{
		RequestID:      stream.RequestID,
		ConversationID: conversationID,
		CancelReason:   "用户手动停止 goal",
	})
}

// handleGoalProviderError 在 goal 模式下把 provider 错误转为"注入错误摘要 + 续跑重试"，
// 超过 ErrorMaxRetries 才放行给 failStream。返回 (true, nil) 表示已重试（调用方 return）。
func (service *Service) handleGoalProviderError(stream *ActiveStream, conversationID string, turnSeq int64, requestID, modelCallID string, providerPass int, err error) (bool, error) {
	goal := stream.Goal
	if goal == nil {
		return false, nil
	}
	cfg := service.currentGoalConfig()
	if goal.ErrorRetries >= cfg.ErrorMaxRetries {
		return false, nil
	}
	goal.ErrorRetries++
	goal.ProviderPasses = providerPass
	goal.UpdatedAt = time.Now().UTC()
	appended, appendErr := service.appendGoalPromptContext(stream, conversationID, turnSeq, requestID, promptContextSourceGoalErrorRetry, goalErrorRetryReminder(errTextOf(err)).Message.Content)
	if appendErr != nil {
		return false, appendErr
	}
	if !appended {
		return false, nil // 已注入过同类提示，走 failStream 收口
	}
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "goal_error_retry", map[string]any{
			"provider_pass": providerPass,
			"retry_count":   goal.ErrorRetries,
			"error":         truncateText(errTextOf(err), 300),
		})
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		return true, err
	}
	if err := service.publishCheckpoint(requestID, conversationID); err != nil {
		return true, err
	}
	return true, service.requestProviderAction(stream, providerActionResume)
}