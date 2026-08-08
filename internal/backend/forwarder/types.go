// types.go 定义 forwarder 的核心数据结构与最小接口边界。
package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
)

type ConversationFile struct {
	SchemaVersion                   int                                   `json:"schema_version,omitempty"`
	ConversationID                  string                                `json:"conversation_id"`
	RootConversationID              string                                `json:"root_conversation_id"`
	ParentConversationID            string                                `json:"parent_conversation_id"`
	ParentToolCallID                string                                `json:"parent_tool_call_id"`
	SubagentTypeName                string                                `json:"subagent_type_name,omitempty"`
	AgentTranscriptsFolder          string                                `json:"agent_transcripts_folder,omitempty"`
	Mode                            string                                `json:"mode"`
	ContextVersion                  int64                                 `json:"context_version,omitempty"`
	CurrentLoopID                   string                                `json:"current_loop_id,omitempty"`
	CurrentLoopStatus               string                                `json:"current_loop_status,omitempty"`
	CurrentRequestID                string                                `json:"current_request_id,omitempty"`
	CurrentTurnSeq                  int64                                 `json:"current_turn_seq,omitempty"`
	TokenDetailsUsedTokens          uint32                                `json:"token_details_used_tokens,omitempty"`
	TokenDetailsMaxTokens           uint32                                `json:"token_details_max_tokens,omitempty"`
	AutoCompactionPending           bool                                  `json:"auto_compaction_pending,omitempty"`
	AutoCompactionPromptTokens      int64                                 `json:"auto_compaction_prompt_tokens,omitempty"`
	AutoCompactionReserveTokens     int64                                 `json:"auto_compaction_reserve_tokens,omitempty"`
	AutoCompactionTriggeredAt       string                                `json:"auto_compaction_triggered_at,omitempty"`
	AutoCompactionSourceModelCallID string                                `json:"auto_compaction_source_model_call_id,omitempty"`
	CurrentPlanText                 string                                `json:"current_plan_text,omitempty"`
	CurrentPlans                    map[string]*agentv1.PlanRegistryEntry `json:"current_plans,omitempty"`
	MCPTools                        []*agentv1.McpToolDefinition          `json:"mcp_tools,omitempty"`
	MCPToolsInitialized             bool                                  `json:"mcp_tools_initialized,omitempty"`
	// LastActivatedSkills 记录该会话最近一次稀疏激活注入的技能名列表，
	// 供子代理会话读取作保底候选（调用链传递）。不进 model-visible history，不影响 replay prefix。
	LastActivatedSkills []string                   `json:"last_activated_skills,omitempty"`
	CurrentTodos        []*agentv1.TodoItem        `json:"current_todos,omitempty"`
	ImportedTurnIDs     [][]byte                   `json:"imported_turn_ids,omitempty"`
	LatestRequestPrefix *ConversationRequestPrefix `json:"latest_request_prefix,omitempty"`
	LastProviderCall    *ConversationProviderCall  `json:"last_provider_call,omitempty"`
	CreatedAt           time.Time                  `json:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at"`
	NextTurnSeq         int64                      `json:"next_turn_seq"`
	NextEntrySeq        int64                      `json:"next_entry_seq"`
	Entries             []HistoryEntry             `json:"entries,omitempty"`
}

type ConversationRequestPrefix struct {
	RequestID               string    `json:"request_id,omitempty"`
	ModelCallID             string    `json:"model_call_id,omitempty"`
	Provider                string    `json:"provider,omitempty"`
	OpenAIEndpoint          string    `json:"openai_endpoint,omitempty"`
	Model                   string    `json:"model,omitempty"`
	PromptTokensTotal       int64     `json:"prompt_tokens_total,omitempty"`
	ReplayMessageCount      int       `json:"replay_message_count,omitempty"`
	CanonicalBodyHash       string    `json:"canonical_body_hash,omitempty"`
	FrontierHash            string    `json:"frontier_hash,omitempty"`
	FrontierPath            string    `json:"frontier_path,omitempty"`
	BreakpointCount         int       `json:"breakpoint_count,omitempty"`
	ExpectedCacheRead       bool      `json:"expected_cache_read,omitempty"`
	PreviousFrontierMatched bool      `json:"previous_frontier_matched,omitempty"`
	UpdatedAt               time.Time `json:"updated_at,omitempty"`
}

type ConversationProviderCall struct {
	RequestID   string    `json:"request_id,omitempty"`
	ModelCallID string    `json:"model_call_id,omitempty"`
	Provider    string    `json:"provider,omitempty"`
	Model       string    `json:"model,omitempty"`
	Status      string    `json:"status,omitempty"`
	ErrorText   string    `json:"error_text,omitempty"`
	Degraded    string    `json:"degraded,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type HistoryEntry struct {
	Seq              int64           `json:"seq"`
	TurnSeq          int64           `json:"turn_seq"`
	RequestID        string          `json:"request_id,omitempty"`
	Role             string          `json:"role"`
	Kind             string          `json:"kind"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ParentToolCallID string          `json:"parent_tool_call_id,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	CreatedAt        time.Time       `json:"created_at"`
}

type ConversationSummary struct {
	ConversationID string    `json:"conversation_id"`
	Mode           string    `json:"mode"`
	EntriesCount   int       `json:"entries_count"`
	NextTurnSeq    int64     `json:"next_turn_seq"`
	NextEntrySeq   int64     `json:"next_entry_seq"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type StreamStatus string

const (
	StreamStatusCreated   StreamStatus = "created"
	StreamStatusStreaming StreamStatus = "streaming"
	StreamStatusCompleted StreamStatus = "completed"
	StreamStatusCanceled  StreamStatus = "canceled"
	StreamStatusFailed    StreamStatus = "failed"
)

type StreamEvent struct {
	Message              *agentv1.AgentServerMessage
	End                  bool
	TerminalErrorCode    string
	TerminalErrorMessage string
}

type StreamSubscriber struct {
	Signal chan struct{}
}

type manualCompactionDirective struct {
	Requested   bool
	Instruction string
}

// providerCallTiming 记录一次 provider 调用的时序摘要，随 provider_call 事件落库，
// 供请求明细展示首字延迟（TTFB）与整体耗时（移植自 cursor2api 的请求阶段耗时时间线）。
type providerCallTiming struct {
	TTFTMS       int64  `json:"ttft_ms"`
	DurationMS   int64  `json:"duration_ms"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type ActiveStream struct {
	mu sync.Mutex

	// Goal 挂载 goal 会话状态；nil = 非 goal 会话（全路径旁路）。
	Goal                         *GoalState
	RequestID                    string
	ConversationID               string
	TurnSeq                      int64
	ModelID                      string
	ModelName                    string
	Mode                         agentv1.AgentMode
	LatestUserText               string
	Status                       StreamStatus
	ThinkingEffort               string
	CustomSystemPrompt           string
	MaxMode                      bool
	SubagentModelOverrides       map[string]runtimecore.SubagentModelOverrideSelection
	SelectedSubagentModels       []*agentv1.RequestedModel
	SelectedSubagentModelDetails []*agentv1.ModelDetails
	ManualCompaction             manualCompactionDirective

	CurrentModelCallID             string
	ProviderActive                 bool
	ProviderCancel                 func()
	ProviderPassCount              int
	MultitaskStartupCanceled       bool
	MultitaskCanceledProviderPass  int
	ToolInvocationCount            int
	AutoMultitaskDelegationStarted bool
	ActorMailbox                   chan streamCommandEnvelope
	ActorDone                      chan struct{}
	Phase                          TurnPhase
	PendingProviderAction          providerAction
	PendingProviderCompletion      *pendingTurnCompletion
	CurrentProviderToken           uint64
	CurrentCompactionToken         uint64
	// DelegationRunTerminals 记录已收尾委派（delegation_aggregate）的终态
	// SubagentRun（toolCallID → run）。publishCheckpoint 时随 checkpoint 的
	// subagent_runs_by_parent_tool_call_id 同步给 Cursor 客户端，客户端 UI
	// 据此把 Task 卡片渲染为 succeeded/error/aborted；运行中的委派则由
	// attachDelegationRunStates 从 PendingExecs 推导为 RUNNING。
	DelegationRunTerminals map[string]*agentv1.SubagentRunState
	// DelegationRunProgress 记录本地委派 worker 的最新可见进度（key=toolCallID）。
	// attachDelegationRunStates 会把它写入 RUNNING run 的 detail 随 checkpoint 推送，
	// 让 Cursor Task 卡片在运行期间持续显示实时进度；detail 变化会改变 wire hash，
	// 避免周期性 checkpoint 被 duplicate 去重跳过（否则卡片停留在 stopped）。
	DelegationRunProgress                       map[string]string
	TimerTokens                                 map[string]uint64
	StreamTimers                                map[string]*time.Timer
	ProviderAccumulatedText                     []byte
	ProviderAccumulatedReasoning                []byte
	ProviderAccumulatedReasoningSignature       string
	ProviderAccumulatedReasoningSignatureSource string
	ProviderAccumulatedReasoningItemID          string
	ProviderAccumulatedReasoningStatus          string
	ProviderAccumulatedReasoningSummary         json.RawMessage
	ProviderSyntheticThinkingStartedAt          time.Time
	ProviderSyntheticThinkingPublished          bool
	ProviderThinkingDeltaCount                  int
	ProviderThinkingCompletedCount              int
	ProviderThinkingSuppressedCount             int
	ProviderFinishReason                        string
	ProviderUsage                               turnUsageSnapshot
	ProviderTerminalToolInvocation              bool
	// SummaryEmittedTurn 记录最近一次已向 RunSSE 流发送会话摘要事件的 turnSeq，
	// 用于同一 turn 多次 provider pass 时去重（摘要每轮只发一次，内容取最终快照）。
	SummaryEmittedTurn int64
	// Wire-level checkpoint dedupe/rate limiting; persisted history is untouched.
	LastCheckpointWireHash        string
	LastCheckpointSentAt          time.Time
	CheckpointPublishTimer        *time.Timer
	CheckpointPublishPending      bool
	PendingCompaction             *PendingCompaction
	PendingCheckpointBlobWrites   map[uint32]pendingCheckpointBlobWrite
	PendingCheckpointBlobRequests map[string]uint32
	NextCheckpointBlobRequestID   uint32
	NextCheckpointRevision        uint64
	PendingCheckpoint             *pendingCheckpointPublish
	// TurnStaleGraceStartedAt 记录 turn-staleness 看门狗「阶段一（重对齐 append 序列）」的触发时刻。
	// 零值表示尚未进入宽限期；非零值表示已重对齐过序列并进入宽限，再次触发即走「阶段二强制收口」。
	TurnStaleGraceStartedAt time.Time
	// ContextOverflowCompactionAttempts 记录本回合因 context_length_exceeded 触发「强制压缩+重试」的次数，
	// 用于限制每轮最多重试若干次，避免压不动时无限循环。
	ContextOverflowCompactionAttempts int
	// MaxTokensRecoveryCap 记录因 max_tokens 超限被中转站拒绝后，重试时强制施加的 max_tokens 上限。
	// 由 recoverFromMaxTokensExceeded 从错误文本解析的中转站真实限制值设置；driveProvider 读取它覆盖预算。
	// 零值表示未触发恢复，沿用 catalog/配置预算。
	MaxTokensRecoveryCap int
	// MaxTokensRecoveryAttempts 记录本回合因 max_tokens 超限触发降级重试的次数，
	// 用于限制重试次数避免无限循环。
	MaxTokensRecoveryAttempts int
	// ShellSyntheticRecoveryInTurn 标记本回合发生过 shell 合成结果恢复（<shell-incomplete>），
	// 下一次 provider_call 记录 usage 时消费并标记 degraded（工具「假成功」，见 token_usage.go）。
	ShellSyntheticRecoveryInTurn bool
	// LastProviderTiming 记录最近一次 provider 调用的时序（TTFB/总耗时/结束原因），
	// 由 artifactRecorder.RecordLLMSummary 写入、recordTurnUsageSnapshot 读取落库。
	// 每次新调用开始（RecordLLMRequest）时清空，避免失败路径读到上一 pass 的旧值。
	LastProviderTiming *providerCallTiming
	// StaleToolResultSnipApplied 标记本 provider pass 在压缩评估阶段已对陈旧工具结果做过持久化 snip/prune。
	// driveProvider 据此在 maybeCompactBeforeProvider 返回「不压缩」后重新快照+编译一次，
	// 让后续 provider 请求用上 snip 后的新鲜历史（参考 tool_result_snip.go）。
	StaleToolResultSnipApplied bool
	// doomLoopCounts 记录以（工具名+规范化参数）签名计的连续相同工具调用次数（stream.mu 保护）。
	doomLoopCounts        map[string]int
	lastDoomLoopSignature string
	pendingDoomLoopNotice string

	Backlog                     []StreamEvent
	BacklogStartCursor          int
	Subscribers                 map[string]*StreamSubscriber
	CheckpointConversation      *ConversationFile
	PendingExecs                map[string]runtimecore.PendingExec
	PendingInteractions         map[string]runtimecore.PendingInteraction
	PartialToolCallIDs          map[string]struct{}
	PatchEditQueues             map[string][]queuedPatchEditOperation
	MCPToolServers              map[string]string
	MCPToolNames                map[string]string
	WorkspacePaths              []string
	TerminalsFolder             string
	RequestFileContents         map[string]string
	RecentCompletedExecs        map[uint32]time.Time
	RecentCompletedInteractions map[string]time.Time
	BackgroundShells            map[string]*BackgroundShellState
	BackgroundShellsByMessageID map[uint32]string
	BackgroundShellsByExecID    map[string]string
	BackgroundShellActions      map[string]time.Time
	TerminalCleanupTimer        *time.Timer
	TerminalCleanupSeq          atomic.Uint64

	CreatedAt time.Time
	UpdatedAt time.Time
}

type BackgroundShellState struct {
	ShellID            string
	Command            string
	WorkingDirectory   string
	PID                *uint32
	OriginalToolCallID string
	OriginalExecID     string
	OriginalMessageID  uint32
	ModelCallID        string
	ArgsJSON           []byte
	Status             string
	ExitCode           *int32
	StdoutBuffer       string
	StderrBuffer       string
	AwaitStdoutOffset  int
	AwaitStderrOffset  int
	CreatedAt          time.Time
	LastActivityAt     time.Time
	CompletedAt        time.Time
	StreamClosed       bool
}

type pendingTurnCompletion struct {
	ConversationID string
	RequestID      string
	TurnSeq        int64
	ModelCallID    string
	ProviderPass   int
	Usage          turnUsageSnapshot
	Disposition    pendingCompletionDisposition
}

type pendingCheckpointBlobWrite struct {
	Key      string
	Revision uint64
}

type checkpointTerminalActionKind uint8

const (
	checkpointTerminalActionNone checkpointTerminalActionKind = iota
	checkpointTerminalActionComplete
	checkpointTerminalActionCancel
)

type checkpointTerminalAction struct {
	kind          checkpointTerminalActionKind
	completion    pendingTurnCompletion
	cancelMessage string
}

type pendingCheckpointPublish struct {
	Revision       uint64
	State          *agentv1.ConversationStateStructure
	Required       map[string]struct{}
	TerminalAction checkpointTerminalAction
}

type PendingCompaction struct {
	Trigger                            string
	ContextTokens                      int64
	ContextWindowSize                  int64
	ContextUsagePercent                float64
	ReserveTokens                      int64
	MessageCount                       int32
	MessagesToCompact                  int32
	CompactTurnCount                   int32
	IsFirstCompaction                  bool
	ExistingSummary                    string
	CompactedTurns                     []compactedTurnSummary
	ManualInstruction                  string
	RequestSource                      string
	CurrentTurnSeq                     int64
	CurrentRequestID                   string
	CurrentUserText                    string
	PreserveCurrentTurnInputs          bool
	HookMessage                        string
	SummaryModelCallID                 string
	StartedAt                          time.Time
	ProjectionConversationID           string
	ProjectionRootConversationID       string
	ProjectionParentConversationID     string
	ProjectionParentToolCallID         string
	ProjectionModelKey                 string
	ProjectionContextVersion           int64
	ProjectionSummaryStartEntrySeq     int64
	ProjectionCoveredEntrySeq          int64
	ProjectionCoveredPrefixFingerprint string
}

type ProviderRequest struct {
	RequestID           string
	ConversationID      string
	RunID               string
	ModelCallID         string
	ModelID             string
	ModelName           string
	Role                string
	ParentModel         string
	ModelGroupID        string
	TaskID              string
	ExecutionMode       string
	SupervisorModel     string
	ReviewerModel       string
	Mode                agentv1.AgentMode
	ThinkingEffort      string
	MaxMode             bool
	Messages            []modeladapter.Message
	StableMessageCount  int
	Tools               []json.RawMessage
	MaxTokens           int
	RequestKnobs        map[string]any
	CompileSummary      string
	Observer            modeladapter.LLMArtifactObserver
	ArtifactPaths       *modeladapter.LLMArtifactPaths
	RequestBodyOverride map[string]any
}

type ProviderGateway interface {
	StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error
}

type ToolCatalog interface {
	Load(mode agentv1.AgentMode, subagentTypeName string) ([]json.RawMessage, []string, error)
}

type PromptReminders struct {
	SystemParts    []string
	TailMessages   []modeladapter.Message
	PromptContexts []PromptContextMessage
}

type ReminderInjector interface {
	Inject(mode agentv1.AgentMode, conversation *ConversationFile, replayMessages []modeladapter.Message, latestUserText string, toolNames []string) PromptReminders
}

type PromptContextMessage struct {
	Source      string
	Message     modeladapter.Message
	ContentHash string
	Persist     bool
}

type CompiledConversation struct {
	Mode               agentv1.AgentMode
	Messages           []modeladapter.Message
	StableMessageCount int
	Tools              []json.RawMessage
	CompileSummary     string
}

type ToolRequestKind string

const (
	ToolRequestExec        ToolRequestKind = "exec"
	ToolRequestInteraction ToolRequestKind = "interaction"
)

// providerTerminalError 表示底层 LLM/provider 返回的真实错误。
type providerTerminalError struct {
	cause error
}

// Error 返回 provider 错误的字符串形式。
func (err providerTerminalError) Error() string {
	if err.cause == nil {
		return "provider error"
	}
	return err.cause.Error()
}

// Unwrap 允许调用方继续取到底层原始错误。
func (err providerTerminalError) Unwrap() error {
	return err.cause
}

type toolResultEntryPayload struct {
	ToolCallID               string          `json:"tool_call_id"`
	ToolName                 string          `json:"tool_name"`
	Arguments                string          `json:"arguments,omitempty"`
	ResultText               string          `json:"result_text,omitempty"`
	ReasoningContent         string          `json:"reasoning_content,omitempty"`
	ReasoningSignature       string          `json:"reasoning_signature,omitempty"`
	ReasoningSignatureSource string          `json:"reasoning_signature_source,omitempty"`
	ReasoningItemID          string          `json:"reasoning_item_id,omitempty"`
	ReasoningStatus          string          `json:"reasoning_status,omitempty"`
	ReasoningSummary         json.RawMessage `json:"reasoning_summary,omitempty"`
	ProviderItemID           string          `json:"provider_item_id,omitempty"`
	ProviderCallID           string          `json:"provider_call_id,omitempty"`
	ProviderStatus           string          `json:"provider_status,omitempty"`
	ToolCall                 json.RawMessage `json:"tool_call,omitempty"`
}

type toolCallEntryPayload struct {
	ToolCallID               string          `json:"tool_call_id"`
	ToolName                 string          `json:"tool_name"`
	ReasoningContent         string          `json:"reasoning_content,omitempty"`
	ReasoningSignature       string          `json:"reasoning_signature,omitempty"`
	ReasoningSignatureSource string          `json:"reasoning_signature_source,omitempty"`
	ReasoningItemID          string          `json:"reasoning_item_id,omitempty"`
	ReasoningStatus          string          `json:"reasoning_status,omitempty"`
	ReasoningSummary         json.RawMessage `json:"reasoning_summary,omitempty"`
	ProviderItemID           string          `json:"provider_item_id,omitempty"`
	ProviderCallID           string          `json:"provider_call_id,omitempty"`
	ProviderStatus           string          `json:"provider_status,omitempty"`
	ToolCall                 json.RawMessage `json:"tool_call"`
}

type assistantTextPayload struct {
	Text                     string          `json:"text"`
	ReasoningContent         string          `json:"reasoning_content,omitempty"`
	ReasoningSignature       string          `json:"reasoning_signature,omitempty"`
	ReasoningSignatureSource string          `json:"reasoning_signature_source,omitempty"`
	ReasoningItemID          string          `json:"reasoning_item_id,omitempty"`
	ReasoningStatus          string          `json:"reasoning_status,omitempty"`
	ReasoningSummary         json.RawMessage `json:"reasoning_summary,omitempty"`
}

type metadataPayload struct {
	Type  string         `json:"type"`
	Value map[string]any `json:"value,omitempty"`
}

type promptContextEntryPayload struct {
	Source      string `json:"source"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash,omitempty"`
}

type modelMessageEntryPayload struct {
	Message modeladapter.Message `json:"message"`
}

type runtimeStateEntryPayload struct {
	PlanText string                                `json:"plan_text,omitempty"`
	Plans    map[string]*agentv1.PlanRegistryEntry `json:"plans,omitempty"`
	Todos    []*agentv1.TodoItem                   `json:"todos,omitempty"`
}

type compactionSummaryEntryPayload struct {
	Summary                   string `json:"summary"`
	Trigger                   string `json:"trigger,omitempty"`
	CurrentTurnSeq            int64  `json:"current_turn_seq,omitempty"`
	CurrentRequestID          string `json:"current_request_id,omitempty"`
	CompactTurnCount          int32  `json:"compact_turn_count,omitempty"`
	MessagesToCompact         int32  `json:"messages_to_compact,omitempty"`
	PreserveCurrentTurnInputs bool   `json:"preserve_current_turn_inputs,omitempty"`
}

type ModeSource string

const (
	ModeSourceUnknown           ModeSource = ""
	ModeSourceUserMessage       ModeSource = "user_message"
	ModeSourceStartPlanAction   ModeSource = "start_plan_action"
	ModeSourceExecutePlanAction ModeSource = "execute_plan_action"
	ModeSourceConversationState ModeSource = "conversation_state"
	ModeSourceSwitchModeTool    ModeSource = "switch_mode_tool"
)

type InboundIntent struct {
	Kind                         string
	RequestID                    string
	ConversationID               string
	ModelID                      string
	ModelName                    string
	ThinkingEffort               string
	CustomSystemPrompt           string
	MaxMode                      bool
	Mode                         agentv1.AgentMode
	HasExplicitMode              bool
	ModeSource                   ModeSource
	StartsRun                    bool
	ForceNewTurn                 bool
	SubagentTypeName             string
	SubagentModelOverrides       map[string]runtimecore.SubagentModelOverrideSelection
	SelectedSubagentModels       []*agentv1.RequestedModel
	SelectedSubagentModelDetails []*agentv1.ModelDetails
	ConversationState            *agentv1.ConversationStateStructure
	PreFetchedBlobs              []*agentv1.PreFetchedBlob
	UserMessage                  *agentv1.UserMessage
	RequestContext               *agentv1.RequestContext
	MCPToolsProvided             bool
	ClientMessage                *agentv1.AgentClientMessage
	ExecClientMessage            *agentv1.ExecClientMessage
	ExecClientControlMessage     *agentv1.ExecClientControlMessage
	InteractionResponse          *agentv1.InteractionResponse
	KVClientMessage              *agentv1.KvClientMessage
	CancelReason                 string
	IgnoredReason                string
	Prewarm                      bool
	ManualCompaction             manualCompactionDirective
	// GoalMode 标记该 run 以 goal 模式执行（/goal 前缀或前端面板发起）。
	GoalMode   bool
	GoalText   string
	GoalStrict bool // /goal --strict 标记（借鉴 Reasonix Strict 模式）
}

// normalizeMode 对外部传入的 mode 做最小归一化，但不再静默降级。
func normalizeMode(mode agentv1.AgentMode) agentv1.AgentMode {
	return mode
}

func isSupportedActiveMode(mode agentv1.AgentMode) bool {
	switch normalizeMode(mode) {
	case agentv1.AgentMode_AGENT_MODE_AGENT,
		agentv1.AgentMode_AGENT_MODE_ASK,
		agentv1.AgentMode_AGENT_MODE_PLAN,
		agentv1.AgentMode_AGENT_MODE_DEBUG,
		agentv1.AgentMode_AGENT_MODE_MULTITASK:
		return true
	default:
		return false
	}
}

func validateSupportedActiveMode(mode agentv1.AgentMode) (agentv1.AgentMode, error) {
	normalized := normalizeMode(mode)
	if !isSupportedActiveMode(normalized) {
		return agentv1.AgentMode_AGENT_MODE_UNSPECIFIED, fmt.Errorf("unsupported active mode: %s", normalized.String())
	}
	return normalized, nil
}

func resolveExplicitMode(mode agentv1.AgentMode, source ModeSource) (agentv1.AgentMode, ModeSource, bool, error) {
	normalized, err := validateSupportedActiveMode(mode)
	if err != nil {
		return agentv1.AgentMode_AGENT_MODE_UNSPECIFIED, source, true, fmt.Errorf("%w (source=%s)", err, strings.TrimSpace(string(source)))
	}
	return normalized, source, true, nil
}

// modeAlias 把协议枚举转换为写入 JSON history 的简短模式名。
func modeAlias(mode agentv1.AgentMode) (string, error) {
	switch normalizeMode(mode) {
	case agentv1.AgentMode_AGENT_MODE_AGENT:
		return "agent", nil
	case agentv1.AgentMode_AGENT_MODE_ASK:
		return "ask", nil
	case agentv1.AgentMode_AGENT_MODE_PLAN:
		return "plan", nil
	case agentv1.AgentMode_AGENT_MODE_DEBUG:
		return "debug", nil
	case agentv1.AgentMode_AGENT_MODE_MULTITASK:
		return "multitask", nil
	default:
		return "", fmt.Errorf("unsupported mode alias: %s", normalizeMode(mode).String())
	}
}

// parseModeAlias 把写入 JSON history 的模式名恢复为协议枚举。
func parseModeAlias(raw string) (agentv1.AgentMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "agent":
		return agentv1.AgentMode_AGENT_MODE_AGENT, nil
	case "ask":
		return agentv1.AgentMode_AGENT_MODE_ASK, nil
	case "plan":
		return agentv1.AgentMode_AGENT_MODE_PLAN, nil
	case "debug":
		return agentv1.AgentMode_AGENT_MODE_DEBUG, nil
	case "multitask":
		return agentv1.AgentMode_AGENT_MODE_MULTITASK, nil
	default:
		return agentv1.AgentMode_AGENT_MODE_UNSPECIFIED, fmt.Errorf("unsupported mode alias: %q", strings.TrimSpace(raw))
	}
}

func parseTargetModeID(raw string) (agentv1.AgentMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "agent":
		return agentv1.AgentMode_AGENT_MODE_AGENT, nil
	case "ask":
		return agentv1.AgentMode_AGENT_MODE_ASK, nil
	case "plan":
		return agentv1.AgentMode_AGENT_MODE_PLAN, nil
	case "debug":
		return agentv1.AgentMode_AGENT_MODE_DEBUG, nil
	case "multitask":
		return agentv1.AgentMode_AGENT_MODE_MULTITASK, nil
	default:
		return agentv1.AgentMode_AGENT_MODE_UNSPECIFIED, fmt.Errorf("unsupported target mode id: %q", strings.TrimSpace(raw))
	}
}
