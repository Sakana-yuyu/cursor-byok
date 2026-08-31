// service.go 实现 forwarder 装配与 RunSSE/Bidi 主入口；职责块见同包 service_*.go。
//
// Subpackages: forwarder/runqueue — per-conversation run serialization.
package forwarder

import (
	"context"
	"cursor/internal/logger"
	"cursor/internal/safego"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
	"cursor/internal/appdata"
	execbridge "cursor/internal/backend/agent/bridge/exec"
	interactionbridge "cursor/internal/backend/agent/bridge/interaction"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	protocol "cursor/internal/backend/agent/protocol"
	"cursor/internal/backend/delegation"
	"cursor/internal/promptinject"
)

const (
	completedExecRetention         = 15 * time.Second
	defaultNonStreamingCloseGrace  = 30 * time.Second
	defaultSummaryCompletedThought = "Chat context summarized"
	providerDefaultMaxOutputTokens = 65536
	providerOutputSafetyTokens     = 1024

	// doomLoopThreshold 连续相同工具调用达到该次数时，向模型注入"请改变策略"提示。
	doomLoopThreshold = 3
	// doomLoopHardLimit 连续相同工具调用达到该次数时，中断本轮（返回可恢复错误）。
	doomLoopHardLimit = 5

	runtimeThinkingEffortParameterID = "thinking_effort"
	checkpointMinSendInterval        = 1500 * time.Millisecond
)

type Service struct {
	store                  *ConversationFileStore
	usageStore             *UsageFileStore
	codebaseIndexStore     *CodebaseIndexStore
	docsIndexStore         *DocsIndexStore
	rules                  *UserRuleStore
	projector              *HistoryProjector
	compiler               PromptCompiler
	toolCatalog            *DefaultToolCatalog
	promptInjection        *promptinject.Manager
	provider               ProviderGateway
	resolver               modeladapter.ChannelResolver
	modelMemory            agentModelMemory
	maxTokensPersister     maxTokensConfigPersister
	contextWindowPersister contextWindowPersister
	scanConfig             skillMCPScanConfigProvider
	mcpRuntime             *MCPRuntimeRegistry
	broker                 *StreamBroker
	recorder               *artifactRecorder
	debug                  *debugRecorder
	// catalogUncoveredReported 记录已上报过「目录未覆盖」审计事件的模型名，
	// 避免每个请求重复刷日志（进程内一次即可，覆盖后由用户配置/目录补录解决）。
	catalogUncoveredReported sync.Map
	execBridge               execbridge.ExecBridge
	interactionBridge        interactionbridge.InteractionBridge
	appendSeq                *appendSequenceTracker
	runQueue                 *runQueue
	startOwnedRunHook        func(InboundIntent) error
	cursorDelegation         *cursorDelegationBridge
	localDelegation          *localDelegatedAgentAdapter
	delegationConfig         delegation.RuntimeConfigProvider
	executorRegistry         *delegation.ExecutorRegistry
	computerUseCfg           computerUseConfigProvider
	multitaskDelegation      *multitaskDelegationCoordinator
	delegationRuntimeMu      sync.Mutex
	nativeDelegations        map[string]*nativeDelegationRuntime
	nativeDelegationLimiter  *nativeDelegationLimiter
	visionRunsMu             sync.Mutex
	visionRuns               map[string]*visionDelegationRun
	visionCacheMu            sync.Mutex
	visionCache              map[string]visionCacheEntry
	visionCacheOrder         []string // LRU 顺序：头部最久未使用，尾部最近使用
	visionInflight           map[string]*visionInflightCall
	visionImageMu            sync.Mutex
	visionImageFiles         map[string][]string
	// visionArchiveMu 保护 visionArchive：会话级图片识图结果归档。
	// key = conversationID#imageHash，命中后直接用归档文本替换图片 part，
	// 不再重复调识图模型，也避免历史图片反复进入 provider 上下文。
	// 归档同时落盘到 history/<conversationID>/vision-archive.json，
	// 进程重启后（同会话、同图片内容）仍可命中；visionArchiveLoaded 记录
	// 已懒加载过的会话，避免重复读盘。
	visionArchiveMu          sync.Mutex
	visionArchive            map[string]visionArchiveEntry
	visionArchiveLimit       int
	visionArchiveLoaded      map[string]struct{}
	provider400RecoveryMu    sync.Mutex
	provider400RecoveryTurns map[string]struct{}
	// backgroundedDelegationMu 保护 backgroundedDelegations：记录因「新消息顶掉当前
	// turn」而转入后台的委派执行（key = exec_id）。父流随后进入终态，这些执行的迟到
	// 结果不能写回死流，必须凭该记录按会话归属重新落地。
	backgroundedDelegationMu sync.Mutex
	backgroundedDelegations  map[string]backgroundedDelegationExec
	// childParentLinkMu 保护 childParentLinks：记录客户端在子代理 RunSSE 上发来的
	// 父链路 HTTP 头（key = 子 request_id）。native 子代理是独立 conversation，
	// 这些头是后端唯一能拿到父会话/父 tool_call 的来源。
	childParentLinkMu sync.Mutex
	childParentLinks  map[string]childParentLink
	// conversationActivityMu 保护 conversationLastActivity：
	// 记录每个 conversation 最近一次模型输出/思考/工具活动，供 native 子代理
	// 无进展看门狗判断「子代理是否仍在工作」，避免长文本生成/长思考被误判超时。
	conversationActivityMu   sync.Mutex
	conversationLastActivity map[string]time.Time
	checkpointBlobMu         sync.Mutex
	checkpointBlobs          map[string]*checkpointBlobCacheEntry
	recentWorkspaceRootMu    sync.RWMutex
	recentWorkspaceRoot      string
	// nonStreamingCloseGrace returns the bounded wait period for non-streaming
	// exec recovery after stream_close. Production uses a conservative default;
	// tests may override via direct field assignment.
	nonStreamingCloseGrace func() time.Duration
	// nonStreamingRecoveryTimer, if non-nil, provides the timer channel used
	// by the non-streaming recovery goroutine instead of time.After. Tests
	// inject a test-controlled channel to trigger timeouts deterministically.
	nonStreamingRecoveryTimer func(execID string, grace time.Duration) <-chan time.Time
	// visionProxyPassBudget 控制自动识图在主模型首字前可占用的总时间。
	// 仅测试可覆盖；生产使用 visionProxyPassTimeout，避免把该运行时策略持久化进历史。
	visionProxyPassBudget func() time.Duration
}

// RecentWorkspaceRoot returns the latest non-empty workspace root observed
// during request asset enrichment. It is runtime-only state.
func (service *Service) RecentWorkspaceRoot() string {
	if service == nil {
		return ""
	}
	service.recentWorkspaceRootMu.RLock()
	defer service.recentWorkspaceRootMu.RUnlock()
	return service.recentWorkspaceRoot
}

func (service *Service) setRecentWorkspaceRoot(workspaceRoot string) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if service == nil || workspaceRoot == "" {
		return
	}
	service.recentWorkspaceRootMu.Lock()
	service.recentWorkspaceRoot = workspaceRoot
	service.recentWorkspaceRootMu.Unlock()
}

type agentModelMemory interface {
	LastAgentModelHash() string
	SaveLastAgentModelHash(context.Context, string) error
}

// computerUseConfigProvider 暴露 ComputerUse 执行模式配置（desktop/browser），
// 由 *serverconfig.Manager 实现（在 NewService 中通过类型断言注入）。
// 返回基础类型避免 config<->forwarder 循环依赖。
type computerUseConfigProvider interface {
	ComputerUseMode() (mode string, browserStartURL string)
}

// maxTokensConfigPersister 允许转发层把解析到的中转站 max_tokens 限制
// 持久化到命中的具体渠道配置（按 channelID 匹配），实现「只修正该渠道」而非全局修改。
// 由 *serverconfig.Manager 实现（在 NewService 中通过类型断言注入）。
type maxTokensConfigPersister interface {
	PersistChannelMaxTokensCap(ctx context.Context, channelID string, maxTokens int) error
}

// contextWindowPersister 允许转发层把中转站自适应探测到的真实上下文窗口
// 持久化到命中的具体渠道配置（按 channelID 匹配），实现「只修正该渠道」而非全局修改。
// 由 *serverconfig.Manager 实现（在 NewService 中通过类型断言注入）。
type contextWindowPersister interface {
	PersistChannelContextWindow(ctx context.Context, channelID string, contextWindowTokens int) error
}

// PromptInjection 返回提示词注入管理器，供外部（如路由分发）按需读取
// 当前 commit 来源、语言等运行时配置。Manager 自身每次读取都从磁盘 reload，
// 因此返回的引用始终能拿到最新配置。
func (service *Service) PromptInjection() *promptinject.Manager {
	if service == nil {
		return nil
	}
	return service.promptInjection
}

// NewService 使用默认依赖创建 forwarder 服务。
// transcriptBackfillOnce 保证 Cursor 记录回填每个进程只执行一次：
// NewService 在启动链路里会被构造两次（NewHost 与 Host.Start 各一次），
// 配置保存时的 rebuild 也会再次构造，重复同步会白白占用磁盘 I/O。
var transcriptBackfillOnce sync.Once

func NewService(historyRoot string, resolver modeladapter.ChannelResolver) *Service {
	return newService(historyRoot, resolver, nil)
}

func NewServiceWithExecutorRegistry(historyRoot string, resolver modeladapter.ChannelResolver, registry *delegation.ExecutorRegistry) *Service {
	return newService(historyRoot, resolver, registry)
}

func newService(historyRoot string, resolver modeladapter.ChannelResolver, registry *delegation.ExecutorRegistry) *Service {
	toolCatalog := NewToolCatalog()
	projector := NewHistoryProjector()
	store := NewConversationFileStore(historyRoot)
	broker := NewStreamBroker()
	rules := NewUserRuleStore(appdata.RulesRootPath())
	skills := NewSkillStore(appdata.SkillsRootPath())
	// 注入会话存储，启用技能稀疏激活的调用链父子传递（子代理读父激活集）。
	skills.SetConversationStore(store)
	promptInjection := promptinject.New()
	if _, err := promptInjection.Load(); err != nil {
		// A malformed optional prompt config must not prevent normal BYOK startup.
		promptInjection = promptinject.New()
	}
	var modelMemory agentModelMemory
	if candidate, ok := resolver.(agentModelMemory); ok {
		modelMemory = candidate
	}
	var debugConfig debugLogConfig
	if candidate, ok := resolver.(debugLogConfig); ok {
		debugConfig = candidate
	}
	var maxTokensPersister maxTokensConfigPersister
	if candidate, ok := resolver.(maxTokensConfigPersister); ok {
		maxTokensPersister = candidate
	}
	var ctxWindowPersister contextWindowPersister
	if candidate, ok := resolver.(contextWindowPersister); ok {
		ctxWindowPersister = candidate
	}
	var scanConfig skillMCPScanConfigProvider
	if candidate, ok := resolver.(skillMCPScanConfigProvider); ok {
		scanConfig = candidate
	}
	var delegationConfig delegation.RuntimeConfigProvider
	if candidate, ok := resolver.(delegation.RuntimeConfigProvider); ok {
		delegationConfig = candidate
	}
	var computerUseCfg computerUseConfigProvider
	if candidate, ok := resolver.(computerUseConfigProvider); ok {
		computerUseCfg = candidate
	}
	debug := newDebugRecorder(historyRoot, broker, debugConfig)
	service := &Service{
		store:                  store,
		usageStore:             NewUsageFileStore(historyRoot),
		codebaseIndexStore:     NewCodebaseIndexStore(appdata.CodebaseIndexRootPath()),
		docsIndexStore:         NewDocsIndexStore(appdata.DocsIndexRootPath()),
		rules:                  rules,
		projector:              projector,
		compiler:               NewPromptCompiler(projector, toolCatalog, NewReminderInjector(), rules, skills, promptInjection),
		toolCatalog:            toolCatalog,
		promptInjection:        promptInjection,
		provider:               NewProviderGateway(resolver),
		resolver:               resolver,
		modelMemory:            modelMemory,
		maxTokensPersister:     maxTokensPersister,
		contextWindowPersister: ctxWindowPersister,
		scanConfig:             scanConfig,
		delegationConfig:       delegationConfig,
		executorRegistry:       registry,
		computerUseCfg:         computerUseCfg,
		mcpRuntime:             SharedMCPRuntimeRegistry(),
		broker:                 broker,
		recorder:               newArtifactRecorder(store, broker, debug),
		debug:                  debug,
		execBridge:             execbridge.NewBridge(),
		interactionBridge:      interactionbridge.NewBridge(),
		appendSeq:              newAppendSequenceTracker(),
		runQueue:               newRunQueue(),
		nativeDelegations:      make(map[string]*nativeDelegationRuntime),
		// 视觉委派相关的三个 map 必须在构造时初始化：
		// 缺失会导致 beginVisionRun 向 nil map 写入，触发
		// "assignment to entry in nil map" panic，杀死整个 Wails 主进程
		// （视觉委派一触发就闪退的根因）。
		visionRuns:               make(map[string]*visionDelegationRun),
		visionCache:              make(map[string]visionCacheEntry),
		visionInflight:           make(map[string]*visionInflightCall),
		visionImageFiles:         make(map[string][]string),
		visionArchive:            make(map[string]visionArchiveEntry),
		visionArchiveLimit:       visionArchiveMaxEntries,
		checkpointBlobs:          make(map[string]*checkpointBlobCacheEntry),
		conversationLastActivity: make(map[string]time.Time),
		nonStreamingCloseGrace:   func() time.Duration { return defaultNonStreamingCloseGrace },
	}
	service.cursorDelegation = newCursorDelegationBridge(service)
	service.localDelegation = newLocalDelegatedAgentAdapter(service)
	service.multitaskDelegation = newMultitaskDelegationCoordinator(service, delegationConfig)
	service.startHistoryMaintenance()
	// Cursor 记录回填是 best-effort 维护任务：历史目录可能包含上千会话、数百 MB 数据，
	// 同步扫描会拖慢应用窗口与后端启动（实测各约 8s）。改为进程内只执行一次的后台任务。
	transcriptBackfillOnce.Do(func() {
		safego.Go("forwarder:transcript-backfill", func() {
			store.SyncAllCursorTranscriptsBestEffort()
		})
	})
	service.registerStreamLifecycleHooks()
	return service
}

// ResetUsageMetrics 清空当前 forwarder 持有的全部用量统计。
func (service *Service) ResetUsageMetrics() error {
	if service == nil || service.usageStore == nil {
		return nil
	}
	return service.usageStore.Reset()
}

// newServiceWithDependencies 主要用于测试场景，允许注入替身依赖。
func newServiceWithDependencies(store *ConversationFileStore, projector *HistoryProjector, compiler PromptCompiler, provider ProviderGateway, broker *StreamBroker) *Service {
	historyRoot := ""
	if store != nil {
		historyRoot = store.HistoryDir()
	}
	debug := newDebugRecorder(historyRoot, broker, nil)
	service := &Service{
		store:                    store,
		rules:                    NewUserRuleStore(appdata.RulesRootPath()),
		projector:                projector,
		compiler:                 compiler,
		provider:                 provider,
		broker:                   broker,
		usageStore:               NewUsageFileStore(store.HistoryDir()),
		codebaseIndexStore:       NewCodebaseIndexStore(appdata.CodebaseIndexRootPath()),
		docsIndexStore:           NewDocsIndexStore(appdata.DocsIndexRootPath()),
		recorder:                 newArtifactRecorder(store, broker, debug),
		debug:                    debug,
		execBridge:               execbridge.NewBridge(),
		interactionBridge:        interactionbridge.NewBridge(),
		mcpRuntime:               SharedMCPRuntimeRegistry(),
		appendSeq:                newAppendSequenceTracker(),
		runQueue:                 newRunQueue(),
		nativeDelegations:        make(map[string]*nativeDelegationRuntime),
		visionRuns:               make(map[string]*visionDelegationRun),
		visionCache:              make(map[string]visionCacheEntry),
		visionInflight:           make(map[string]*visionInflightCall),
		visionImageFiles:         make(map[string][]string),
		visionArchive:            make(map[string]visionArchiveEntry),
		visionArchiveLimit:       visionArchiveMaxEntries,
		checkpointBlobs:          make(map[string]*checkpointBlobCacheEntry),
		conversationLastActivity: make(map[string]time.Time),
		nonStreamingCloseGrace:   func() time.Duration { return defaultNonStreamingCloseGrace },
	}
	service.cursorDelegation = newCursorDelegationBridge(service)
	service.localDelegation = newLocalDelegatedAgentAdapter(service)
	service.multitaskDelegation = newMultitaskDelegationCoordinator(service, nil)
	service.registerStreamLifecycleHooks()
	return service
}

// BidiAppend 处理 legacy Bidi 上行，把用户输入和外部结果归一化后写入 history。
func (service *Service) BidiAppend(ctx context.Context, req *connect.Request[aiserverv1.BidiAppendRequest]) (*connect.Response[aiserverv1.BidiAppendResponse], error) {
	if service == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("forwarder service is nil"))
	}
	requestID := protocol.NormalizeRequestID(protocol.ReadAppendRequestID(req.Msg))
	if requestID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("request_id is required"))
	}
	appendSeqno := req.Msg.GetAppendSeqno()
	dataHex := req.Msg.GetData()
	dataBinary := req.Msg.GetDataBinary()
	debugData := protocol.BidiAppendDebugData(dataHex, dataBinary)
	appendTicket, staleAppend, err := service.appendSeq.Acquire(ctx, requestID, appendSeqno)
	if err != nil {
		return nil, connect.NewError(connect.CodeCanceled, err)
	}
	if staleAppend {
		logger.Infof("forwarder ignored stale bidi append request_id=%s append_seqno=%d", requestID, appendSeqno)
		service.debug.LogBidiRaw(ctx, requestID, "", appendSeqno, debugData, "stale", nil)
		return connect.NewResponse(&aiserverv1.BidiAppendResponse{}), nil
	}
	defer appendTicket.Release()
	message, clientKind, canonicalHex, err := protocol.DecodeBidiAppendAgentClientMessage(dataHex, dataBinary)
	if err != nil {
		service.debug.LogBidiRaw(ctx, requestID, "", appendSeqno, debugData, "decode_error", map[string]any{
			"error": err.Error(),
		})
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	intent, err := service.decodeInboundIntent(requestID, message, clientKind)
	if err != nil {
		service.debug.LogBidiRaw(ctx, requestID, "", appendSeqno, debugData, "intent_error", map[string]any{
			"client_kind": strings.TrimSpace(clientKind),
			"error":       err.Error(),
		})
		service.debug.LogBidiDecoded(ctx, requestID, "", appendSeqno, clientKind, message, InboundIntent{RequestID: requestID}, map[string]any{
			"error": err.Error(),
		})
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	service.debug.LogBidiRaw(ctx, requestID, intent.ConversationID, appendSeqno, canonicalHex, "accepted", map[string]any{
		"client_kind": strings.TrimSpace(clientKind),
	})
	service.debug.LogBidiDecoded(ctx, requestID, intent.ConversationID, appendSeqno, clientKind, message, intent, nil)
	if err := service.dispatchInboundIntent(intent); err != nil {
		if shouldAcknowledgeInterruptedInboundIntent(intent, err) {
			service.debug.LogRuntime(ctx, requestID, intent.ConversationID, "dispatch_interrupted_ignored", map[string]any{
				"kind":  strings.TrimSpace(intent.Kind),
				"error": err.Error(),
			})
			return connect.NewResponse(&aiserverv1.BidiAppendResponse{}), nil
		}
		service.debug.LogRuntime(ctx, requestID, intent.ConversationID, "dispatch_error", map[string]any{
			"kind":  strings.TrimSpace(intent.Kind),
			"error": err.Error(),
		})
		code := connect.CodeInvalidArgument
		if strings.TrimSpace(intent.Kind) == "run" {
			code = connect.CodeInternal
		}
		return nil, connect.NewError(code, err)
	}
	service.debug.LogRuntime(ctx, requestID, intent.ConversationID, "inbound_intent_dispatched", map[string]any{
		"kind":            strings.TrimSpace(intent.Kind),
		"thinking_effort": strings.TrimSpace(intent.ThinkingEffort),
		"prewarm":         intent.Prewarm,
		"ignored_reason":  strings.TrimSpace(intent.IgnoredReason),
	})

	return connect.NewResponse(&aiserverv1.BidiAppendResponse{}), nil
}

func shouldAcknowledgeInterruptedInboundIntent(intent InboundIntent, err error) bool {
	if !errors.Is(err, errProviderLoopInterrupted) {
		return false
	}
	switch strings.TrimSpace(intent.Kind) {
	case "metadata", "kv_result", "exec_result", "exec_control", "interaction_result", "cancel":
		return true
	default:
		return false
	}
}

// RunSSE 订阅指定 request 的活动流，优先回放 backlog，在 backlog 清空期间按 5 秒周期发送心跳。
func (service *Service) RunSSE(ctx context.Context, req *connect.Request[aiserverv1.BidiRequestId], stream *connect.ServerStream[agentv1.AgentServerMessage]) error {
	if service == nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("forwarder service is nil"))
	}
	requestID := protocol.NormalizeRequestID(protocol.ReadBidiRequestID(req.Msg))
	if requestID == "" {
		return buildRunSSECustomError(connect.CodeInvalidArgument, "请求参数无效", fmt.Errorf("request_id is required"))
	}
	// 子代理的父链路只存在于 RunSSE 请求头里，run_request 不带任何父信息。
	service.rememberChildParentLink(requestID, req.Header())
	subscriberID, signal, cursor, err := service.broker.Subscribe(requestID)
	if err != nil {
		return buildRunSSECustomError(connect.CodeInvalidArgument, "请求参数无效", err)
	}
	service.debug.LogRunSSE(ctx, requestID, "", "subscribe", map[string]any{
		"subscriber_id": subscriberID,
	})
	defer func() {
		remaining := service.broker.Unsubscribe(requestID, subscriberID)
		service.debug.LogRunSSE(context.Background(), requestID, "", "unsubscribe_state", service.runSSEStateDebugFields(requestID))
		service.debug.LogRunSSE(context.Background(), requestID, "", "unsubscribe", map[string]any{
			"subscriber_id":         subscriberID,
			"remaining_subscribers": remaining,
		})
		if remaining == 0 {
			// RunSSE 连接短暂抖动时，给活跃 provider 一段重连宽限期，
			// 避免把本来还能正常收口的请求直接打成 context canceled。
			if !service.scheduleOrphanCancelActor(requestID, "[canceled] RunSSE client disconnected") {
				service.broker.RemoveIfIdle(requestID)
			}
		}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		backlog, err := service.broker.ReadFromCursor(requestID, cursor)
		if err != nil {
			service.debug.LogRunSSE(ctx, requestID, "", "read_error", map[string]any{
				"cursor": cursor,
				"error":  err.Error(),
			})
			return buildRunSSECustomError(connect.CodeInternal, "Server Error", err)
		}
		if len(backlog) > 0 {
			for _, event := range backlog {
				if event.Message != nil {
					if err := stream.Send(event.Message); err != nil {
						service.debug.LogRunSSE(ctx, requestID, "", "send_error", map[string]any{
							"cursor":       cursor,
							"message_case": agentServerMessageCase(event.Message),
							"message_size": proto.Size(event.Message),
							"message":      protoJSONDebugPayload(event.Message),
							"error":        err.Error(),
						})
						return err
					}
					service.debug.LogRunSSELazy(ctx, requestID, "", "send_message", func() map[string]any {
						return runSSEMessageDebugFields(cursor, event.Message)
					})
				}
				cursor++
				if event.End {
					service.debug.LogRunSSE(ctx, requestID, "", "terminal", map[string]any{
						"cursor":                 cursor,
						"terminal_error_code":    strings.TrimSpace(event.TerminalErrorCode),
						"terminal_error_message": strings.TrimSpace(event.TerminalErrorMessage),
						"trace_id":               strings.TrimSpace(event.TerminalTraceID),
						"error_code":             strings.TrimSpace(event.TerminalAppErrorCode),
						"disposition":            strings.TrimSpace(event.TerminalDisposition),
						"retry_attempt":          event.TerminalRetryAttemptCount,
					})
					return buildTerminalStreamError(event)
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			service.debug.LogRunSSE(ctx, requestID, "", "client_context_done", map[string]any{
				"cursor": cursor,
				"error":  ctx.Err().Error(),
			})
			service.debug.LogRunSSE(context.Background(), requestID, "", "client_context_done_state", service.runSSEStateDebugFields(requestID))
			if backlog, err := service.broker.ReadFromCursor(requestID, cursor); err == nil {
				for _, event := range backlog {
					cursor++
					if event.End {
						service.debug.LogRunSSE(context.Background(), requestID, "", "terminal_after_context_done", map[string]any{
							"cursor":                 cursor,
							"terminal_error_code":    strings.TrimSpace(event.TerminalErrorCode),
							"terminal_error_message": strings.TrimSpace(event.TerminalErrorMessage),
							"trace_id":               strings.TrimSpace(event.TerminalTraceID),
							"error_code":             strings.TrimSpace(event.TerminalAppErrorCode),
							"disposition":            strings.TrimSpace(event.TerminalDisposition),
							"retry_attempt":          event.TerminalRetryAttemptCount,
						})
						return buildTerminalStreamError(event)
					}
				}
			} else {
				service.debug.LogRunSSE(context.Background(), requestID, "", "read_error", map[string]any{
					"cursor": cursor,
					"error":  err.Error(),
				})
				return buildRunSSECustomError(connect.CodeInternal, "Server Error", err)
			}
			return nil
		case <-signal:
			continue
		case <-ticker.C:
		}
		if backlog, err := service.broker.ReadFromCursor(requestID, cursor); err != nil {
			service.debug.LogRunSSE(ctx, requestID, "", "read_error", map[string]any{
				"cursor": cursor,
				"error":  err.Error(),
			})
			return buildRunSSECustomError(connect.CodeInternal, "Server Error", err)
		} else if len(backlog) > 0 {
			continue
		}
		heartbeat := buildHeartbeatMessage()
		if err := stream.Send(heartbeat); err != nil {
			service.debug.LogRunSSE(ctx, requestID, "", "heartbeat_error", map[string]any{
				"cursor":       cursor,
				"message_case": agentServerMessageCase(heartbeat),
				"message_size": proto.Size(heartbeat),
				"message":      protoJSONDebugPayload(heartbeat),
				"error":        err.Error(),
			})
			return err
		}
		service.debug.LogRunSSE(ctx, requestID, "", "heartbeat", map[string]any{
			"cursor":       cursor,
			"message_case": agentServerMessageCase(heartbeat),
			"message_size": proto.Size(heartbeat),
			"message":      protoJSONDebugPayload(heartbeat),
		})
	}
}

func (service *Service) runSSEStateDebugFields(requestID string) map[string]any {
	fields := map[string]any{}
	if service == nil || service.broker == nil {
		return fields
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		fields["stream_found"] = false
		return fields
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	fields["stream_found"] = true
	fields["conversation_id"] = strings.TrimSpace(stream.ConversationID)
	fields["status"] = string(stream.Status)
	fields["phase"] = string(stream.Phase)
	fields["provider_active"] = stream.ProviderActive
	fields["provider_pass_count"] = stream.ProviderPassCount
	fields["current_model_call_id"] = strings.TrimSpace(stream.CurrentModelCallID)
	fields["pending_exec_count"] = len(stream.PendingExecs)
	fields["pending_interaction_count"] = len(stream.PendingInteractions)
	fields["subscriber_count"] = len(stream.Subscribers)
	fields["backlog_count"] = len(stream.Backlog)
	fields["backlog_start_cursor"] = stream.BacklogStartCursor
	fields["delegation_terminal_count"] = len(stream.DelegationRunTerminals)
	if stream.CheckpointConversation != nil {
		fields["checkpoint_context_version"] = stream.CheckpointConversation.ContextVersion
		fields["checkpoint_entry_count"] = len(stream.CheckpointConversation.Entries)
	}
	return fields
}

// decodeInboundIntent 把 legacy AgentClientMessage 映射为 forwarder 内部 intent。

func (service *Service) loadPreviousSummaryReplay(conversationID string) ([][]byte, bool, error) {
	if service == nil || strings.TrimSpace(conversationID) == "" {
		return nil, false, nil
	}
	return service.loadLatestCarryForwardReplay(conversationID)
}

func (service *Service) snapshotVisibleTurns(conversation *ConversationFile) ([][]byte, error) {
	if service == nil || service.projector == nil || conversation == nil {
		return nil, nil
	}
	projection, err := service.projector.ProjectCheckpointProjection(conversation)
	if err != nil {
		return nil, err
	}
	if projection == nil || projection.State == nil {
		return nil, fmt.Errorf("checkpoint projection is empty")
	}
	return cloneByteSlices(projection.State.GetTurns()), nil
}

// Shutdown 在服务退出前主动取消所有未终态活动流。
// 这样 RunSSE 能先发出 TurnEnded + canceled endstream，避免 Cursor 只看到连接被硬断后报 RetriableError: Canceled。
func (service *Service) Shutdown(ctx context.Context) error {
	if service == nil {
		return nil
	}
	if service.mcpRuntime != nil {
		defer service.mcpRuntime.Close()
	}
	if service.cursorDelegation != nil {
		defer service.cursorDelegation.Close()
	}
	if service.multitaskDelegation != nil {
		defer service.multitaskDelegation.Close()
	}
	if service.runQueue != nil {
		// 关闭调度器：清空所有排队项并禁止晋升，避免关闭期间启动新的 provider 调用。
		service.runQueue.Close()
	}
	if service.broker == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestIDs := service.broker.ActiveRequestIDs()
	if len(requestIDs) == 0 {
		return nil
	}
	logger.Infof("forwarder shutdown canceling active streams count=%d", len(requestIDs))
	service.persistShutdownInterruptedTerminals(requestIDs)
	var firstErr error
	for _, requestID := range requestIDs {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		stream, ok := service.broker.Get(requestID)
		if !ok || stream == nil {
			continue
		}
		// 单个流取消不能无限占用关闭预算：actor 可能卡在 compile/provider 准备阶段。
		cancelCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		errCh := make(chan error, 1)
		shutdownRequestID := requestID
		shutdownStream := stream
		safego.Go("forwarder:shutdown-cancel", func() {
			errCh <- service.postStreamCommandWait(shutdownStream, streamCommand{
				Kind: streamCommandCancel,
				Intent: InboundIntent{
					Kind:         "cancel",
					RequestID:    shutdownRequestID,
					CancelReason: "[canceled] Local assistant service shutting down",
				},
			})
		})
		var err error
		select {
		case err = <-errCh:
		case <-cancelCtx.Done():
			err = cancelCtx.Err()
		}
		cancel()
		if err != nil && !errors.Is(err, errProviderLoopInterrupted) {
			logger.Errorf("forwarder shutdown cancel failed request_id=%s err=%v", strings.TrimSpace(requestID), err)
			if firstErr == nil {
				firstErr = err
			}
		}
		// 无条件补发终态：不依赖 streamStillActive 判断。actor 的异步 cancel 命令可能
		// 在 1.5s 内把 stream 改成 Canceled 但终态事件尚未被 RunSSE 读走，也可能完全
		// 卡住没发终态。无论哪种情况，这里都必须保证 Cursor 能收到 TurnEnded + canceled
		// endstream，否则前端会一直停在「运行中」等待永远不会到来的响应。
		// 先停掉 provider，避免它继续往已取消的 stream 写入造成竞态。
		forceCancelStreamProvider(stream)
		if err := service.broker.Publish(requestID, StreamEvent{
			Message: buildTurnEndedMessage(0, 0, 0, 0),
		}); err != nil {
			logger.Errorf("forwarder shutdown turn-ended publish failed request_id=%s err=%v", strings.TrimSpace(requestID), err)
			if firstErr == nil {
				firstErr = err
			}
		}
		if cancelErr := service.broker.Cancel(requestID, "[canceled] Local assistant service shutting down"); cancelErr != nil {
			// broker.Cancel 仅在 stream 已不在 broker 中时返回 error（已被 actor 移除），
			// 这种情况下 TurnEnded 已 Publish 到已关闭的订阅也不会被消费——属正常，不记错误。
			if !errors.Is(cancelErr, errStreamNotActive) {
				logger.Errorf("forwarder shutdown force cancel failed request_id=%s err=%v", strings.TrimSpace(requestID), cancelErr)
				if firstErr == nil {
					firstErr = cancelErr
				}
			}
		}
		service.setTurnPhase(stream, TurnPhaseCanceled)
	}

	// 给已连接的 RunSSE 充分时间读走 TurnEnded/endstream，再进入 HTTP Shutdown。
	// RunSSE 从 broker 读事件是异步的，若 drain 太短，Cursor 会在读到终态前被 HTTP
	// 连接关闭打断，表现为「byok 断了但 Cursor 还在转」。这里拉长到 1.5s 并优先等
	// 所有活跃 request 真正退出。
	drainDeadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(drainDeadline) {
		if err := ctx.Err(); err != nil {
			break
		}
		if len(service.broker.ActiveRequestIDs()) == 0 {
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return firstErr
		case <-timer.C:
		}
	}
	return firstErr
}

// persistShutdownInterruptedTerminals 在取消循环之前先把所有活动会话的磁盘状态收口。
//
// 取消循环给每条流 1.5s 的 actor 往返，而调用方给整个 backendHost.Stop 只有 8s（还要
// 包含 drain 与 HTTP Shutdown），最多约 5 条流能走完；剩下的会被 ctx.Err() 直接跳过，
// 永远停在 running/waiting_tool。这一趟只写磁盘：不走 actor、不碰 provider、不发 SSE，
// 因此不占用取消预算。
//
// 幂等取舍：随后被 actor 成功取消的流还会追加 control{status:"canceled"}，
// deriveRequestLoopStatus 顺序覆盖后结果是 canceled——正常取消成功就该是 canceled，
// 语义更准，代价只是多一条冗余条目。反过来「只在取消失败时才补 interrupted」做不到
// 全覆盖：ctx 到期后根本不会走到那些流。已经是终态的会话不会被重复写。
func (service *Service) persistShutdownInterruptedTerminals(requestIDs []string) {
	if service == nil || service.store == nil || service.broker == nil {
		return
	}
	handled := make(map[string]struct{}, len(requestIDs))
	for _, requestID := range requestIDs {
		stream, ok := service.broker.Get(requestID)
		if !ok || stream == nil {
			continue
		}
		stream.mu.Lock()
		conversationID := strings.TrimSpace(stream.ConversationID)
		stream.mu.Unlock()
		if conversationID == "" {
			continue
		}
		if _, done := handled[conversationID]; done {
			continue
		}
		handled[conversationID] = struct{}{}
		_ = service.flushCheckpointPersistSync(stream, conversationID)
		service.forceMarkConversationInterrupted(conversationID, "local assistant service shutting down")
	}
}

func streamStillActive(stream *ActiveStream) bool {
	if stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if isTerminalStreamStatus(stream.Status) {
		return false
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return false
	default:
		return true
	}
}

// forceCancelStreamProvider 直接取消 stream 持有的 provider context，
// 不经过 actor 命令链。用于 shutdown 等必须立即停掉上游调用的场景，
// 防止 provider 在 stream 已发终态后继续写入造成竞态。
func forceCancelStreamProvider(stream *ActiveStream) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	cancel := stream.ProviderCancel
	stream.ProviderCancel = nil
	stream.ProviderActive = false
	stream.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// handleCancelIntent 处理取消请求，并向客户端发送执行桥 abort。

func readStringAny(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func readMapAny(value any) map[string]any {
	switch item := value.(type) {
	case map[string]any:
		return item
	case nil:
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

// inferToolName 从完整 ToolCall proto 中反推出 canonical 工具名。
func inferToolName(toolCall *agentv1.ToolCall) string {
	if toolCall == nil || toolCall.GetTool() == nil {
		return ""
	}
	switch toolCall.GetTool().(type) {
	case *agentv1.ToolCall_ReadToolCall:
		return "Read"
	case *agentv1.ToolCall_UpdateTodosToolCall:
		return "TodoWrite"
	case *agentv1.ToolCall_ReadTodosToolCall:
		return "ReadTodos"
	case *agentv1.ToolCall_DeleteToolCall:
		return "Delete"
	case *agentv1.ToolCall_GrepToolCall:
		return "Grep"
	case *agentv1.ToolCall_GlobToolCall:
		return "Glob"
	case *agentv1.ToolCall_ShellToolCall:
		return "Shell"
	case *agentv1.ToolCall_AwaitToolCall:
		return "AwaitShell"
	case *agentv1.ToolCall_WriteShellStdinToolCall:
		return "WriteShellStdin"
	case *agentv1.ToolCall_EditToolCall:
		return inferEditToolNameFromToolCall(toolCall.GetEditToolCall())
	case *agentv1.ToolCall_LsToolCall:
		return "Ls"
	case *agentv1.ToolCall_McpToolCall:
		return "CallMcpTool"
	case *agentv1.ToolCall_ListMcpResourcesToolCall:
		return "ListMcpResources"
	case *agentv1.ToolCall_ReadMcpResourceToolCall:
		return "FetchMcpResource"
	case *agentv1.ToolCall_CreatePlanToolCall:
		return "CreatePlan"
	case *agentv1.ToolCall_AskQuestionToolCall:
		return "AskQuestion"
	case *agentv1.ToolCall_WebSearchToolCall:
		return "WebSearch"
	case *agentv1.ToolCall_WebFetchToolCall:
		return "WebFetch"
	case *agentv1.ToolCall_SwitchModeToolCall:
		return "SwitchMode"
	case *agentv1.ToolCall_GenerateImageToolCall:
		return "GenerateImage"
	case *agentv1.ToolCall_TaskToolCall:
		return "Task"
	default:
		return ""
	}
}

// deriveToolNameFromPendingExec 根据执行桥种类反推出 canonical 工具名。
func deriveToolNameFromPendingExec(pending runtimecore.PendingExec) string {
	switch strings.TrimSpace(pending.ExecKind) {
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "delete":
		return "Delete"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	case "diagnostics":
		return "ReadLints"
	case "ls":
		return "Ls"
	case "mcp":
		return "CallMcpTool"
	case "list_mcp_resources":
		return "ListMcpResources"
	case "read_mcp_resource":
		return "FetchMcpResource"
	case "shell":
		return "Shell"
	case "await_shell":
		return "AwaitShell"
	case "write_shell_stdin":
		return "WriteShellStdin"
	case "force_background_shell":
		return "ForceBackgroundShell"
	case "subagent":
		return "Task"
	case "delegation_aggregate":
		return "Task"
	case "git_diff":
		return "GitDiff"
	case "mcp_state":
		return "GetMcpTools"
	case "conversation_search":
		return "SearchConversations"
	case "fetch":
		return "Fetch"
	case "record_screen":
		return "RecordScreen"
	case "computer_use":
		return "ComputerUse"
	case "force_background_subagent":
		return "ForceBackgroundSubagent"
	case "subagent_await":
		return "SubagentAwait"
	default:
		return ""
	}
}

func execKindFromToolName(name string) (string, bool) {
	switch strings.TrimSpace(name) {
	case "Read":
		return "read", true
	case "Write":
		return "write", true
	case "PatchEdit":
		return "patch_edit", true
	case "Delete":
		return "delete", true
	case "Glob":
		return "glob", true
	case "Grep":
		return "grep", true
	case "Ls":
		return "ls", true
	case "ReadLints":
		return "diagnostics", true
	case "CallMcpTool":
		return "mcp", true
	case "FetchMcpResource":
		return "read_mcp_resource", true
	case "Shell":
		return "shell", true
	case "AwaitShell":
		return "await_shell", true
	case "WriteShellStdin":
		return "write_shell_stdin", true
	case "ForceBackgroundShell":
		return "force_background_shell", true
	case "Task":
		return "subagent", true
	case "Fetch":
		return "fetch", true
	case "RecordScreen":
		return "record_screen", true
	case "ComputerUse":
		return "computer_use", true
	case "ForceBackgroundSubagent":
		return "force_background_subagent", true
	case "SubagentAwait":
		return "subagent_await", true
	default:
		return "", false
	}
}

func isExecTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "Read", "Write", "PatchEdit", "Delete", "Shell", "WriteShellStdin", "ForceBackgroundShell", "Grep", "Glob", "Ls", "ReadLints", "CallMcpTool", "FetchMcpResource", "Task",
		"Fetch", "GitDiff", "GetMcpTools", "SearchConversations", "RecordScreen", "ComputerUse", "ForceBackgroundSubagent", "SubagentAwait":
		return true
	default:
		return false
	}
}

// isPollingAwaitTool 判断工具是否为轮询型 await 工具。这类工具按设计会以相同参数
// 反复调用（轮询一个仍在运行的子代理 / shell），不应参与 doom-loop 计数，否则会
// 误杀正在等待长任务的正常轮询。
func isPollingAwaitTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "SubagentAwait", "AwaitShell":
		return true
	default:
		return false
	}
}

func inferEditToolName(args *agentv1.EditArgs) string {
	if args != nil && args.StreamContent != nil {
		return "Write"
	}
	return "Edit"
}

func inferEditToolNameFromToolCall(toolCall *agentv1.EditToolCall) string {
	if toolCall == nil {
		return ""
	}
	if editResultLooksLikeStructuredEdit(toolCall.GetResult()) {
		return "Edit"
	}
	return inferEditToolName(toolCall.GetArgs())
}

func editResultLooksLikeStructuredEdit(result *agentv1.EditResult) bool {
	success := result.GetSuccess()
	if success == nil {
		return false
	}
	return success.BeforeFullFileContent != nil || success.DiffString != nil
}

// buildTerminalStreamError 把 broker 终态事件转换成 Connect endstream 错误。
func buildTerminalStreamError(event StreamEvent) error {
	if !event.End {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(event.TerminalErrorCode)) {
	case "":
		return nil
	case "canceled":
		return connect.NewError(connect.CodeCanceled, errors.New(strings.TrimSpace(event.TerminalErrorMessage)))
	case "invalid_argument":
		return connect.NewError(connect.CodeInvalidArgument, errors.New(strings.TrimSpace(event.TerminalErrorMessage)))
	case "failed_precondition":
		return connect.NewError(connect.CodeFailedPrecondition, errors.New(strings.TrimSpace(event.TerminalErrorMessage)))
	case compactionOverflowTerminalCode:
		return buildRunSSECustomError(connect.CodeInvalidArgument, "Context Too Large After Compaction", errors.New(strings.TrimSpace(event.TerminalErrorMessage)))
	case "provider_error":
		return buildRunSSEProviderError(errors.New(strings.TrimSpace(event.TerminalErrorMessage)))
	default:
		return connect.NewError(connect.CodeUnknown, errors.New(strings.TrimSpace(event.TerminalErrorMessage)))
	}
}

// buildRunSSEProviderError 构造 provider 专用的 RunSSE 错误包。
func buildRunSSEProviderError(cause error) error {
	return buildRunSSEStructuredErrorWithDetail(
		connect.CodeUnavailable,
		"Server Error",
		"",
		cause,
		aiserverv1.ErrorDetails_ERROR_PROVIDER_ERROR,
		false,
	)
}

// buildRunSSECustomError 构造带有 CustomErrorDetails 的 RunSSE 结构化错误。
func buildRunSSECustomError(code connect.Code, title string, cause error) error {
	return buildRunSSEStructuredErrorWithDetail(code, title, "", cause, aiserverv1.ErrorDetails_ERROR_CUSTOM_MESSAGE, false)
}

// buildRunSSEStructuredError 统一构造带有 ErrorDetails 的 Connect endstream 错误。
func buildRunSSEStructuredErrorWithDetail(code connect.Code, title string, detailText string, cause error, errorKind aiserverv1.ErrorDetails_Error, expected bool) error {
	if cause == nil {
		cause = fmt.Errorf("unknown RunSSE error")
	}
	trimmedDetail := strings.TrimSpace(detailText)
	if trimmedDetail == "" {
		trimmedDetail = cause.Error()
	}
	// Provider failures are already persisted and published as a terminal
	// stream event. Marking only those details retryable makes Cursor wrap the
	// response in RetriableError and replay the same request, which presents as
	// a disconnected stream and can duplicate provider work. Preserve the
	// existing retry metadata for non-provider RunSSE errors.
	isRetryable := errorKind != aiserverv1.ErrorDetails_ERROR_PROVIDER_ERROR
	allowUnsafeCommandLinks := true
	showRequestID := true
	shouldShowImmediateError := false
	isExpected := expected
	payload := &aiserverv1.ErrorDetails{
		Error: errorKind,
		Details: &aiserverv1.CustomErrorDetails{
			Title:       strings.TrimSpace(title),
			Detail:      trimmedDetail,
			IsRetryable: &isRetryable,
			AllowCommandLinksPotentiallyUnsafePleaseOnlyUseForHandwrittenTrustedMarkdown: &allowUnsafeCommandLinks,
			ShowRequestId:            &showRequestID,
			ShouldShowImmediateError: &shouldShowImmediateError,
		},
		IsExpected: &isExpected,
	}
	result := connect.NewError(code, cause)
	detail, detailErr := connect.NewErrorDetail(payload)
	if detailErr == nil {
		result.AddDetail(detail)
	}
	return result
}
