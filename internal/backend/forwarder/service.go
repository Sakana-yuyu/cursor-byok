// service.go 实现 forwarder 的主链路：Bidi 上行归一化、history 写入、provider 驱动和 RunSSE 下行。
package forwarder

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
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
	"cursor/internal/modelcontext"
	"cursor/internal/promptinject"
)

const (
	providerResumeDebounce         = 200 * time.Millisecond
	completedExecRetention         = 15 * time.Second
	nonStreamingExecCloseGrace     = 1500 * time.Millisecond
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
	store                    *ConversationFileStore
	usageStore               *UsageFileStore
	codebaseIndexStore       *CodebaseIndexStore
	docsIndexStore           *DocsIndexStore
	rules                    *UserRuleStore
	projector                *HistoryProjector
	compiler                 PromptCompiler
	toolCatalog              *DefaultToolCatalog
	promptInjection          *promptinject.Manager
	provider                 ProviderGateway
	resolver                 modeladapter.ChannelResolver
	modelMemory              agentModelMemory
	maxTokensPersister       maxTokensConfigPersister
	contextWindowPersister   contextWindowPersister
	scanConfig               skillMCPScanConfigProvider
	mcpRuntime               *MCPRuntimeRegistry
	broker                   *StreamBroker
	recorder                 *artifactRecorder
	debug                    *debugRecorder
	execBridge               execbridge.ExecBridge
	interactionBridge        interactionbridge.InteractionBridge
	appendSeq                *appendSequenceTracker
	runQueue                 *runQueue
	cursorDelegation         *cursorDelegationBridge
	localDelegation          *localDelegatedAgentAdapter
	delegationConfig         delegation.RuntimeConfigProvider
	goalConfig               goalConfigProvider
	usageCostEstimator       goalUsageCostEstimator
	goalsMu                  sync.RWMutex
	goals                    map[string]*GoalState // conversationID → goal 状态，保留最近 100 条
	multitaskDelegation      *multitaskDelegationCoordinator
	delegationRuntimeMu      sync.Mutex
	nativeDelegations        map[string]*nativeDelegationRuntime
	visionRunsMu             sync.Mutex
	visionRuns               map[string]*visionDelegationRun
	visionCacheMu            sync.Mutex
	visionCache              map[string]visionCacheEntry
	visionImageMu            sync.Mutex
	visionImageFiles         map[string][]string
	// visionArchiveMu 保护 visionArchive：会话级图片识图结果归档。
	// key = conversationID#imageHash，命中后直接用归档文本替换图片 part，
	// 不再重复调识图模型，也避免历史图片反复进入 provider 上下文。
	// 归档同时落盘到 history/<conversationID>/vision-archive.json，
	// 进程重启后（同会话、同图片内容）仍可命中；visionArchiveLoaded 记录
	// 已懒加载过的会话，避免重复读盘。
	visionArchiveMu      sync.Mutex
	visionArchive        map[string]visionArchiveEntry
	visionArchiveLimit   int
	visionArchiveLoaded  map[string]struct{}
	provider400RecoveryMu    sync.Mutex
	provider400RecoveryTurns map[string]struct{}
	// conversationActivityMu 保护 conversationLastActivity：
	// 记录每个 conversation 最近一次模型输出/思考/工具活动，供 native 子代理
	// 无进展看门狗判断「子代理是否仍在工作」，避免长文本生成/长思考被误判超时。
	conversationActivityMu   sync.Mutex
	conversationLastActivity map[string]time.Time
	checkpointBlobMu         sync.Mutex
	checkpointBlobs          map[string]*checkpointBlobCacheEntry
}

type agentModelMemory interface {
	LastAgentModelHash() string
	SaveLastAgentModelHash(context.Context, string) error
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

// NewService 使用默认依赖创建 forwarder 服务。
func NewService(historyRoot string, resolver modeladapter.ChannelResolver) *Service {
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
	var goalCfg goalConfigProvider
	if candidate, ok := resolver.(goalConfigProvider); ok {
		goalCfg = candidate
	}
	debug := newDebugRecorder(historyRoot, broker, debugConfig)
	service := &Service{
		store:                    store,
		usageStore:               NewUsageFileStore(historyRoot),
		codebaseIndexStore:       NewCodebaseIndexStore(appdata.CodebaseIndexRootPath()),
		docsIndexStore:           NewDocsIndexStore(appdata.DocsIndexRootPath()),
		rules:                    rules,
		projector:                projector,
		compiler:                 NewPromptCompiler(projector, toolCatalog, NewReminderInjector(), rules, skills, promptInjection),
		toolCatalog:              toolCatalog,
		promptInjection:          promptInjection,
		provider:                 NewProviderGateway(resolver),
		resolver:                 resolver,
		modelMemory:              modelMemory,
		maxTokensPersister:       maxTokensPersister,
		contextWindowPersister:   ctxWindowPersister,
		scanConfig:               scanConfig,
		delegationConfig:         delegationConfig,
		goalConfig:               goalCfg,
		usageCostEstimator:       &defaultUsageCostEstimator{lookup: newPricingLookupFromConfig(resolver)},
		goals:                    make(map[string]*GoalState),
		mcpRuntime:               SharedMCPRuntimeRegistry(),
		broker:                   broker,
		recorder:                 newArtifactRecorder(store, broker, debug),
		debug:                    debug,
		execBridge:               execbridge.NewBridge(),
		interactionBridge:        interactionbridge.NewBridge(),
		appendSeq:                newAppendSequenceTracker(),
		runQueue:                 newRunQueue(),
		nativeDelegations:        make(map[string]*nativeDelegationRuntime),
		// 视觉委派相关的三个 map 必须在构造时初始化：
		// 缺失会导致 beginVisionRun 向 nil map 写入，触发
		// "assignment to entry in nil map" panic，杀死整个 Wails 主进程
		// （视觉委派一触发就闪退的根因）。
		visionRuns:               make(map[string]*visionDelegationRun),
		visionCache:              make(map[string]visionCacheEntry),
		visionImageFiles:         make(map[string][]string),
		visionArchive:            make(map[string]visionArchiveEntry),
		visionArchiveLimit:       visionArchiveMaxEntries,
		checkpointBlobs:          make(map[string]*checkpointBlobCacheEntry),
		conversationLastActivity: make(map[string]time.Time),
	}
	service.cursorDelegation = newCursorDelegationBridge(service)
	service.localDelegation = newLocalDelegatedAgentAdapter(service)
	service.multitaskDelegation = newMultitaskDelegationCoordinator(service, delegationConfig)
	service.startHistoryMaintenance()
	store.SyncAllCursorTranscriptsBestEffort()
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
		visionImageFiles:         make(map[string][]string),
		visionArchive:            make(map[string]visionArchiveEntry),
		visionArchiveLimit:       visionArchiveMaxEntries,
		checkpointBlobs:          make(map[string]*checkpointBlobCacheEntry),
		conversationLastActivity: make(map[string]time.Time),
	}
	service.cursorDelegation = newCursorDelegationBridge(service)
	service.localDelegation = newLocalDelegatedAgentAdapter(service)
	service.multitaskDelegation = newMultitaskDelegationCoordinator(service, nil)
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
		log.Printf("forwarder ignored stale bidi append request_id=%s append_seqno=%d", requestID, appendSeqno)
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
					service.debug.LogRunSSE(ctx, requestID, "", "send_message", map[string]any{
						"cursor":       cursor,
						"message_case": agentServerMessageCase(event.Message),
						"message_size": proto.Size(event.Message),
						"message":      protoJSONDebugPayload(event.Message),
					})
				}
				cursor++
				if event.End {
					service.debug.LogRunSSE(ctx, requestID, "", "terminal", map[string]any{
						"cursor":                 cursor,
						"terminal_error_code":    strings.TrimSpace(event.TerminalErrorCode),
						"terminal_error_message": strings.TrimSpace(event.TerminalErrorMessage),
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
func (service *Service) decodeInboundIntent(requestID string, message *agentv1.AgentClientMessage, clientKind string) (InboundIntent, error) {
	intent := InboundIntent{
		RequestID:     strings.TrimSpace(requestID),
		ClientMessage: message,
	}
	var err error
	switch strings.TrimSpace(clientKind) {
	case "run_request":
		runRequest := message.GetRunRequest()
		if runRequest == nil {
			return InboundIntent{}, fmt.Errorf("run_request payload is required")
		}
		conversationID := strings.TrimSpace(runRequest.GetConversationId())
		if conversationID == "" {
			return InboundIntent{}, fmt.Errorf("conversation_id is required in run_request")
		}
		intent.ConversationID = conversationID
		intent.ConversationState = runRequest.GetConversationState()
		intent.PreFetchedBlobs = runRequest.GetPreFetchedBlobs()
		intent.UserMessage = extractUserMessage(message)
		// Cursor 粘贴图片走 blob 协议：图片数据在 pre_fetched_blobs 里，selected_images 只带 blob_id。
		// 这里把 blob 数据填充进图片，否则后续 buildSelectedImageContentParts 会静默丢弃图片，
		// 图片进不了消息 ContentPart，图片路径占位也不会触发。
		hydrateSelectedImageBlobs(intent.UserMessage, buildPrefetchedBlobMap(runRequest.GetPreFetchedBlobs()))
		actionRequestContext := extractRequestContext(message)
		intent.RequestContext = extractEffectiveRunRequestContext(message)
		intent.MCPToolsProvided = runRequest.McpTools != nil || len(actionRequestContext.GetTools()) > 0
		if service.shouldIgnoreEmptyResumeRunRequest(requestID, runRequest, intent.UserMessage, actionRequestContext) {
			intent.Kind = "metadata"
			intent.StartsRun = false
			intent.HasExplicitMode = false
			intent.ModeSource = ModeSourceUnknown
			intent.IgnoredReason = "empty_resume_without_pending_continuation"
			return intent, nil
		}
		intent.Kind = "run"
		intent.StartsRun = true
		intent.Mode, intent.ModeSource, intent.HasExplicitMode, err = extractRunMode(message)
		if err != nil {
			return InboundIntent{}, err
		}
		intent.ModelID = extractRequestedModelID(message)
		intent.ThinkingEffort = extractRuntimeThinkingEffort(message)
		intent.CustomSystemPrompt = truncatePromptGuardText("run_request.custom_system_prompt", runRequest.GetCustomSystemPrompt(), promptGuardCustomSystemPromptChars)
		intent.MaxMode = extractRequestedMaxMode(message)
		intent.SubagentTypeName = strings.TrimSpace(runRequest.GetSubagentTypeName())
		intent.SelectedSubagentModels = cloneSelectedSubagentModels(runRequest.GetSelectedSubagentModels())
		intent.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(runRequest.GetSelectedSubagentModelDetails())
		parsedOverrides := parseSubagentModelOverrides(runRequest.GetSubagentModelOverrides())
		intent.SubagentModelOverrides = parsedOverrides.Overrides
		service.debug.LogRuntime(context.Background(), intent.RequestID, intent.ConversationID, "subagent_model_overrides_parsed", map[string]any{
			"override_count": parsedOverrides.RawCount,
			"valid_count":    len(parsedOverrides.Overrides),
			"ignored_count":  len(parsedOverrides.Ignored),
			"overrides":      subagentModelOverrideSummaries(parsedOverrides.Overrides),
			"ignored":        parsedOverrides.Ignored,
		})
		if intent.ModelID == "" {
			intent.ModelID = "default"
		}
		intent.ModelName = service.resolveRequestedModelName(message, intent.ModelID)
	case "prewarm_request":
		prewarmRequest := message.GetPrewarmRequest()
		if prewarmRequest == nil {
			return InboundIntent{}, fmt.Errorf("prewarm_request payload is required")
		}
		conversationID := strings.TrimSpace(prewarmRequest.GetConversationId())
		if conversationID == "" {
			return InboundIntent{}, fmt.Errorf("conversation_id is required in prewarm_request")
		}
		intent.Kind = "run"
		intent.Prewarm = true
		intent.StartsRun = true
		intent.ConversationID = conversationID
		intent.SubagentTypeName = strings.TrimSpace(prewarmRequest.GetSubagentTypeName())
		intent.SelectedSubagentModels = cloneSelectedSubagentModels(prewarmRequest.GetSelectedSubagentModels())
		intent.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(prewarmRequest.GetSelectedSubagentModelDetails())
		parsedOverrides := parseSubagentModelOverrides(prewarmRequest.GetSubagentModelOverrides())
		intent.SubagentModelOverrides = parsedOverrides.Overrides
		intent.RequestContext = extractEffectivePrewarmRequestContext(prewarmRequest)
		intent.MCPToolsProvided = prewarmRequest.McpTools != nil
		intent.ConversationState = prewarmRequest.GetConversationState()
		intent.PreFetchedBlobs = prewarmRequest.GetPreFetchedBlobs()
		intent.Mode, intent.ModeSource, intent.HasExplicitMode, err = extractPrewarmMode(prewarmRequest)
		if err != nil {
			return InboundIntent{}, err
		}
		intent.ModelID = firstNonEmpty(extractRequestedModelID(message), "default")
		intent.ThinkingEffort = extractRuntimeThinkingEffort(message)
		intent.MaxMode = extractRequestedMaxMode(message)
		intent.ModelName = service.resolveRequestedModelName(message, intent.ModelID)
	case "conversation_action":
		action := message.GetConversationAction()
		if action == nil {
			return InboundIntent{}, fmt.Errorf("conversation_action payload is required")
		}
		intent.UserMessage = extractConversationActionUserMessage(action)
		intent.RequestContext = extractConversationActionRequestContext(action)
		intent.StartsRun = conversationActionStartsRun(action)
		intent.ForceNewTurn = intent.StartsRun
		intent.Mode, intent.ModeSource, intent.HasExplicitMode, err = extractConversationActionMode(action)
		if err != nil {
			return InboundIntent{}, err
		}
		switch item := action.GetAction().(type) {
		case *agentv1.ConversationAction_CancelAction:
			intent.Kind = "cancel"
			intent.CancelReason = strings.TrimSpace(item.CancelAction.GetReason())
		default:
			if intent.StartsRun || intent.HasExplicitMode {
				if stream, ok := service.broker.Get(intent.RequestID); ok && stream != nil {
					stream.mu.Lock()
					intent.ConversationID = strings.TrimSpace(stream.ConversationID)
					intent.ModelID = strings.TrimSpace(stream.ModelID)
					intent.ModelName = strings.TrimSpace(stream.ModelName)
					intent.ThinkingEffort = strings.TrimSpace(stream.ThinkingEffort)
					intent.MaxMode = stream.MaxMode
					intent.SubagentModelOverrides = cloneSubagentModelOverrides(stream.SubagentModelOverrides)
					intent.SelectedSubagentModels = cloneSelectedSubagentModels(stream.SelectedSubagentModels)
					intent.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(stream.SelectedSubagentModelDetails)
					if !intent.HasExplicitMode && stream.Mode != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
						intent.Mode = stream.Mode
					}
					if stream.CheckpointConversation != nil {
						intent.SubagentTypeName = strings.TrimSpace(stream.CheckpointConversation.SubagentTypeName)
					}
					stream.mu.Unlock()
				}
				if strings.TrimSpace(intent.ConversationID) == "" {
					return InboundIntent{}, fmt.Errorf("conversation_action requires active request context")
				}
			}
			if intent.StartsRun {
				intent.Kind = "run"
				intent.StartsRun = true
				if intent.ModelID == "" {
					intent.ModelID = "default"
				}
			} else {
				intent.Kind = "metadata"
			}
		}
	case "exec_client_message":
		intent.Kind = "exec_result"
		intent.ExecClientMessage = message.GetExecClientMessage()
	case "exec_client_control_message":
		intent.Kind = "exec_control"
		intent.ExecClientControlMessage = message.GetExecClientControlMessage()
	case "interaction_response":
		intent.Kind = "interaction_result"
		intent.InteractionResponse = message.GetInteractionResponse()
	case "kv_client_message":
		intent.Kind = "kv_result"
		intent.KVClientMessage = message.GetKvClientMessage()
	case "client_heartbeat":
		intent.Kind = "metadata"
	default:
		return InboundIntent{}, fmt.Errorf("unsupported client message kind: %s", clientKind)
	}
	intent.ManualCompaction = resolveInboundManualCompaction(message, intent.UserMessage)
	return intent, nil
}

func (service *Service) shouldReuseActiveRun(intent InboundIntent) bool {
	if intent.ForceNewTurn {
		return false
	}
	if service == nil || service.broker == nil || strings.TrimSpace(intent.RequestID) == "" || strings.TrimSpace(intent.ConversationID) == "" {
		return false
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if strings.TrimSpace(stream.ConversationID) != strings.TrimSpace(intent.ConversationID) {
		return false
	}
	if isTerminalStreamStatus(stream.Status) {
		return false
	}
	switch stream.Phase {
	case TurnPhaseCanceled, TurnPhaseCompleted, TurnPhaseFailed:
		return false
	}
	// 用户切换模型后发送新 run_request 时，不应复用旧 stream（旧模型）。
	if requestedModel := strings.TrimSpace(intent.ModelID); requestedModel != "" {
		if currentModel := strings.TrimSpace(stream.ModelID); currentModel != "" && requestedModel != currentModel {
			return false
		}
	}
	// RunSSE 重连会重复提交同一个 run_request；只要该 request 仍处于活动回合，
	// 就不能重新初始化 checkpoint、pending exec 或 provider pass。
	return stream.TurnSeq > 0 || stream.ProviderActive || len(stream.PendingExecs) > 0 || len(stream.PendingInteractions) > 0
}

// handleRunIntent 处理 run/prewarm 类 intent，负责建会话、写 turn 和拉起 provider。
func (service *Service) handleRunIntent(intent InboundIntent) error {
	if err := service.prepareStreamForForcedTurn(intent); err != nil {
		return err
	}
	if !intent.GoalMode {
		if goalText, strict, isGoal := parseGoalCommand(userMessageText(intent.UserMessage)); isGoal {
			intent.GoalMode = true
			intent.GoalText = goalText
			intent.GoalStrict = strict
			// 剥离前缀，避免 goal 目标文本被当作指令重复注入。
			intent.UserMessage = replaceUserMessageText(intent.UserMessage, goalText)
		}
	}
	if service.shouldReuseActiveRun(intent) {
		log.Printf("forwarder duplicate run reused request_id=%s conversation_id=%s", strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID))
		return nil
	}
	intent.UserMessage = normalizeUserMessageForStorage(intent.UserMessage)
	// 在写历史前，把磁盘扫描到的技能/MCP server 合并进 RequestContext。
	// 仅 turn 1 需要持久化静态上下文（与 normalizeRequestContextForStorageMode 的 turnSeq==1 语义一致），
	// 复用现有 request_context → projector → engine.go 的原生 user-message 注入链路。
	service.enrichRequestContextWithScannedAssets(&intent)
	if !intent.Prewarm {
		service.cancelOtherConversationActors(
			intent.ConversationID,
			intent.RequestID,
			"[canceled] Superseded by newer request",
		)
	}
	conversation, effectiveMode, turnSeq, initialEntries, err := service.bootstrapRuntimeConversation(intent)
	if err != nil {
		return err
	}
	if intent.RequestContext != nil {
		if folder := normalizeAgentTranscriptsFolder(intent.RequestContext.GetEnv().GetAgentTranscriptsFolder()); folder != "" {
			conversation.AgentTranscriptsFolder = folder
		}
	}
	rewindDecision := service.decideRunRewind(intent, conversation)
	if rewindDecision.Evaluated && !rewindDecision.Apply {
		service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_skipped", rewindDecision)
	}
	if rewindDecision.Apply {
		service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_detected", rewindDecision)
		turnSeq = rewindDecision.TargetTurnSeq
		initialEntries, err = buildRunEntries(intent, effectiveMode, turnSeq)
		if err != nil {
			return err
		}
	}
	if service.store != nil {
		if rewindDecision.Apply {
			persisted, err := service.store.ReplaceEntries(
				intent.ConversationID,
				appendReplacementRunEntries(rewindDecision.PrefixEntries, initialEntries),
				func(item *ConversationFile) error {
					applyRunRewindMetadata(item, conversation, intent, turnSeq)
					return nil
				},
			)
			if err != nil {
				return err
			}
			if persisted != nil {
				conversation = persisted
			}
			service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_applied", rewindDecision)
		} else {
			persisted, err := service.store.SaveConversationWithEntries(intent.ConversationID, conversation, initialEntries)
			if err != nil {
				return err
			}
			if persisted != nil {
				conversation = persisted
			}
		}
	} else if rewindDecision.Apply {
		service.applyRunRewindToConversation(conversation, rewindDecision, initialEntries, intent, turnSeq)
		service.logRunRewindDecision(intent.RequestID, intent.ConversationID, "rewind_applied", rewindDecision)
	} else if len(initialEntries) > 0 {
		appendEntriesInPlace(conversation, initialEntries)
		deriveConversationLoopState(conversation)
	}

	stream, err := service.broker.OpenStream(intent.RequestID, intent.ConversationID, turnSeq, intent.ModelID, intent.ModelName, effectiveMode, userMessageText(intent.UserMessage))
	if err != nil {
		return err
	}
	if stream == nil {
		return fmt.Errorf("open stream failed")
	}
	if intent.GoalMode {
		goalState := newGoalState(intent.ConversationID, intent.GoalText, intent.GoalStrict)
		stream.Goal = goalState
		service.registerGoal(intent.ConversationID, goalState)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), intent.RequestID, intent.ConversationID, "goal_started", map[string]any{
				"goal_text": intent.GoalText,
			})
		}
	}
	if err := service.replaceCheckpointConversation(stream, conversation); err != nil {
		return err
	}
	updateStreamRequestContextData(stream, intent.RequestContext)
	service.updateStreamMCPToolServers(stream, intent.RequestContext)
	clearPendingProviderCompletion(stream)
	stream.mu.Lock()
	stream.ThinkingEffort = strings.TrimSpace(intent.ThinkingEffort)
	stream.CustomSystemPrompt = strings.TrimSpace(intent.CustomSystemPrompt)
	stream.MaxMode = intent.MaxMode
	stream.SubagentModelOverrides = cloneSubagentModelOverrides(intent.SubagentModelOverrides)
	stream.SelectedSubagentModels = cloneSelectedSubagentModels(intent.SelectedSubagentModels)
	stream.SelectedSubagentModelDetails = cloneSelectedSubagentModelDetails(intent.SelectedSubagentModelDetails)
	stream.ManualCompaction = intent.ManualCompaction
	stream.PendingProviderAction = providerActionNone
	stream.PendingCompaction = nil
	stream.PendingExecs = make(map[string]runtimecore.PendingExec)
	stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	stream.RecentCompletedExecs = make(map[uint32]time.Time)
	stream.RecentCompletedInteractions = make(map[string]time.Time)
	stream.BackgroundShells = make(map[string]*BackgroundShellState)
	stream.BackgroundShellsByMessageID = make(map[uint32]string)
	stream.BackgroundShellsByExecID = make(map[string]string)
	stopAllStreamTimersLocked(stream)
	if intent.ForceNewTurn {
		if stream.TimerTokens == nil {
			stream.TimerTokens = make(map[string]uint64)
		}
		if stream.StreamTimers == nil {
			stream.StreamTimers = make(map[string]*time.Timer)
		}
	} else {
		stream.TimerTokens = make(map[string]uint64)
		stream.StreamTimers = make(map[string]*time.Timer)
		stream.CurrentProviderToken = 0
		stream.CurrentCompactionToken = 0
	}
	stream.ProviderAccumulatedText = ""
	stream.ProviderAccumulatedReasoning = ""
	stream.ProviderAccumulatedReasoningSignature = ""
	stream.ProviderAccumulatedReasoningSignatureSource = ""
	stream.ProviderAccumulatedReasoningItemID = ""
	stream.ProviderAccumulatedReasoningStatus = ""
	stream.ProviderAccumulatedReasoningSummary = nil
	stream.ProviderSyntheticThinkingStartedAt = time.Time{}
	stream.ProviderSyntheticThinkingPublished = false
	stream.ProviderThinkingDeltaCount = 0
	stream.ProviderThinkingCompletedCount = 0
	stream.ProviderThinkingSuppressedCount = 0
	stream.ProviderFinishReason = ""
	stream.ProviderUsage = turnUsageSnapshot{}
	stream.ToolInvocationCount = 0
	stream.AutoMultitaskDelegationStarted = false
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	service.setTurnPhase(stream, TurnPhaseIdle)
	service.debug.LogRuntime(context.Background(), intent.RequestID, intent.ConversationID, "stream_state_updated", map[string]any{
		"turn_seq":                             turnSeq,
		"model_id":                             strings.TrimSpace(intent.ModelID),
		"model_name":                           strings.TrimSpace(intent.ModelName),
		"thinking_effort":                      strings.TrimSpace(intent.ThinkingEffort),
		"mode":                                 effectiveMode.String(),
		"prewarm":                              intent.Prewarm,
		"subagent_type":                        strings.TrimSpace(intent.SubagentTypeName),
		"subagent_model_override_count":        len(intent.SubagentModelOverrides),
		"subagent_model_overrides":             subagentModelOverrideSummaries(intent.SubagentModelOverrides),
		"selected_subagent_model_count":        len(intent.SelectedSubagentModels),
		"selected_subagent_model_detail_count": len(intent.SelectedSubagentModelDetails),
		"latest_user_text":                     userMessageText(intent.UserMessage),
		"manual_compaction_requested":          intent.ManualCompaction.Requested,
	})
	if err := service.publishCheckpointForce(intent.RequestID, intent.ConversationID); err != nil {
		return err
	}
	if intent.Prewarm {
		return nil
	}
	return service.requestProviderAction(stream, providerActionStart)
}

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
	log.Printf("forwarder shutdown canceling active streams count=%d", len(requestIDs))
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
		go func(requestID string, stream *ActiveStream) {
			errCh <- service.postStreamCommandWait(stream, streamCommand{
				Kind: streamCommandCancel,
				Intent: InboundIntent{
					Kind:         "cancel",
					RequestID:    requestID,
					CancelReason: "[canceled] Local assistant service shutting down",
				},
			})
		}(requestID, stream)
		var err error
		select {
		case err = <-errCh:
		case <-cancelCtx.Done():
			err = cancelCtx.Err()
		}
		cancel()
		if err != nil && !errors.Is(err, errProviderLoopInterrupted) {
			log.Printf("forwarder shutdown cancel failed request_id=%s err=%v", strings.TrimSpace(requestID), err)
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
		_ = service.broker.Publish(requestID, StreamEvent{
			Message: buildTurnEndedMessage(0, 0, 0, 0),
		})
		if cancelErr := service.broker.Cancel(requestID, "[canceled] Local assistant service shutting down"); cancelErr != nil {
			// broker.Cancel 仅在 stream 已不在 broker 中时返回 error（已被 actor 移除），
			// 这种情况下 TurnEnded 已 Publish 到已关闭的订阅也不会被消费——属正常，不记错误。
			if !errors.Is(cancelErr, errStreamNotActive) {
				log.Printf("forwarder shutdown force cancel failed request_id=%s err=%v", strings.TrimSpace(requestID), cancelErr)
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
func (service *Service) handleCancelIntent(intent InboundIntent) error {
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", intent.RequestID)
	}
	stream.mu.Lock()
	turnSeq := stream.TurnSeq
	conversationID := strings.TrimSpace(stream.ConversationID)
	phase := stream.Phase
	status := stream.Status
	providerActive := stream.ProviderActive
	pendingExecCount := len(stream.PendingExecs)
	activeDelegationCount := 0
	for _, pending := range stream.PendingExecs {
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			activeDelegationCount++
		}
	}
	stream.mu.Unlock()
	log.Printf("forwarder cancel intent received request_id=%s conversation_id=%s reason=%q phase=%s status=%s provider_active=%t pending_execs=%d active_delegations=%d",
		strings.TrimSpace(intent.RequestID), conversationID, strings.TrimSpace(intent.CancelReason), phase, status, providerActive, pendingExecCount, activeDelegationCount)
	service.debug.LogRuntime(context.Background(), intent.RequestID, conversationID, "cancel_intent_received", map[string]any{
		"reason":                     strings.TrimSpace(intent.CancelReason),
		"phase":                      string(phase),
		"status":                     string(status),
		"provider_active":            providerActive,
		"pending_exec_count":         pendingExecCount,
		"active_delegation_count":    activeDelegationCount,
		"client_requested_cancel":    true,
		"cancel_replay_policy_value": cancelReplayPolicyForReason(intent.CancelReason),
	})
	service.clearProvider400Recovery(intent.RequestID, turnSeq)
	// 先切断当前 provider 请求，再做 history、工具 abort 和委派清理。
	// 断线取消不能因为后续持久化或广播变慢而继续消耗上游额度。
	forceCancelStreamProvider(stream)
	if service.multitaskDelegation != nil {
		service.multitaskDelegation.CancelStream(stream)
	}
	hasCheckpoint := checkpointConversationInitialized(stream)
	stream.mu.Lock()
	pendingExecs := make([]runtimecore.PendingExec, 0, len(stream.PendingExecs))
	for _, pending := range stream.PendingExecs {
		pendingExecs = append(pendingExecs, pending)
	}
	if stream.ProviderCancel != nil {
		stream.ProviderCancel()
		stream.ProviderCancel = nil
	}
	stream.ProviderActive = false
	stream.CurrentProviderToken++
	stream.CurrentCompactionToken++
	stream.PendingProviderAction = providerActionNone
	stream.PendingCompaction = nil
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if hasCheckpoint {
		cancelReason := firstNonEmpty(intent.CancelReason, "user aborted")
		cancelEntry := newMetadataEntry(stream.TurnSeq, intent.RequestID, "control", map[string]any{
			"status":        "canceled",
			"reason":        cancelReason,
			"replay_policy": cancelReplayPolicyForReason(cancelReason),
		})
		if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{cancelEntry}); err != nil {
			log.Printf("forwarder cancellation metadata persistence failed request_id=%s conversation_id=%s err=%v", stream.RequestID, stream.ConversationID, err)
			if memoryErr := service.appendCheckpointEntries(stream, []HistoryEntry{cancelEntry}); memoryErr != nil {
				return memoryErr
			}
		}
	}
	for _, pending := range pendingExecs {
		if strings.TrimSpace(pending.ExecKind) == "subagent" {
			service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskCanceled, "Cursor 子代理已取消", "subagent canceled")
		}
		if strings.TrimSpace(pending.ExecKind) == "delegation_aggregate" {
			continue
		}
		_ = service.broker.Publish(intent.RequestID, StreamEvent{
			Message: buildExecAbortMessage(pending),
		})
	}
	// 清除所有 pending exec，防止 stream 永远卡在 running
	cleanupAllPendingExecs(stream)
	clearPendingProviderCompletion(stream)
	stream.mu.Lock()
	stream.PendingExecs = make(map[string]runtimecore.PendingExec)
	stream.PendingInteractions = make(map[string]runtimecore.PendingInteraction)
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if hasCheckpoint {
		if err := service.publishCheckpointWithTerminalAction(
			stream.RequestID,
			stream.ConversationID,
			checkpointCancellationAction(firstNonEmpty(intent.CancelReason, "[canceled] User aborted request")),
		); err != nil {
			return err
		}
		return nil
	}
	service.setTurnPhase(stream, TurnPhaseCanceled)
	// 发送 TurnEndedUpdate 让前端退出活跃状态（否则一直显示 "planning next moves"）
	_ = service.broker.Publish(intent.RequestID, StreamEvent{
		Message: buildTurnEndedMessage(0, 0, 0, 0),
	})
	cancelErr := service.broker.Cancel(intent.RequestID, firstNonEmpty(intent.CancelReason, "[canceled] User aborted request"))
	// 当前 turn 终态后，排空该会话因「子代理运行期间」排队的新消息。
	service.drainRunQueue(stream.ConversationID)
	return cancelErr
}

// handleExecResult 处理客户端返回的执行桥结果，并在终态时把 tool_result 写回 history。
func (service *Service) handleExecResult(intent InboundIntent) error {
	// CursorAdapter only owns child worker execs registered in its waiter table.
	// The parent aggregate remains owned by this stream and is closed later by
	// streamDelegationResult -> handleDelegationResult.
	if intent.ExecClientMessage != nil && service.cursorDelegation != nil && service.cursorDelegation.ConsumeExecMessage(intent.RequestID, intent.ExecClientMessage) {
		return nil
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", intent.RequestID)
	}
	if intent.ExecClientMessage == nil {
		return fmt.Errorf("exec client message is required")
	}
	pending, found := selectPendingExec(intent.ExecClientMessage.GetExecId(), intent.ExecClientMessage.GetId(), stream)
	if !found {
		if service.observeMissingBackgroundShellExecClientMessage(stream, intent.ExecClientMessage) {
			return nil
		}
		if service.observeMissingShellExecClientMessage(stream, intent.ExecClientMessage) {
			return nil
		}
		if shouldIgnoreMissingExecResult(intent.ExecClientMessage, stream) {
			return nil
		}
		return fmt.Errorf("pending exec not found")
	}
	if execID := strings.TrimSpace(intent.ExecClientMessage.GetExecId()); execID != "" &&
		intent.ExecClientMessage.GetId() != 0 && pending.MessageID != 0 &&
		intent.ExecClientMessage.GetId() != pending.MessageID && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "exec_identity_mismatch_accepted", map[string]any{
			"exec_id":             execID,
			"expected_message_id": pending.MessageID,
			"received_message_id": intent.ExecClientMessage.GetId(),
			"parent_request_id":   stream.RequestID,
			"exec_kind":           strings.TrimSpace(pending.ExecKind),
			"provider_pass":       pending.ProviderPass,
		})
	}
	if service.ignoreStaleExecProviderPass(stream, pending, "exec_client_message") {
		return nil
	}
	service.observeBackgroundShellExecClientMessage(stream, pending, intent.ExecClientMessage)
	service.observeShellExecClientMessage(stream, pending, intent.ExecClientMessage)
	service.markConversationActivity(stream.ConversationID)
	pending = service.applyExecProgress(stream, pending, intent.ExecClientMessage)
	if isHiddenPatchEditExecKind(pending.ExecKind) {
		return service.handleHiddenPatchEditExecResult(stream, pending, intent.ExecClientMessage)
	}
	if isHiddenWriteExecKind(pending.ExecKind) {
		return service.handleHiddenWriteExecResult(stream, pending, intent.ExecClientMessage)
	}
	result, err := service.execBridge.ApplyExecClientMessage(intent.ExecClientMessage, pending)
	if err != nil {
		if strings.TrimSpace(pending.ExecKind) == "subagent" {
			service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskFailed, "Cursor 子代理执行失败", err.Error())
		}
		return err
	}
	// 捕获点：MCP 执行结果（含失败模式），便于后续读取日志针对性修复执行层问题。
	// 对应已知限制：MCP 执行仍依赖 Cursor 客户端；server_not_found/tool_not_found/超时等需要日志证据。
	if strings.TrimSpace(pending.ExecKind) == "mcp" && result.IsTerminal {
		service.captureMCPExecResult(intent, pending, execTerminalResult{
			ToolCallID:        result.ToolCallID,
			ToolResultPayload: result.ToolResultPayload,
		})
	}
	if result.ShellOutputDelta != nil {
		if err := service.broker.Publish(intent.RequestID, StreamEvent{
			Message: buildShellOutputDeltaMessage(result.ShellOutputDelta),
		}); err != nil {
			return err
		}
	}
	if !result.IsTerminal {
		if len(result.HookAdditionalContexts) > 0 {
			if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
				newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_hook_additional_context", map[string]any{
					"tool_call_id": strings.TrimSpace(pending.ToolCallID),
					"exec_id":      strings.TrimSpace(pending.ExecID),
					"contexts":     hookAdditionalContextsToRecords(result.HookAdditionalContexts),
				}),
			}); err != nil {
				log.Printf("forwarder shell hook context metadata failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), err)
			}
		}
		// 只有真实执行数据才算有效进展；摘要/heartbeat 不能延长 watchdog。
		if execMessageHasEffectiveProgress(intent.ExecClientMessage, result) {
			service.markNativeDelegationEffectiveProgress(pending.ExecID, "Cursor 子代理正在处理工具结果")
			service.rescheduleExecWatchdog(intent.RequestID, pending)
		}
		return nil
	}
	markExecCompleted(stream, pending)
	// Shell 指纹熔断：terminal 拒绝事件计入本轮账本，达到阈值后开路。
	if strings.TrimSpace(pending.ExecKind) == "shell" {
		if err := service.recordShellRejection(stream, pending, intent.ExecClientMessage); err != nil {
			return err
		}
	}
	if strings.TrimSpace(pending.ExecKind) == "subagent" {
		status := delegation.TaskCompleted
		progress := "Cursor 子代理已完成"
		errorText := ""
		runStatus := agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_SUCCESS
		if subagentResultFailed(intent.ExecClientMessage.GetSubagentResult()) {
			status = delegation.TaskFailed
			progress = "Cursor 子代理执行失败"
			errorText = subagentResultErrorText(intent.ExecClientMessage.GetSubagentResult())
			runStatus = agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_ERROR
		}
		service.updateNativeDelegationStatus(pending.ExecID, status, progress, errorText)
		service.recordDelegationRunTerminal(stream, pending, runStatus, "Cursor 子代理", errorText)
	}
	backgroundShellToolCallID := ""
	if strings.TrimSpace(pending.ExecKind) == "shell" && shellToolCallIsBackgrounded(result.ToolCall) {
		backgroundShellToolCallID = firstNonEmpty(strings.TrimSpace(result.ToolCallID), strings.TrimSpace(pending.ToolCallID))
	}
	if strings.TrimSpace(pending.ExecKind) == "execute_hook_pre_compact" {
		return service.handlePreCompactTerminal(stream, pending.ProviderPass, strings.TrimSpace(result.ToolResultPayload))
	}
	if result.ToolCall != nil {
		if err := service.appendToolResult(stream, result.ToolCallID, deriveToolNameFromPendingExec(pending), pending.ArgsJSON, result.ToolResultPayload, pending.ReasoningContent, result.ToolCall); err != nil {
			return err
		}
	} else if strings.TrimSpace(result.ToolResultPayload) != "" {
		if err := service.appendToolResult(stream, pending.ToolCallID, deriveToolNameFromPendingExec(pending), pending.ArgsJSON, result.ToolResultPayload, pending.ReasoningContent, nil); err != nil {
			return err
		}
	}
	if backgroundShellToolCallID != "" {
		if recordedToolCallID, recorded := recordBackgroundShellActionMemory(stream, backgroundShellToolCallID, time.Now().UTC()); recorded {
			if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
				newBackgroundShellActionMetadataEntry(stream.TurnSeq, stream.RequestID, recordedToolCallID, backgroundShellActionSourceLocalBackgrounded),
			}); err != nil {
				return err
			}
		}
	}
	if err := service.publishToolCallCompleted(intent.RequestID, result.ToolCallID, pending.ModelCallID, result.ToolCall); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, intent.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishExecCheckpoint(stream, pending); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

func execMessageHasEffectiveProgress(message *agentv1.ExecClientMessage, result execbridge.ExecApplyResult) bool {
	if message == nil {
		return false
	}
	if result.ShellOutputDelta != nil {
		return true
	}
	if message.GetSubagentResult() != nil {
		return true
	}
	if shell := message.GetShellStream(); shell != nil {
		switch shell.GetEvent().(type) {
		case *agentv1.ShellStream_Stdout, *agentv1.ShellStream_Stderr, *agentv1.ShellStream_Start, *agentv1.ShellStream_Exit, *agentv1.ShellStream_Rejected, *agentv1.ShellStream_PermissionDenied, *agentv1.ShellStream_Backgrounded:
			return true
		default:
			return false
		}
	}
	return message.GetReadResult() != nil ||
		message.GetWriteResult() != nil ||
		message.GetDeleteResult() != nil ||
		message.GetGrepResult() != nil ||
		message.GetLsResult() != nil ||
		message.GetDiagnosticsResult() != nil ||
		message.GetMcpResult() != nil ||
		message.GetFetchResult() != nil ||
		message.GetExecuteHookResult() != nil ||
		message.GetWriteShellStdinResult() != nil ||
		message.GetSubagentAwaitResult() != nil
}

// handleExecControl 处理执行桥控制面结果，例如 stream_close 或 throw。
func (service *Service) handleExecControl(intent InboundIntent) error {
	if intent.ExecClientControlMessage != nil && service.cursorDelegation != nil && service.cursorDelegation.ConsumeExecControl(intent.RequestID, intent.ExecClientControlMessage) {
		return nil
	}
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		if shouldIgnoreStaleExecControl(intent.ExecClientControlMessage) {
			return nil
		}
		return fmt.Errorf("request is not active: %s", intent.RequestID)
	}
	if intent.ExecClientControlMessage == nil {
		return fmt.Errorf("exec client control message is required")
	}
	pending, found := selectPendingExecByControl(intent.ExecClientControlMessage, stream)
	if !found {
		if shouldIgnoreMissingExecControl(intent.ExecClientControlMessage, stream) {
			return nil
		}
		return fmt.Errorf("pending exec not found for control message")
	}
	if service.ignoreStaleExecProviderPass(stream, pending, "exec_client_control") {
		return nil
	}
	service.markConversationActivity(stream.ConversationID)
	pending = service.applyExecControlProgress(stream, pending, intent.ExecClientControlMessage)
	if isHiddenPatchEditExecKind(pending.ExecKind) {
		return service.handleHiddenPatchEditExecControl(stream, pending, intent.ExecClientControlMessage)
	}
	if isHiddenWriteExecKind(pending.ExecKind) {
		return service.handleHiddenWriteExecControl(stream, pending, intent.ExecClientControlMessage)
	}
	result, err := service.execBridge.ApplyExecClientControl(intent.ExecClientControlMessage, pending)
	if err != nil {
		if strings.TrimSpace(pending.ExecKind) == "subagent" {
			service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskFailed, "Cursor 子代理控制通道失败", err.Error())
		}
		return err
	}
	if !result.IsTerminal {
		if shouldRecoverNonStreamingExecOnStreamClose(intent.ExecClientControlMessage, pending) {
			markExecTransportClosed(stream, pending)
			service.scheduleNonStreamingExecRecovery(intent.RequestID, pending)
			return nil
		}
		if shouldObserveShellStreamClose(intent.ExecClientControlMessage, pending) {
			service.observeShellStreamClose(stream, pending)
		}
		return nil
	}
	markExecCompleted(stream, pending)
	if strings.TrimSpace(pending.ExecKind) == "subagent" {
		service.updateNativeDelegationStatus(pending.ExecID, delegation.TaskFailed, "Cursor 子代理被控制通道终止", strings.TrimSpace(result.ToolResultPayload))
	}
	if strings.TrimSpace(pending.ExecKind) == "execute_hook_pre_compact" {
		return service.handlePreCompactTerminal(stream, pending.ProviderPass, "")
	}
	if strings.TrimSpace(result.ToolResultPayload) != "" {
		if err := service.appendToolResult(stream, pending.ToolCallID, deriveToolNameFromPendingExec(pending), pending.ArgsJSON, result.ToolResultPayload, pending.ReasoningContent, nil); err != nil {
			return err
		}
		_, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newMetadataEntry(stream.TurnSeq, stream.RequestID, "tool_control", map[string]any{
				"tool_call_id": result.ToolCallID,
				"payload":      result.ToolResultPayload,
			}),
		})
		if err != nil {
			return err
		}
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, intent.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishToolCallCompleted(intent.RequestID, result.ToolCallID, pending.ModelCallID, nil); err != nil {
		return err
	}
	if err := service.publishExecCheckpoint(stream, pending); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

func shouldRecoverNonStreamingExecOnStreamClose(message *agentv1.ExecClientControlMessage, pending runtimecore.PendingExec) bool {
	if message == nil || isStreamingPendingExecKind(pending.ExecKind) {
		return false
	}
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return true
	default:
		return false
	}
}

func shouldObserveShellStreamClose(message *agentv1.ExecClientControlMessage, pending runtimecore.PendingExec) bool {
	if message == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return false
	}
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return true
	default:
		return false
	}
}

func isStreamingPendingExecKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "shell":
		return true
	default:
		return false
	}
}

func markExecTransportClosed(stream *ActiveStream, pending runtimecore.PendingExec) {
	if stream == nil {
		return
	}
	stream.mu.Lock()
	current, ok := stream.PendingExecs[pending.ExecID]
	if ok {
		now := time.Now().UTC()
		current.StreamState = "transport_closed"
		current.LastShellActivityAt = now
		stream.PendingExecs[pending.ExecID] = current
		stream.UpdatedAt = now
	}
	stream.mu.Unlock()
}

func snapshotPendingExec(stream *ActiveStream, execID string) (runtimecore.PendingExec, bool) {
	if stream == nil || strings.TrimSpace(execID) == "" {
		return runtimecore.PendingExec{}, false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	item, ok := stream.PendingExecs[strings.TrimSpace(execID)]
	return item, ok
}

func (service *Service) scheduleNonStreamingExecRecovery(requestID string, pending runtimecore.PendingExec) {
	if service == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(pending.ExecID) == "" {
		return
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return
	}
	service.scheduleStreamTimer(
		stream,
		providerTimerKey(streamTimerNonStreamingRecovery, pending.ExecID),
		nonStreamingExecCloseGrace,
		streamTimerNonStreamingRecovery,
		pending.ExecID,
		pending.MessageID,
		"",
	)
}

func (service *Service) recoverNonStreamingExecAfterStreamClose(stream *ActiveStream, pending runtimecore.PendingExec) error {
	if stream == nil {
		return nil
	}
	markExecCompleted(stream, pending)
	toolName := strings.TrimSpace(deriveToolNameFromPendingExec(pending))
	resultPayload := fmt.Sprintf("%s transport closed before terminal result arrived", firstNonEmpty(toolName, pending.ExecKind, "tool"))
	log.Printf("forwarder synthetic exec recovery request_id=%s tool_call_id=%s message_id=%d exec_id=%s exec_kind=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), pending.MessageID, strings.TrimSpace(pending.ExecID), strings.TrimSpace(pending.ExecKind))
	if toolName != "" {
		if err := service.appendToolResult(stream, pending.ToolCallID, toolName, pending.ArgsJSON, resultPayload, pending.ReasoningContent, nil); err != nil {
			return err
		}
	}
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "tool_transport_closed", map[string]any{
			"tool_call_id": pending.ToolCallID,
			"message_id":   pending.MessageID,
			"exec_id":      pending.ExecID,
			"exec_kind":    pending.ExecKind,
			"payload":      resultPayload,
		}),
	}); err != nil {
		return err
	}
	if err := service.syncSummaryCarryForward(stream.ConversationID, stream.RequestID, pending.ModelCallID); err != nil {
		return err
	}
	if err := service.publishToolCallCompleted(stream.RequestID, pending.ToolCallID, pending.ModelCallID, nil); err != nil {
		return err
	}
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		return err
	}
	return service.reconcileStream(stream)
}

func (service *Service) observeShellStreamClose(stream *ActiveStream, pending runtimecore.PendingExec) {
	if service == nil || stream == nil {
		return
	}
	current, ok := snapshotPendingExec(stream, pending.ExecID)
	if !ok {
		return
	}
	recentState := strings.TrimSpace(current.StreamState)
	if recentState == "transport_closed" || recentState == "exited" || recentState == "backgrounded" || recentState == "rejected" || recentState == "permission_denied" || recentState == "sandbox_unsupported" {
		return
	}
	log.Printf(
		"forwarder shell stream closed without terminal event request_id=%s tool_call_id=%s message_id=%d exec_id=%s stream_state=%s chunk_count=%d",
		strings.TrimSpace(stream.RequestID),
		strings.TrimSpace(current.ToolCallID),
		current.MessageID,
		strings.TrimSpace(current.ExecID),
		recentState,
		current.ChunkCount,
	)
	markExecTransportClosed(stream, current)
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "shell_stream_transport_closed", map[string]any{
			"tool_call_id":        current.ToolCallID,
			"message_id":          current.MessageID,
			"exec_id":             current.ExecID,
			"exec_kind":           current.ExecKind,
			"recent_stream_state": recentState,
			"chunk_count":         current.ChunkCount,
			"first_chunk_at":      current.FirstChunkAt,
			"reasoning_present":   strings.TrimSpace(current.ReasoningContent) != "",
			"stdout_buffer_bytes": len(current.StdoutBuffer),
			"stderr_buffer_bytes": len(current.StderrBuffer),
		}),
	}); err != nil {
		log.Printf("forwarder shell stream close metadata failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(current.ToolCallID), err)
	}
	service.scheduleShellTransportCloseRecovery(stream.RequestID, current)
}

// handleMetadataIntent 处理当前不驱动 provider 的轻量元数据上行。
func (service *Service) handleMetadataIntent(intent InboundIntent) error {
	stream, ok := service.broker.Get(intent.RequestID)
	if !ok || stream == nil {
		if intent.HasExplicitMode || intent.StartsRun {
			return fmt.Errorf("metadata intent requires active request context: %s", intent.RequestID)
		}
		return nil
	}
	backgroundShellToolCallID, backgroundShellActionWasNew := observeBackgroundShellAction(stream, intent.ClientMessage)
	observeBackgroundTaskCompletionAction(stream, intent.ClientMessage)
	backgroundSubagentToolCallID, backgroundSubagentActionWasNew := observeBackgroundSubagentAction(stream, intent.ClientMessage)
	if !checkpointConversationInitialized(stream) {
		if intent.HasExplicitMode {
			stream.mu.Lock()
			stream.Mode = intent.Mode
			stream.UpdatedAt = time.Now().UTC()
			stream.mu.Unlock()
		}
		return nil
	}
	entries := []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "metadata", map[string]any{
			"kind":       intent.Kind,
			"starts_run": intent.StartsRun,
		}),
	}
	if backgroundShellToolCallID != "" && backgroundShellActionWasNew {
		entries = append(entries, newBackgroundShellActionMetadataEntry(stream.TurnSeq, stream.RequestID, backgroundShellToolCallID, backgroundShellActionSourceClient))
	}
	if backgroundSubagentToolCallID != "" && backgroundSubagentActionWasNew {
		entries = append(entries, newBackgroundSubagentActionMetadataEntry(stream.TurnSeq, stream.RequestID, backgroundSubagentToolCallID, backgroundShellActionSourceClient))
	}
	entries = append(entries, backgroundTaskCompletionMetadataEntries(stream.TurnSeq, stream.RequestID, intent.ClientMessage)...)
	if intent.HasExplicitMode {
		modeEntry, err := newModeMetadataEntry(stream.TurnSeq, stream.RequestID, intent.Mode, true, intent.ModeSource)
		if err != nil {
			return err
		}
		modeAliasValue, err := modeAlias(intent.Mode)
		if err != nil {
			return err
		}
		entries = append(entries, modeEntry, newModeChangePromptContextEntry(stream.TurnSeq, stream.RequestID, intent.Mode))
		stream.mu.Lock()
		stream.Mode = intent.Mode
		stream.UpdatedAt = time.Now().UTC()
		stream.mu.Unlock()
		if _, err := service.updateConversationMetaAndCheckpoint(stream, stream.ConversationID, func(item *ConversationFile) error {
			if item == nil {
				return nil
			}
			item.Mode = modeAliasValue
			return nil
		}); err != nil {
			return err
		}
	}
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, entries); err != nil {
		return err
	}
	if intent.HasExplicitMode {
		stream.mu.Lock()
		modelCallID := strings.TrimSpace(stream.CurrentModelCallID)
		stream.mu.Unlock()
		if modelCallID != "" {
			if err := service.syncSummaryCarryForward(stream.ConversationID, intent.RequestID, modelCallID); err != nil {
				return err
			}
		}
		if err := service.publishCheckpoint(intent.RequestID, stream.ConversationID); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) scheduleProviderResume(stream *ActiveStream, _ int) error {
	return service.requestProviderAction(stream, providerActionResume)
}

func shouldResumeAfterToolResults(finishReason string) bool {
	switch strings.TrimSpace(finishReason) {
	case "tool_use", "tool_calls", "function_call":
		return true
	default:
		return false
	}
}

func (service *Service) cancelScheduledProviderResume(stream *ActiveStream) {
	if stream == nil {
		return
	}
	clearStreamTimer(stream, providerTimerKey(streamTimerProviderResume, ""))
}

// driveProvider 由 actor 触发一次 provider pass，并把真实流包装成 provider_event 回投 mailbox。
func (service *Service) driveProvider(stream *ActiveStream) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	if stream.ProviderActive || stream.Status == StreamStatusCanceled || stream.Status == StreamStatusCompleted || stream.Status == StreamStatusFailed {
		stream.mu.Unlock()
		return nil
	}
	stream.ProviderPassCount++
	currentPass := stream.ProviderPassCount
	stream.Status = StreamStatusStreaming
	stream.PendingProviderAction = providerActionNone
	stream.CurrentModelCallID = uuid.NewString()
	stream.CurrentProviderToken++
	currentToken := stream.CurrentProviderToken
	stream.ProviderAccumulatedText = ""
	stream.ProviderAccumulatedReasoning = ""
	stream.ProviderAccumulatedReasoningSignature = ""
	stream.ProviderAccumulatedReasoningSignatureSource = ""
	stream.ProviderAccumulatedReasoningItemID = ""
	stream.ProviderAccumulatedReasoningStatus = ""
	stream.ProviderAccumulatedReasoningSummary = nil
	if stream.ProviderSyntheticThinkingStartedAt.IsZero() {
		stream.ProviderSyntheticThinkingStartedAt = time.Now().UTC()
	}
	// Synthetic encrypted-thinking placeholder 属于整个 Cursor turn，而不是
	// 单个 provider pass。保留 Published 标记，避免工具调用后的重试 pass
	// 再次创建 Cursor 思考块；新 turn 在 handleRunIntent 中统一清零。
	stream.ProviderFinishReason = ""
	stream.ProviderUsage = turnUsageSnapshot{}
	stream.ToolInvocationCount = 0
	stream.StaleToolResultSnipApplied = false
	modelCallID := stream.CurrentModelCallID
	conversationID := stream.ConversationID
	requestID := stream.RequestID
	modelID := stream.ModelID
	modelName := stream.ModelName
	thinkingEffort := stream.ThinkingEffort
	maxMode := stream.MaxMode
	mode := stream.Mode
	latestUserText := stream.LatestUserText
	goal := stream.Goal
	customSystemPrompt := stream.CustomSystemPrompt
	thinkingCompletedPublished := stream.ProviderSyntheticThinkingPublished
	thinkingDeltaCount := stream.ProviderThinkingDeltaCount
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if goal != nil {
		if frag := goalSystemPromptFragment(goal, service.currentGoalConfig()); frag != "" {
			customSystemPrompt = joinNonEmpty(customSystemPrompt, frag)
		}
	}
	log.Printf("forwarder provider pass started request_id=%s model_call_id=%s provider_pass=%d thinking_completed=%t thinking_delta_count=%d", strings.TrimSpace(requestID), strings.TrimSpace(modelCallID), currentPass, thinkingCompletedPublished, thinkingDeltaCount)
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, conversationID, "provider_pass_started", map[string]any{
			"model_call_id":                strings.TrimSpace(modelCallID),
			"provider_pass":                currentPass,
			"thinking_completed_published": thinkingCompletedPublished,
			"thinking_delta_count":         thinkingDeltaCount,
		})
	}

	conversation, _, _, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	conversation, err = service.syncConversationContextWindowTokens(stream, conversationID, conversation)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	conversation, err = service.persistDerivedPromptContexts(stream, conversationID, requestID, conversation, mode, latestUserText)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	compiled, err := service.compiler.Compile(conversation, mode, latestUserText, modelName, customSystemPrompt)
	if err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	compiled = guardCompiledConversationForProvider(compiled)
	if compacted, compactErr := service.maybeCompactBeforeProvider(stream, conversation, compiled); compactErr != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", compactErr)
	} else if compacted {
		stream.mu.Lock()
		stream.ProviderActive = false
		stream.ProviderCancel = nil
		stream.UpdatedAt = time.Now().UTC()
		hasPendingCompaction := stream.PendingCompaction != nil
		status := stream.Status
		stream.mu.Unlock()
		switch {
		case isTerminalStreamStatus(status):
			switch status {
			case StreamStatusCompleted:
				service.setTurnPhase(stream, TurnPhaseCompleted)
			case StreamStatusCanceled:
				service.setTurnPhase(stream, TurnPhaseCanceled)
			default:
				service.setTurnPhase(stream, TurnPhaseFailed)
			}
		case hasPendingCompaction:
			service.setTurnPhase(stream, TurnPhaseCompacting)
		default:
			service.setTurnPhase(stream, TurnPhaseIdle)
		}
		return nil
	}
	// 陈旧工具结果 snip/prune 救回：若压缩评估阶段已持久化缩短陈旧工具结果，则前面的
	// conversation/compiled 是 snip 之前的快照，需基于已更新的 checkpoint 重新快照+编译，
	// 让后续 provider 请求使用 snip 后的新鲜历史（参考 tool_result_snip.go）。
	if stream.staleToolResultSnipAppliedLocked() {
		freshConversation, _, _, freshErr := service.snapshotCheckpointConversation(stream)
		if freshErr != nil {
			service.setTurnPhase(stream, TurnPhaseFailed)
			return service.failStream(stream, "unknown", freshErr)
		}
		recompiled, recompileErr := service.compiler.Compile(freshConversation, mode, latestUserText, modelName, customSystemPrompt)
		if recompileErr != nil {
			service.setTurnPhase(stream, TurnPhaseFailed)
			return service.failStream(stream, "unknown", recompileErr)
		}
		conversation = freshConversation
		compiled = guardCompiledConversationForProvider(recompiled)
	}
	if err := service.syncSummarySnapshot(stream, conversation, requestID, modelCallID); err != nil {
		service.setTurnPhase(stream, TurnPhaseFailed)
		return service.failStream(stream, "unknown", err)
	}
	maxTokens, requestKnobs := service.resolveProviderOutputBudget(modelID, modelName, conversation, compiled)
	// max_tokens 超限恢复：若本回合因中转站 400 触发过降级重试，用恢复上限覆盖预算，
	// 确保重试请求的 max_tokens 不超过中转站真实限制。
	stream.mu.Lock()
	recoveryCap := stream.MaxTokensRecoveryCap
	stream.mu.Unlock()
	if recoveryCap > 0 && recoveryCap < maxTokens {
		maxTokens = recoveryCap
		if requestKnobs == nil {
			requestKnobs = map[string]any{}
		}
		requestKnobs["max_tokens_recovery_cap"] = recoveryCap
	}
	service.maybeSaveLastAgentModelHash(conversation, modelID, mode, currentPass)
	// 视觉代理：主模型不支持图片输入时，自动把消息中的图片委派给识图模型，
	// 用返回的画面描述 / OCR 文本替换图片块，使纯文本模型也能“看图”。
	// 此处持有 service.provider，可发起同步子调用；替换后不再含图片 ContentPart，
	// 下游 router.stripImagesFromMessages 会原样放行，不会重复处理。
	// 未启用视觉委派时，从工具清单剔除 see_image，避免模型调用一个不可用的工具。
	if service.needsVisionProxy(modelID, modelName, compiled.Messages) {
		vdbg("[service] needsVisionProxy true -> run vision pass msgs=%d model=%s model_id=%s", len(compiled.Messages), modelName, modelID)
		visionCtx, visionCancel := context.WithCancel(context.Background())
		compiled.Messages = service.synthesizeImageDescriptions(visionCtx, requestID, conversationID, compiled.Messages, modelName)
		visionCancel()
	} else {
		vdbg("[service] needsVisionProxy false enabled=%v", service.visionProxyEnabled())
	}
	if !service.visionProxyEnabled() {
		compiled.Tools = filterToolDescriptorByName(compiled.Tools, seeImageToolName)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream.mu.Lock()
	stream.ProviderActive = true
	stream.ProviderCancel = cancel
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	service.setTurnPhase(stream, TurnPhaseProviderRunning)

	providerRequest := ProviderRequest{
		RequestID:          requestID,
		ConversationID:     conversationID,
		RunID:              requestID,
		ModelCallID:        modelCallID,
		ModelID:            modelID,
		ModelName:          modelName,
		Role:               "parent",
		ExecutionMode:      "parent",
		Mode:               compiled.Mode,
		ThinkingEffort:     compiled.Mode.String(),
		MaxMode:            maxMode,
		Messages:           compiled.Messages,
		StableMessageCount: compiled.StableMessageCount,
		Tools:              compiled.Tools,
		MaxTokens:          maxTokens,
		RequestKnobs:       requestKnobs,
		CompileSummary:     compiled.CompileSummary,
		Observer:           service.recorder,
		ArtifactPaths:      &modeladapter.LLMArtifactPaths{},
	}
	providerRequest.ThinkingEffort = thinkingEffort
	service.debug.LogProvider(context.Background(), requestID, conversationID, "provider_request_prepared", map[string]any{
		"model_call_id":          strings.TrimSpace(modelCallID),
		"provider_pass":          currentPass,
		"model_id":               strings.TrimSpace(modelID),
		"model_name":             strings.TrimSpace(modelName),
		"mode":                   compiled.Mode.String(),
		"thinking_effort":        strings.TrimSpace(thinkingEffort),
		"max_tokens":             maxTokens,
		"request_knobs":          requestKnobs,
		"message_count":          len(compiled.Messages),
		"tool_count":             len(compiled.Tools),
		"compile_summary_length": len(compiled.CompileSummary),
	})
	go service.runProviderStream(stream, currentToken, ctx, providerRequest)
	return nil
}

func (service *Service) resolveProviderOutputBudget(modelID string, modelName string, conversation *ConversationFile, compiled CompiledConversation) (int, map[string]any) {
	configuredMaxTokens := service.resolveConfiguredProviderMaxOutputTokens(modelID)
	contextWindowTokens := compactionContextWindowSize(conversation)
	estimatedPromptTokens := estimateCompiledPromptTokens(compiled)
	if conversation != nil && int64(conversation.TokenDetailsUsedTokens) > estimatedPromptTokens {
		estimatedPromptTokens = int64(conversation.TokenDetailsUsedTokens)
	}
	remainingTokens := int64(0)
	requestMaxTokens := int64(configuredMaxTokens)
	if requestMaxTokens <= 0 {
		requestMaxTokens = providerDefaultMaxOutputTokens
	}
	// catalog 记录了每个模型 provider 侧允许的最大输出 token 数。
	// 某些 provider（如 Neurons 代理的 k2.7）会对超出的 max_tokens 直接返回 400，
	// 因此这里必须把它当作硬上限：无论 channel 配了多大的值，都不能超过模型上限。
	//
	// 注意：modelID 可能是客户端内部哈希 ID（如 "4fd90578ea9510b1"），catalog 无法匹配；
	// 必须优先用显示名 modelName（如 "kimi-k2.7-code"）查 catalog，否则会返回 0 导致 cap 失效、
	// 发出默认 65536 触发中转站 400。modelID 仅作兜底。
	catalogModelKey := strings.TrimSpace(modelName)
	if catalogModelKey == "" {
		catalogModelKey = strings.TrimSpace(modelID)
	}
	catalogMax := int64(modelcontext.MaxOutputTokens(catalogModelKey))
	if catalogMax <= 0 {
		// 显示名未命中时再用 modelID 兜底（少数场景 modelID 即真实模型名）。
		catalogMax = int64(modelcontext.MaxOutputTokens(modelID))
	}
	if catalogMax > 0 && catalogMax < requestMaxTokens {
		requestMaxTokens = catalogMax
	}
	if contextWindowTokens > 0 && estimatedPromptTokens > 0 {
		remainingTokens = contextWindowTokens - estimatedPromptTokens
		allowedTokens := remainingTokens - providerOutputSafetyTokens
		if allowedTokens < 1 {
			allowedTokens = 1
		}
		if allowedTokens < requestMaxTokens {
			requestMaxTokens = allowedTokens
		}
	}
	maxTokens := int(requestMaxTokens)
	if maxTokens <= 0 {
		maxTokens = 1
	}
	requestKnobs := map[string]any{
		"configured_max_tokens":             configuredMaxTokens,
		"dynamic_max_tokens":                maxTokens,
		"catalog_model_key":                 catalogModelKey,
		"catalog_max_output_tokens":         modelcontext.MaxOutputTokens(catalogModelKey),
		"compiled_prompt_tokens_estimate":   estimatedPromptTokens,
		"context_window_tokens":             contextWindowTokens,
		"remaining_context_tokens_estimate": remainingTokens,
		"provider_output_safety_tokens":     providerOutputSafetyTokens,
	}
	return maxTokens, withPreviousCacheFrontierHint(requestKnobs, conversation)
}

func withPreviousCacheFrontierHint(requestKnobs map[string]any, conversation *ConversationFile) map[string]any {
	if len(requestKnobs) == 0 {
		requestKnobs = map[string]any{}
	}
	if conversation == nil || conversation.LatestRequestPrefix == nil {
		return requestKnobs
	}
	prefix := conversation.LatestRequestPrefix
	frontierHash := strings.TrimSpace(prefix.FrontierHash)
	if frontierHash == "" {
		return requestKnobs
	}
	requestKnobs["previous_cache_frontier_hash"] = frontierHash
	requestKnobs["previous_cache_frontier"] = map[string]any{
		"canonical_body_hash": prefix.CanonicalBodyHash,
		"frontier_hash":       frontierHash,
		"frontier_path":       prefix.FrontierPath,
		"breakpoint_count":    prefix.BreakpointCount,
		"request_id":          strings.TrimSpace(prefix.RequestID),
		"model_call_id":       strings.TrimSpace(prefix.ModelCallID),
	}
	return requestKnobs
}

func (service *Service) resolveConfiguredProviderMaxOutputTokens(modelID string) int {
	if service == nil || service.resolver == nil {
		return providerDefaultMaxOutputTokens
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil {
		return providerDefaultMaxOutputTokens
	}
	maxTokens := configuredProviderMaxOutputTokens(channel.Provider, channel.MaxTokens, channel.AnthropicMaxTokens)
	if maxTokens <= 0 {
		return providerDefaultMaxOutputTokens
	}
	return maxTokens
}

func configuredProviderMaxOutputTokens(provider string, maxTokens int, anthropicMaxTokens int) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		if anthropicMaxTokens > 0 {
			return anthropicMaxTokens
		}
		if maxTokens > 0 {
			return maxTokens
		}
	case "openai":
		if maxTokens > 0 {
			return maxTokens
		}
		if anthropicMaxTokens > 0 {
			return anthropicMaxTokens
		}
	default:
		if maxTokens > 0 && anthropicMaxTokens > 0 {
			if anthropicMaxTokens > maxTokens {
				return anthropicMaxTokens
			}
			return maxTokens
		}
		if maxTokens > 0 {
			return maxTokens
		}
		if anthropicMaxTokens > 0 {
			return anthropicMaxTokens
		}
	}
	return providerDefaultMaxOutputTokens
}

func (service *Service) maybeSaveLastAgentModelHash(conversation *ConversationFile, modelID string, mode agentv1.AgentMode, providerPass int) {
	if service == nil || service.modelMemory == nil || service.resolver == nil {
		return
	}
	if providerPass != 1 || !isSupportedActiveMode(mode) {
		return
	}
	if conversation != nil && strings.TrimSpace(conversation.SubagentTypeName) != "" {
		return
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil || strings.TrimSpace(channel.ID) == "" {
		if err != nil {
			log.Printf("forwarder skipped last agent model hash update model_id=%s error=%v", strings.TrimSpace(modelID), err)
		}
		return
	}
	if err := service.modelMemory.SaveLastAgentModelHash(context.Background(), strings.TrimSpace(channel.ID)); err != nil {
		log.Printf("forwarder failed to save last agent model hash channel_id=%s error=%v", strings.TrimSpace(channel.ID), err)
	}
}

func (service *Service) persistDerivedPromptContexts(stream *ActiveStream, conversationID string, requestID string, conversation *ConversationFile, mode agentv1.AgentMode, latestUserText string) (*ConversationFile, error) {
	if stream == nil {
		return nil, fmt.Errorf("active stream is required")
	}
	if service == nil || service.compiler == nil {
		return conversation, nil
	}
	contexts, err := service.compiler.DerivePromptContexts(conversation, mode, latestUserText)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return conversation, nil
	}
	stream.mu.Lock()
	turnSeq := stream.TurnSeq
	stream.mu.Unlock()
	if turnSeq <= 0 {
		return conversation, nil
	}
	entries := make([]HistoryEntry, 0, len(contexts))
	for _, context := range contexts {
		context = normalizePromptContextMessage(context)
		if !isReplayablePromptContext(context) {
			continue
		}
		entries = append(entries, newPromptContextEntry(turnSeq, requestID, context))
	}
	if len(entries) == 0 {
		return conversation, nil
	}
	if _, err := service.appendConversationEntries(stream, conversationID, entries); err != nil {
		return nil, err
	}
	conversation, _, _, err = service.snapshotCheckpointConversation(stream)
	return conversation, err
}

func (service *Service) runProviderStream(stream *ActiveStream, token uint64, ctx context.Context, request ProviderRequest) {
	err := service.provider.StartStream(ctx, request, func(event modeladapter.ModelEvent) error {
		return service.postStreamCommandWait(stream, streamCommand{
			Kind: streamCommandProviderEvent,
			Provider: &streamProviderEvent{
				Token: token,
				Event: event,
			},
		})
	})
	if postErr := service.postStreamCommandWait(stream, streamCommand{
		Kind: streamCommandProviderEvent,
		Provider: &streamProviderEvent{
			Token: token,
			Done:  true,
			Err:   err,
		},
	}); postErr != nil && !errors.Is(postErr, errProviderLoopInterrupted) {
		service.debug.LogProvider(context.Background(), request.RequestID, request.ConversationID, "provider_completion_post_error", map[string]any{
			"model_call_id":  strings.TrimSpace(request.ModelCallID),
			"provider_token": token,
			"error":          postErr.Error(),
		})
		log.Printf(
			"forwarder provider completion post failed request_id=%s model_call_id=%s provider_token=%d err=%v",
			strings.TrimSpace(request.RequestID),
			strings.TrimSpace(request.ModelCallID),
			token,
			postErr,
		)
		_ = service.failStreamIfNonTerminal(stream, "unknown", postErr)
	}
	if err != nil {
		service.debug.LogProvider(context.Background(), request.RequestID, request.ConversationID, "provider_stream_finished", map[string]any{
			"model_call_id":  strings.TrimSpace(request.ModelCallID),
			"provider_token": token,
			"error":          err.Error(),
		})
		return
	}
	service.debug.LogProvider(context.Background(), request.RequestID, request.ConversationID, "provider_stream_finished", map[string]any{
		"model_call_id":  strings.TrimSpace(request.ModelCallID),
		"provider_token": token,
	})
}

// handleToolInvocation 把模型产生的工具意图转成 exec/interaction 请求并下发给客户端。
func (service *Service) handleToolInvocation(stream *ActiveStream, invocation runtimecore.ToolInvocation) error {
	if err := providerLoopInterruptErr(nil, stream, invocation.ModelCallID); err != nil {
		return err
	}
	// Shell 指纹熔断：本轮内同一确定性拒绝达到阈值后，Shell 对剩余轮次不可用。
	if strings.TrimSpace(invocation.ToolName) == "Shell" {
		circuit := currentTurnShellCircuit(stream)
		if circuit.Open {
			stream.mu.Lock()
			stream.ToolInvocationCount++
			stream.UpdatedAt = time.Now().UTC()
			stream.mu.Unlock()
			if err := service.recordShellCircuitLocalBlock(stream, invocation, circuit); err != nil {
				return err
			}
			cause := fmt.Errorf("Shell is unavailable for the rest of this turn after a %s rejection; use a non-Shell tool or finish with the blocker", circuit.RejectionClass)
			if err := service.completePreDispatchToolError(stream, invocation, nil, false, false, cause); err != nil {
				return err
			}
			if circuit.LocalBlocks+1 >= shellCircuitLocalBlockLimit {
				return providerTerminalError{cause: fmt.Errorf("Shell circuit stopped the provider loop after %d local blocks", circuit.LocalBlocks+1)}
			}
			return nil
		}
	}
	invocation = service.rewriteDirectMCPToolInvocation(stream, invocation)
	invocation = service.normalizeCallMCPToolInvocation(stream, invocation)
	trimmedToolName := strings.TrimSpace(invocation.ToolName)
	signature := delegation.NormalizeToolSignature(trimmedToolName, invocation.ArgsJSON)
	stream.mu.Lock()
	mode := stream.Mode
	providerPass := stream.ProviderPassCount
	subagentTypeName := ""
	if stream.CheckpointConversation != nil {
		subagentTypeName = strings.TrimSpace(stream.CheckpointConversation.SubagentTypeName)
	}
	stream.ToolInvocationCount++
	stream.UpdatedAt = time.Now().UTC()
	// B1 doom loop 检测：以（工具名+规范化参数）签名对连续相同调用计数。
	// 签名变化即重置；达到硬阈值时中断本轮，达到警告阈值时注入提示。
	// 轮询型工具（SubagentAwait/AwaitShell）按设计就会以相同参数反复调用，
	// 不参与计数，否则会误杀正在等待长任务子代理的正常轮询。
	doomLoopCount := 0
	countsDoomLoop := !isPollingAwaitTool(trimmedToolName)
	if countsDoomLoop {
		if stream.lastDoomLoopSignature != signature {
			stream.doomLoopCounts = map[string]int{}
			stream.lastDoomLoopSignature = signature
		}
		if stream.doomLoopCounts == nil {
			stream.doomLoopCounts = map[string]int{}
		}
		stream.doomLoopCounts[signature]++
		doomLoopCount = stream.doomLoopCounts[signature]
	}
	stream.mu.Unlock()
	if doomLoopCount >= doomLoopHardLimit {
		return service.completePreDispatchToolError(stream, invocation, nil, false, false,
			fmt.Errorf("检测到 %s 以相同参数连续调用 %d 次，已中断本轮：请先阅读之前的工具结果并改变策略", trimmedToolName, doomLoopCount))
	}
	if doomLoopCount == doomLoopThreshold {
		stream.mu.Lock()
		stream.pendingDoomLoopNotice = fmt.Sprintf("[检测到 %s 以相同参数连续调用 %d 次，请先阅读上次工具结果并改变策略]", trimmedToolName, doomLoopCount)
		stream.mu.Unlock()
	}
	delegationEnabled := false
	delegationSupervision := false
	delegationGroups := 0
	if service != nil && service.multitaskDelegation != nil {
		config := service.multitaskDelegation.runtimeConfig()
		delegationEnabled = config.Enabled
		delegationSupervision = config.SupervisionEnabled
		delegationGroups = len(config.Groups)
	}
	log.Printf("forwarder tool invocation request_id=%s conversation_id=%s mode=%s tool=%s call_id=%s model_call_id=%s provider_pass=%d multitask_coordinator=%t delegation_enabled=%t supervision_enabled=%t delegation_groups=%d",
		strings.TrimSpace(stream.RequestID), strings.TrimSpace(stream.ConversationID), mode.String(), trimmedToolName, strings.TrimSpace(invocation.CallID), strings.TrimSpace(invocation.ModelCallID), providerPass, service != nil && service.multitaskDelegation != nil, delegationEnabled, delegationSupervision, delegationGroups)
	if service != nil && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "tool_invocation_routed", map[string]any{
			"mode":                   mode.String(),
			"tool_name":              trimmedToolName,
			"call_id":                strings.TrimSpace(invocation.CallID),
			"model_call_id":          strings.TrimSpace(invocation.ModelCallID),
			"provider_pass":          providerPass,
			"multitask_coordinator":  service != nil && service.multitaskDelegation != nil,
			"delegation_enabled":     delegationEnabled,
			"supervision_enabled":    delegationSupervision,
			"delegation_group_count": delegationGroups,
		})
	}
	if !isToolAllowedInMode(mode, subagentTypeName, trimmedToolName) {
		return service.completePreDispatchToolError(stream, invocation, nil, false, false, fmt.Errorf("tool invocation is not enabled in mode %s: %s", mode.String(), invocation.ToolName))
	}
	// inspect 子代理（Task 子会话 + PLAN 模式）的 Shell 调用在服务端强制只读白名单，
	// 校验失败直接拒绝，不依赖提示词描述；校验通过时注入 --no-pager 等保护参数。
	if trimmedToolName == "Shell" && isChildConversationSubagentTypeName(subagentTypeName) && normalizeMode(mode) == agentv1.AgentMode_AGENT_MODE_PLAN {
		rewritten, policyErr := service.enforceReadonlyShellPolicy(stream, invocation)
		if policyErr != nil {
			opened, recordErr := service.recordPreDispatchShellRejection(stream, invocation, policyErr)
			if recordErr != nil {
				return recordErr
			}
			if opened {
				// 第二次同指纹失败：附上明确纠正指引并开路，后续同类调用被 circuit.Open 分支拦截。
				policyErr = fmt.Errorf("%s. Do not retry this Shell command this turn — the same deterministic validation error will repeat and Shell is now blocked; use Read/Grep/Glob instead or report the blocker", policyErr.Error())
			}
			return service.completePreDispatchToolError(stream, invocation, nil, false, false, policyErr)
		}
		invocation = rewritten
	}
	var err error
	invocation, err = service.sanitizeCreatePlanInvocationForCurrentPlan(stream, invocation)
	if err != nil {
		if cause, ok := recoverableToolInvocationCause(err); ok {
			return service.completePreDispatchToolError(stream, invocation, nil, false, false, cause)
		}
		return err
	}
	if isPatchEditToolName(trimmedToolName) {
		if err := service.handlePatchEditToolInvocation(stream, invocation); err != nil {
			if cause, ok := recoverableToolInvocationCause(err); ok {
				return service.completePreDispatchToolError(stream, invocation, nil, false, false, cause)
			}
			return err
		}
		return nil
	}
	if trimmedToolName == "Write" {
		if err := service.handleWriteToolInvocation(stream, invocation); err != nil {
			if cause, ok := recoverableToolInvocationCause(err); ok {
				return service.completePreDispatchToolError(stream, invocation, nil, false, false, cause)
			}
			return err
		}
		return nil
	}
	isExecInvocation := isExecTool(trimmedToolName)
	isInteractionInvocation := isInteractionTool(trimmedToolName)
	isLocalStateInvocation := isLocalStateTool(trimmedToolName)
	isImmediateNativeInvocation := isImmediateNativeTool(trimmedToolName)
	if !isExecInvocation && !isInteractionInvocation && !isLocalStateInvocation && !isImmediateNativeInvocation {
		available := ""
		if service.toolCatalog != nil {
			if _, names, loadErr := service.toolCatalog.Load(mode, subagentTypeName); loadErr == nil && len(names) > 0 {
				available = fmt.Sprintf("（可用工具：%s）", strings.Join(names, ", "))
			}
		}
		return service.completePreDispatchToolError(stream, invocation, nil, false, false, fmt.Errorf("unsupported tool invocation: %s%s", invocation.ToolName, available))
	}
	var subagentOverrides map[string]runtimecore.SubagentModelOverrideSelection
	if isExecInvocation {
		subagentOverrides = cloneSubagentModelOverrides(stream.SubagentModelOverrides)
		if resolutionPayload := taskSubagentModelResolutionPayload(invocation, stream.ModelID, subagentOverrides); resolutionPayload != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "subagent_model_override_resolved", resolutionPayload)
		}
		invocation = rewriteTaskInvocationModelForDisplay(invocation, stream.ModelID, subagentOverrides)
	}
	bufferExecDispatch := isExecInvocation && shouldBufferExecDispatch(invocation.ToolName)
	suppressStartedToolCall := shouldSuppressStartedToolCallAfterPartial(stream, trimmedToolName, invocation.CallID)
	startedToolCall := buildStartedToolCall(invocation)
	startedEmitted := suppressStartedToolCall
	delegatedTaskStarted := false
	nativeTaskOpened := false
	var nativeTaskServerMessage *agentv1.AgentServerMessage
	var nativeTaskPending runtimecore.PendingExec
	ensureLoopActive := func() error {
		return providerLoopInterruptErr(nil, stream, invocation.ModelCallID)
	}
	if autoStarted, autoErr := service.maybeStartAutomaticMultitaskDelegation(stream, invocation); autoErr != nil {
		log.Printf("forwarder automatic multitask delegation ignored request_id=%s tool=%s reason=%v", strings.TrimSpace(stream.RequestID), trimmedToolName, autoErr)
	} else if autoStarted {
		log.Printf("forwarder automatic multitask delegation started request_id=%s trigger_tool=%s", strings.TrimSpace(stream.RequestID), trimmedToolName)
	}
	if startedToolCall != nil {
		if err := ensureLoopActive(); err != nil {
			return err
		}
		toolCallPayload, err := protojson.Marshal(startedToolCall)
		if err != nil {
			return err
		}
		_, err = service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
			newToolCallEntryWithProviderMetadata(stream.TurnSeq, stream.RequestID, invocation.CallID, invocation.ToolName, invocation.ReasoningContent, invocation.ReasoningSignature, invocation.ReasoningSignatureSource, invocation.ReasoningProviderItemID, invocation.ReasoningProviderStatus, invocation.ReasoningProviderSummary, invocation.ProviderItemID, invocation.ProviderCallID, invocation.ProviderStatus, toolCallPayload),
		})
		if err != nil {
			return err
		}
	}
	// Cursor creates the Task bubble as soon as tool_call_started arrives. For a
	// locally executed Task, register the aggregate and publish its RUNNING
	// checkpoint first so that the bubble can be associated with a live
	// subagent run instead of briefly falling back to the client's "Stopped"
	// label. A second checkpoint immediately after the bubble is required
	// because the first one predates the client-side tool bubble.
	if trimmedToolName == "Task" && !bufferExecDispatch && !suppressStartedToolCall {
		if err := ensureLoopActive(); err != nil {
			return err
		}
		delegatedTaskStarted, err = service.tryStartDelegatedTask(stream, invocation)
		if err != nil {
			if errors.Is(err, errProviderLoopInterrupted) {
				return err
			}
			return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
		}
		if delegatedTaskStarted {
			log.Printf("forwarder task dispatch order request_id=%s tool_call_id=%s order=aggregate_started_then_checkpoint_then_started", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID))
			if service.debug != nil {
				service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "task_dispatch_order", map[string]any{
					"tool_call_id": strings.TrimSpace(invocation.CallID),
					"order":        "aggregate_started_then_checkpoint_then_started",
				})
			}
		} else {
			// Direct Cursor Task executions must be registered before the client
			// sees tool_call_started. The installed Cursor client creates the Task
			// bubble immediately and labels it Stopped when no RUNNING subagent
			// checkpoint exists at that moment.
			nativeTaskServerMessage, nativeTaskPending, err = service.openNativeTaskExec(stream, invocation, subagentOverrides)
			if err != nil {
				if errors.Is(err, errProviderLoopInterrupted) {
					return err
				}
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
			}
			nativeTaskOpened = true
			log.Printf("forwarder task dispatch order request_id=%s tool_call_id=%s order=native_exec_registered_then_checkpoint_then_started exec_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), strings.TrimSpace(nativeTaskPending.ExecID))
			if service.debug != nil {
				service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "task_dispatch_order", map[string]any{
					"tool_call_id": strings.TrimSpace(invocation.CallID),
					"exec_id":      strings.TrimSpace(nativeTaskPending.ExecID),
					"order":        "native_exec_registered_then_checkpoint_then_started",
				})
			}
		}
	}
	if !bufferExecDispatch && !suppressStartedToolCall {
		if err := ensureLoopActive(); err != nil {
			return err
		}
		if trimmedToolName == "Task" {
			log.Printf("forwarder task tool_call_started publishing request_id=%s tool_call_id=%s model_call_id=%s agent_id=%s args_bytes=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), strings.TrimSpace(invocation.ModelCallID), delegationSubagentID(invocation.CallID), len(invocation.ArgsJSON))
		}
		if err := service.broker.Publish(stream.RequestID, StreamEvent{
			Message: buildToolCallStartedMessage(invocation.CallID, invocation.ModelCallID, startedToolCall),
		}); err != nil {
			if trimmedToolName == "Task" {
				log.Printf("forwarder task tool_call_started publish failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), err)
			}
			return err
		}
		startedEmitted = true
		if trimmedToolName == "Task" && (delegatedTaskStarted || nativeTaskOpened) {
			if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
				log.Printf("forwarder task post-start checkpoint failed request_id=%s tool_call_id=%s err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID), err)
				return err
			}
			log.Printf("forwarder task post-start checkpoint published request_id=%s tool_call_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID))
		}
	}
	if isImmediateNativeInvocation {
		return service.handleImmediateNativeToolInvocation(stream, invocation)
	}
	if isLocalStateInvocation {
		return service.handleLocalStateToolInvocation(stream, invocation)
	}
	if isInteractionInvocation {
		if err := service.handleInteractionToolInvocation(stream, invocation); err != nil {
			if cause, ok := recoverableToolInvocationCause(err); ok {
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, cause)
			}
			return err
		}
		return nil
	}
	if isExecInvocation {
		// A Multitask Task handled by the local delegation aggregate is already
		// registered, checkpointed, and represented in Cursor's Task bubble above.
		// Do not fall through to OpenExec: doing so registers a second native
		// subagent for the same tool call and can leave the foreground turn stuck
		// in Stopped after the native watchdog fires.
		if trimmedToolName == "Task" && delegatedTaskStarted {
			log.Printf("forwarder task local aggregate dispatch complete request_id=%s tool_call_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(invocation.CallID))
			return nil
		}
		if trimmedToolName == "Task" && !delegatedTaskStarted && !nativeTaskOpened {
			started, err := service.tryStartDelegatedTask(stream, invocation)
			if err != nil {
				if errors.Is(err, errProviderLoopInterrupted) {
					return err
				}
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
			}
			if started {
				return nil
			}
		}
		serverMessage := nativeTaskServerMessage
		pendingExec := nativeTaskPending
		if !nativeTaskOpened {
			serverMessage, pendingExec, err = service.execBridge.OpenExec(buildExecOpenContextForStream(stream, subagentOverrides), invocation)
			if err != nil {
				return service.completePreDispatchToolError(stream, invocation, startedToolCall, startedToolCall != nil, startedEmitted, err)
			}
			pendingExec.ModelCallID = invocation.ModelCallID
			pendingExec.ReasoningContent = invocation.ReasoningContent
			pendingExec.ReasoningSignature = invocation.ReasoningSignature
			pendingExec.ReasoningSignatureSource = invocation.ReasoningSignatureSource
			pendingExec = initializePendingExecForTracking(pendingExec)
			stream.mu.Lock()
			pendingExec.ProviderPass = stream.ProviderPassCount
			stream.PendingExecs[pendingExec.ExecID] = pendingExec
			stream.mu.Unlock()
			if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
				service.registerNativeDelegation(stream, pendingExec, serverMessage)
			}
			service.scheduleShellForegroundRecovery(stream.RequestID, pendingExec)
			service.scheduleExecWatchdog(stream.RequestID, pendingExec)
		}
		removePendingExec := func() {
			stream.mu.Lock()
			delete(stream.PendingExecs, pendingExec.ExecID)
			stream.mu.Unlock()
			if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
				status := delegation.TaskFailed
				progress := "Cursor 子代理派发失败"
				if !streamStillActive(stream) {
					status = delegation.TaskCanceled
					progress = "Cursor 子代理已取消"
				}
				service.updateNativeDelegationStatus(pendingExec.ExecID, status, progress, progress)
			}
		}
		if err := ensureLoopActive(); err != nil {
			removePendingExec()
			return err
		}
		if bufferExecDispatch {
			if err := ensureLoopActive(); err != nil {
				removePendingExec()
				return err
			}
			if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: serverMessage}); err != nil {
				removePendingExec()
				return err
			}
			if err := ensureLoopActive(); err != nil {
				removePendingExec()
				return err
			}
			if err := service.broker.Publish(stream.RequestID, StreamEvent{
				Message: buildToolCallStartedMessage(invocation.CallID, invocation.ModelCallID, startedToolCall),
			}); err != nil {
				removePendingExec()
				return err
			}
			startedEmitted = true
			service.recordExecDispatchMetadata(stream, pendingExec, true, startedEmitted, "exec_then_started_then_checkpoint")
			if err := ensureLoopActive(); err != nil {
				removePendingExec()
				return err
			}
			if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
				removePendingExec()
				return err
			}
			return nil
		}
		if err := ensureLoopActive(); err != nil {
			removePendingExec()
			return err
		}
		if err := service.publishCheckpoint(stream.RequestID, stream.ConversationID); err != nil {
			removePendingExec()
			return err
		}
		if err := ensureLoopActive(); err != nil {
			removePendingExec()
			return err
		}
		if err := service.broker.Publish(stream.RequestID, StreamEvent{Message: serverMessage}); err != nil {
			removePendingExec()
			return err
		}
		service.recordExecDispatchMetadata(stream, pendingExec, false, startedEmitted, "started_then_checkpoint_then_exec")
		return nil
	}
	return nil
}

// openNativeTaskExec registers a direct Cursor Task before tool_call_started is
// published. Cursor creates the Task bubble on that event and immediately
// falls back to Stopped when its checkpoint has no RUNNING subagent entry.
func (service *Service) openNativeTaskExec(stream *ActiveStream, invocation runtimecore.ToolInvocation, subagentOverrides map[string]runtimecore.SubagentModelOverrideSelection) (*agentv1.AgentServerMessage, runtimecore.PendingExec, error) {
	if service == nil || stream == nil || service.execBridge == nil {
		return nil, runtimecore.PendingExec{}, fmt.Errorf("cursor exec bridge is unavailable")
	}
	serverMessage, pendingExec, err := service.execBridge.OpenExec(buildExecOpenContextForStream(stream, subagentOverrides), invocation)
	if err != nil {
		return nil, runtimecore.PendingExec{}, err
	}
	pendingExec.ModelCallID = invocation.ModelCallID
	pendingExec.ReasoningContent = invocation.ReasoningContent
	pendingExec.ReasoningSignature = invocation.ReasoningSignature
	pendingExec.ReasoningSignatureSource = invocation.ReasoningSignatureSource
	pendingExec = initializePendingExecForTracking(pendingExec)
	stream.mu.Lock()
	pendingExec.ProviderPass = stream.ProviderPassCount
	stream.PendingExecs[pendingExec.ExecID] = pendingExec
	stream.mu.Unlock()
	if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
		service.registerNativeDelegation(stream, pendingExec, serverMessage)
	}
	if strings.TrimSpace(pendingExec.ExecKind) == "subagent" {
		service.scheduleShellForegroundRecovery(stream.RequestID, pendingExec)
		service.scheduleExecWatchdog(stream.RequestID, pendingExec)
	}
	log.Printf("forwarder native task pre-start registered request_id=%s conversation_id=%s tool_call_id=%s exec_id=%s exec_kind=%s message_id=%d", strings.TrimSpace(stream.RequestID), strings.TrimSpace(stream.ConversationID), strings.TrimSpace(pendingExec.ToolCallID), strings.TrimSpace(pendingExec.ExecID), strings.TrimSpace(pendingExec.ExecKind), pendingExec.MessageID)
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "native_task_prestart_registered", map[string]any{
			"tool_call_id": strings.TrimSpace(pendingExec.ToolCallID),
			"exec_id":      strings.TrimSpace(pendingExec.ExecID),
			"exec_kind":    strings.TrimSpace(pendingExec.ExecKind),
			"message_id":   pendingExec.MessageID,
		})
	}
	if err := service.publishCheckpointForce(stream.RequestID, stream.ConversationID); err != nil {
		stream.mu.Lock()
		delete(stream.PendingExecs, pendingExec.ExecID)
		stream.mu.Unlock()
		service.updateNativeDelegationStatus(pendingExec.ExecID, delegation.TaskFailed, "Cursor 子代理启动状态同步失败", err.Error())
		return nil, runtimecore.PendingExec{}, err
	}
	log.Printf("forwarder native task pre-start checkpoint published request_id=%s tool_call_id=%s exec_id=%s", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pendingExec.ToolCallID), strings.TrimSpace(pendingExec.ExecID))
	return serverMessage, pendingExec, nil
}

func shouldSuppressStartedToolCallAfterPartial(stream *ActiveStream, toolName string, callID string) bool {
	if stream == nil {
		return false
	}
	switch strings.TrimSpace(toolName) {
	case "CreatePlan", "GenerateImage":
	default:
		return false
	}
	trimmedCallID := strings.TrimSpace(callID)
	if trimmedCallID == "" {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.PartialToolCallIDs == nil {
		return false
	}
	_, ok := stream.PartialToolCallIDs[trimmedCallID]
	return ok
}

func (service *Service) recordExecDispatchMetadata(stream *ActiveStream, pending runtimecore.PendingExec, buffered bool, startedEmitted bool, dispatchOrder string) {
	if service == nil || stream == nil {
		return
	}
	toolName := strings.TrimSpace(deriveToolNameFromPendingExec(pending))
	if _, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, "exec_dispatch", map[string]any{
			"tool_call_id":    pending.ToolCallID,
			"message_id":      pending.MessageID,
			"exec_id":         pending.ExecID,
			"exec_kind":       pending.ExecKind,
			"provider_pass":   pending.ProviderPass,
			"tool_name":       toolName,
			"model_call_id":   pending.ModelCallID,
			"buffered":        buffered,
			"started_emitted": startedEmitted,
			"dispatch_order":  strings.TrimSpace(dispatchOrder),
			"opened_at":       pending.OpenedAt,
		}),
	}); err != nil {
		log.Printf("forwarder exec dispatch metadata failed request_id=%s tool_call_id=%s message_id=%d err=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(pending.ToolCallID), pending.MessageID, err)
	}
}

// shouldBufferExecDispatch 把只需要完整参数的快工具改成“先发 exec 请求，再发 started，再发 checkpoint”，
// 避免客户端在参数仍未稳定前过早起计时，同时保留显式的工具开始信号。
func shouldBufferExecDispatch(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "Read", "Grep", "Glob":
		return true
	default:
		return false
	}
}

// appendToolResult 把已完成的工具结果追加到 history，供后续 prompt replay 使用。
//
// reasoning 在已提交 history 中应挂在 assistant_text / tool_call 上。
// tool_result 保存一份 reasoning_content 兜底，replay 只会在缺失 tool_call entry
// 且 reasoning 可回放时用它重建 assistant tool_use，不会把 thinking 复制到工具消息上。
func (service *Service) appendToolResult(stream *ActiveStream, toolCallID string, toolName string, argsJSON []byte, resultText string, reasoningContent string, toolCall *agentv1.ToolCall) error {
	if stream == nil {
		return nil
	}
	// B1 doom loop 提示注入：取走并清空待注入提示，非空时追加到工具结果末尾。
	stream.mu.Lock()
	notice := stream.pendingDoomLoopNotice
	stream.pendingDoomLoopNotice = ""
	stream.mu.Unlock()
	if notice != "" && strings.TrimSpace(resultText) != "" {
		resultText = strings.TrimSpace(resultText) + "\n" + notice
	} else if notice != "" {
		resultText = notice
	}
	var payload json.RawMessage
	if toolCall != nil {
		encoded, err := protojson.Marshal(toolCall)
		if err != nil {
			return err
		}
		payload = encoded
	}
	_, err := service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newToolResultEntry(stream.TurnSeq, stream.RequestID, toolCallID, toolName, string(argsJSON), resultText, reasoningContent, payload),
	})
	return err
}

func (service *Service) publishToolCallCompleted(requestID string, toolCallID string, modelCallID string, toolCall *agentv1.ToolCall) error {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(toolCallID) == "" {
		return nil
	}
	task := toolCall.GetTaskToolCall()
	resultKind := "none"
	agentID := ""
	stepCount := 0
	if task != nil && task.GetResult() != nil {
		switch task.GetResult().GetResult().(type) {
		case *agentv1.TaskResult_Success:
			resultKind = "success"
			if success := task.GetResult().GetSuccess(); success != nil {
				agentID = success.GetAgentId()
				stepCount = len(success.GetConversationSteps())
			}
		case *agentv1.TaskResult_Error:
			resultKind = "error"
		}
	}
	log.Printf("forwarder tool_call_completed publishing request_id=%s tool_call_id=%s model_call_id=%s task_result=%s agent_id=%s conversation_steps=%d", strings.TrimSpace(requestID), strings.TrimSpace(toolCallID), strings.TrimSpace(modelCallID), resultKind, strings.TrimSpace(agentID), stepCount)
	err := service.broker.Publish(requestID, StreamEvent{
		Message: buildToolCallCompletedMessage(toolCallID, modelCallID, toolCall),
	})
	if err != nil {
		log.Printf("forwarder tool_call_completed publish failed request_id=%s tool_call_id=%s model_call_id=%s err=%v", strings.TrimSpace(requestID), strings.TrimSpace(toolCallID), strings.TrimSpace(modelCallID), err)
	}
	return err
}

func (service *Service) applyExecProgress(stream *ActiveStream, pending runtimecore.PendingExec, message *agentv1.ExecClientMessage) runtimecore.PendingExec {
	if stream == nil || message == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return pending
	}
	shellStream := message.GetShellStream()
	if shellStream == nil {
		return pending
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	current, ok := stream.PendingExecs[pending.ExecID]
	if !ok {
		return pending
	}
	now := time.Now().UTC()
	switch event := shellStream.GetEvent().(type) {
	case *agentv1.ShellStream_Stdout:
		if current.FirstChunkAt.IsZero() {
			current.FirstChunkAt = now
		}
		current.ChunkCount++
		current.StreamState = "streaming"
		current.LastShellActivityAt = now
		current.StdoutBuffer += execbridge.DecodeShellStdout(event.Stdout)
	case *agentv1.ShellStream_Stderr:
		if current.FirstChunkAt.IsZero() {
			current.FirstChunkAt = now
		}
		current.ChunkCount++
		current.StreamState = "streaming"
		current.LastShellActivityAt = now
		current.StderrBuffer += event.Stderr.GetData()
	case *agentv1.ShellStream_Start:
		if current.FirstChunkAt.IsZero() {
			current.FirstChunkAt = now
		}
		current.StreamState = "started"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_Backgrounded:
		current.StreamState = "backgrounded"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_Exit:
		current.StreamState = "exited"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_Rejected:
		current.StreamState = "rejected"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_PermissionDenied:
		current.StreamState = "permission_denied"
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_HookContext:
		// hook 附加上下文出现在 shell 开始阶段，不改 StreamState（保留 opened/started 原值），
		// 仅续期 LastShellActivityAt，避免污染 observeShellStreamClose 的状态判断。
		current.LastShellActivityAt = now
	case *agentv1.ShellStream_SandboxUnsupported:
		current.StreamState = "sandbox_unsupported"
		current.LastShellActivityAt = now
	}
	stream.PendingExecs[pending.ExecID] = current
	return current
}

func (service *Service) applyExecControlProgress(stream *ActiveStream, pending runtimecore.PendingExec, message *agentv1.ExecClientControlMessage) runtimecore.PendingExec {
	if stream == nil || message == nil || strings.TrimSpace(pending.ExecKind) != "shell" {
		return pending
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	current, ok := stream.PendingExecs[pending.ExecID]
	if !ok {
		return pending
	}
	now := time.Now().UTC()
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_Heartbeat:
		current.LastShellActivityAt = now
		current.LastShellHeartbeatAt = now
	case *agentv1.ExecClientControlMessage_StreamClose:
		current.LastShellActivityAt = now
	case *agentv1.ExecClientControlMessage_Throw:
		current.LastShellActivityAt = now
		current.StreamState = "throw"
	}
	stream.PendingExecs[pending.ExecID] = current
	return current
}

// closeStreamWithProviderError 在真实 LLM/provider 出错时通过 RunSSE 传回错误，并正常结束流。
func (service *Service) closeStreamWithProviderError(
	stream *ActiveStream,
	conversationID string,
	turnSeq int64,
	requestID string,
	accumulatedText string,
	accumulatedReasoning string,
	accumulatedReasoningSignature string,
	accumulatedReasoningSignatureSource string,
	accumulatedReasoningItemID string,
	accumulatedReasoningStatus string,
	accumulatedReasoningSummary json.RawMessage,
	usage turnUsageSnapshot,
	providerErr providerTerminalError,
	allowReasoningOnly bool,
) error {
	if stream == nil {
		return nil
	}
	errorText := strings.TrimSpace(providerErr.Error())
	if errorText == "" {
		errorText = "provider error"
	}
	if strings.TrimSpace(usage.ErrorCode) == "" {
		if code := extractUsageErrorCodeFromCause(providerErr); code != "" {
			usage.ErrorCode = code
		} else if code := extractUsageErrorCode(errorText); code != "" {
			usage.ErrorCode = code
		} else {
			usage.ErrorCode = "provider_error"
		}
	}
	modelCallID := strings.TrimSpace(stream.CurrentModelCallID)
	if err := service.flushAssistantText(stream, conversationID, turnSeq, requestID, accumulatedText, accumulatedReasoning, accumulatedReasoningSignature, accumulatedReasoningSignatureSource, accumulatedReasoningItemID, accumulatedReasoningStatus, accumulatedReasoningSummary, allowReasoningOnly); err != nil {
		return fmt.Errorf("flush provider error assistant output: %w", err)
	}
	if err := service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, "provider_error", usage, errorText, false); err != nil {
		return fmt.Errorf("record provider error usage: %w", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, "provider_error", map[string]any{
			"model_call_id": modelCallID,
			"error":         errorText,
		}),
	}); err != nil {
		return err
	}
	if err := service.recordTurnFinalizedSnapshot(stream, conversationID, turnSeq, requestID, "provider_error", errorText); err != nil {
		return fmt.Errorf("record provider error turn finalized: %w", err)
	}
	if err := service.updateConversationTokenState(stream, conversationID, usage, modelCallID, false); err != nil {
		return err
	}
	return service.failActiveStream(stream, conversationID, requestID, modelCallID, "provider_error", errorText)
}

func takePendingProviderCompletion(stream *ActiveStream) (pendingTurnCompletion, bool) {
	if stream == nil {
		return pendingTurnCompletion{}, false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.PendingProviderCompletion == nil {
		return pendingTurnCompletion{}, false
	}
	completion := *stream.PendingProviderCompletion
	stream.PendingProviderCompletion = nil
	stream.UpdatedAt = time.Now().UTC()
	return completion, true
}

func pendingBridgeCount(stream *ActiveStream) int {
	if stream == nil {
		return 0
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return len(stream.PendingExecs) + len(stream.PendingInteractions)
}

func (service *Service) finishDeferredTurnAfterInteraction(stream *ActiveStream, pending runtimecore.PendingInteraction) error {
	completion, ok := takePendingProviderCompletion(stream)
	if !ok {
		stream.mu.Lock()
		completion = pendingTurnCompletion{
			ConversationID: stream.ConversationID,
			RequestID:      stream.RequestID,
			TurnSeq:        stream.TurnSeq,
			ModelCallID:    firstNonEmpty(strings.TrimSpace(pending.ModelCallID), strings.TrimSpace(stream.CurrentModelCallID)),
			ProviderPass:   pending.ProviderPass,
		}
		stream.mu.Unlock()
		log.Printf(
			"forwarder missing deferred turn completion snapshot request_id=%s tool_call_id=%s interaction_kind=%s provider_pass=%d",
			strings.TrimSpace(completion.RequestID),
			strings.TrimSpace(pending.ToolCallID),
			strings.TrimSpace(pending.InteractionKind),
			pending.ProviderPass,
		)
	}
	if strings.TrimSpace(completion.ModelCallID) == "" {
		completion.ModelCallID = strings.TrimSpace(pending.ModelCallID)
	}
	if completion.ProviderPass == 0 {
		completion.ProviderPass = pending.ProviderPass
	}
	return service.completeSuccessfulTurn(stream, completion)
}

func (service *Service) completeSuccessfulTurn(stream *ActiveStream, completion pendingTurnCompletion) error {
	if stream == nil {
		return nil
	}
	requestID := firstNonEmpty(strings.TrimSpace(completion.RequestID), strings.TrimSpace(stream.RequestID))
	conversationID := firstNonEmpty(strings.TrimSpace(completion.ConversationID), strings.TrimSpace(stream.ConversationID))
	modelCallID := firstNonEmpty(strings.TrimSpace(completion.ModelCallID), strings.TrimSpace(stream.CurrentModelCallID))
	turnSeq := completion.TurnSeq
	if turnSeq <= 0 {
		turnSeq = stream.TurnSeq
	}
	service.clearProvider400Recovery(requestID, turnSeq)
	usage := completion.Usage
	if err := service.recordTurnUsageSnapshot(stream, conversationID, turnSeq, requestID, modelCallID, "completed", usage, "", false); err != nil {
		return fmt.Errorf("record completed turn usage: %w", err)
	}
	if _, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newMetadataEntry(turnSeq, requestID, "turn_completed", map[string]any{
			"model_call_id": modelCallID,
		}),
	}); err != nil {
		return err
	}
	if err := service.recordTurnFinalizedSnapshot(stream, conversationID, turnSeq, requestID, "completed", ""); err != nil {
		return fmt.Errorf("record completed turn finalized: %w", err)
	}
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil {
		log.Printf(
			"forwarder summary sync after turn completion failed request_id=%s model_call_id=%s err=%v",
			strings.TrimSpace(requestID),
			strings.TrimSpace(modelCallID),
			err,
		)
	}
	if err := service.publishCheckpointWithCompletion(requestID, conversationID, &completion); err != nil {
		return err
	}
	return nil
}

func (service *Service) finishSuccessfulTurnAfterCheckpoint(stream *ActiveStream, completion pendingTurnCompletion) error {
	if stream == nil {
		return nil
	}
	requestID := firstNonEmpty(strings.TrimSpace(completion.RequestID), strings.TrimSpace(stream.RequestID))
	usage := completion.Usage
	if err := service.broker.Publish(requestID, StreamEvent{
		Message: buildTurnEndedMessage(usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens),
	}); err != nil {
		return err
	}
	if err := service.broker.Complete(requestID, "", ""); err != nil {
		return err
	}
	service.setTurnPhase(stream, TurnPhaseCompleted)
	// 当前 turn 终态后，排空该会话因「子代理运行期间」排队的新消息。
	service.drainRunQueue(stream.ConversationID)
	return nil
}

func (service *Service) failStreamIfNonTerminal(stream *ActiveStream, terminalCode string, cause error) error {
	if stream == nil || cause == nil {
		return nil
	}
	stream.mu.Lock()
	terminal := isTerminalStreamStatus(stream.Status)
	stream.mu.Unlock()
	if terminal {
		log.Printf("forwarder fail_stream_if_non_terminal skipped request_id=%s terminal_code=%s cause=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(terminalCode), cause)
		return nil
	}
	log.Printf("forwarder fail_stream_if_non_terminal firing request_id=%s terminal_code=%s cause=%v", strings.TrimSpace(stream.RequestID), strings.TrimSpace(terminalCode), cause)
	return service.failStream(stream, terminalCode, cause)
}

// publishCheckpoint projects the in-memory conversation and broadcasts a legacy checkpoint.
// Ordinary snapshots are deduplicated and rate-limited; terminal snapshots bypass that gate
// so completion, cancellation, and delegation state cannot remain stale in Cursor.
func (service *Service) publishCheckpoint(requestID string, conversationID string) error {
	return service.publishCheckpointWithOptions(requestID, conversationID, false)
}

func (service *Service) publishCheckpointWithCompletion(requestID string, conversationID string, completion *pendingTurnCompletion) error {
	return service.publishCheckpointWithTerminalAction(requestID, conversationID, checkpointCompletionAction(completion))
}

func (service *Service) publishCheckpointWithTerminalAction(requestID string, conversationID string, terminalAction checkpointTerminalAction) error {
	return service.publishCheckpointWithOptionsAndAction(requestID, conversationID, terminalAction, true)
}

func (service *Service) publishCheckpointForce(requestID string, conversationID string) error {
	return service.publishCheckpointWithOptions(requestID, conversationID, true)
}

// publishExecCheckpoint keeps ordinary tool completions coalesced while task-like
// executions are flushed immediately so their Cursor status reflects the backend.
func (service *Service) publishExecCheckpoint(stream *ActiveStream, pending runtimecore.PendingExec) error {
	if stream == nil {
		return nil
	}
	execKind := strings.TrimSpace(pending.ExecKind)
	force := execKind == "subagent" || execKind == "delegation_aggregate"
	return service.publishCheckpointWithOptions(stream.RequestID, stream.ConversationID, force)
}

func (service *Service) publishCheckpointWithOptions(requestID string, conversationID string, force bool) error {
	return service.publishCheckpointWithOptionsAndAction(requestID, conversationID, checkpointTerminalAction{kind: checkpointTerminalActionNone}, force)
}

func (service *Service) publishCheckpointWithOptionsAndAction(requestID string, _ string, terminalAction checkpointTerminalAction, force bool) error {
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return fmt.Errorf("request is not active: %s", requestID)
	}
	conversation, pendingExecs, pendingInteractions, err := service.snapshotCheckpointConversation(stream)
	if err != nil {
		return err
	}
	projection, err := service.projector.ProjectCheckpointProjection(conversation)
	if err != nil {
		return err
	}
	if projection == nil || projection.State == nil {
		return fmt.Errorf("checkpoint projection is empty")
	}
	projection.State.PendingToolCalls = buildPendingToolCalls(pendingExecs, pendingInteractions)
	service.rewriteCheckpointTokenDetailsForClient(stream, conversation, projection.State)
	attachDelegationRunStates(stream, projection.State)
	delegationRunCount := len(projection.State.GetSubagentRunsByParentToolCallId())
	if delegationRunCount > 0 {
		activeDelegationRuns := 0
		terminalDelegationRuns := 0
		for _, run := range projection.State.GetSubagentRunsByParentToolCallId() {
			if run == nil {
				continue
			}
			switch run.GetStatus() {
			case agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_RUNNING, agentv1.SubagentRunStatus_SUBAGENT_RUN_STATUS_BACKGROUNDED:
				activeDelegationRuns++
			default:
				terminalDelegationRuns++
			}
		}
		log.Printf("forwarder delegation checkpoint publishing request_id=%s conversation_id=%s active_runs=%d terminal_runs=%d pending_execs=%d pending_interactions=%d",
			strings.TrimSpace(requestID), strings.TrimSpace(stream.ConversationID), activeDelegationRuns, terminalDelegationRuns, len(pendingExecs), len(pendingInteractions))
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "delegation_checkpoint_publishing", map[string]any{
				"active_run_count":          activeDelegationRuns,
				"terminal_run_count":        terminalDelegationRuns,
				"pending_exec_count":        len(pendingExecs),
				"pending_interaction_count": len(pendingInteractions),
			})
		}
	}
	if delegationRunCount > 0 {
		for key, run := range projection.State.GetSubagentRunsByParentToolCallId() {
			if run == nil {
				continue
			}
			log.Printf("forwarder delegation checkpoint run request_id=%s map_key=%s parent_tool_call_id=%s subagent_id=%s status=%s env=%s", strings.TrimSpace(requestID), strings.TrimSpace(key), strings.TrimSpace(run.GetParentToolCallId()), strings.TrimSpace(run.GetSubagentId()), run.GetStatus().String(), run.GetEnvironment().String())
		}
	}
	message := buildCheckpointMessage(projection.State)
	wireSize := proto.Size(message)
	wireBytes, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if marshalErr != nil {
		return fmt.Errorf("marshal checkpoint for dedupe: %w", marshalErr)
	}
	wireHash := fmt.Sprintf("%x", sha256.Sum256(wireBytes))
	now := time.Now().UTC()
	stream.mu.Lock()
	lastHash := stream.LastCheckpointWireHash
	lastSentAt := stream.LastCheckpointSentAt
	if !force && wireHash == lastHash {
		if stream.CheckpointPublishTimer != nil {
			stream.CheckpointPublishTimer.Stop()
			stream.CheckpointPublishTimer = nil
		}
		stream.CheckpointPublishPending = false
		stream.mu.Unlock()
		log.Printf("forwarder checkpoint skipped request_id=%s reason=duplicate hash=%s wire_size=%d", strings.TrimSpace(requestID), wireHash[:12], wireSize)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "checkpoint_skipped_duplicate", map[string]any{
				"wire_hash": wireHash, "wire_size": wireSize, "pending_exec_count": len(pendingExecs),
			})
		}
		return nil
	}
	if !force && !lastSentAt.IsZero() && now.Sub(lastSentAt) < checkpointMinSendInterval {
		remaining := checkpointMinSendInterval - now.Sub(lastSentAt)
		if stream.CheckpointPublishTimer == nil {
			stream.CheckpointPublishPending = true
			stream.CheckpointPublishTimer = time.AfterFunc(remaining, func() {
				if delayedStream, delayedOK := service.broker.Get(requestID); delayedOK && delayedStream != nil {
					delayedStream.mu.Lock()
					delayedStream.CheckpointPublishTimer = nil
					delayedStream.CheckpointPublishPending = false
					delayedStream.mu.Unlock()
				}
				if service.debug != nil {
					service.debug.LogRuntime(context.Background(), requestID, "", "checkpoint_delayed_publish_fired", map[string]any{
						"delay": remaining.String(),
					})
				}
				if err := service.publishCheckpoint(requestID, ""); err != nil {
					log.Printf("forwarder checkpoint delayed publish failed request_id=%s err=%v", strings.TrimSpace(requestID), err)
				}
			})
		}
		stream.mu.Unlock()
		log.Printf("forwarder checkpoint skipped request_id=%s reason=rate_limited hash=%s wire_size=%d elapsed=%s min_interval=%s", strings.TrimSpace(requestID), wireHash[:12], wireSize, now.Sub(lastSentAt).Round(time.Millisecond), checkpointMinSendInterval)
		if service.debug != nil {
			service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "checkpoint_skipped_rate_limited", map[string]any{
				"wire_hash": wireHash, "wire_size": wireSize, "elapsed_since_last": now.Sub(lastSentAt).String(), "min_interval": checkpointMinSendInterval.String(), "pending_exec_count": len(pendingExecs),
			})
		}
		return nil
	}
	stream.LastCheckpointWireHash = wireHash
	stream.LastCheckpointSentAt = now
	stream.CheckpointPublishPending = false
	if stream.CheckpointPublishTimer != nil {
		stream.CheckpointPublishTimer.Stop()
		stream.CheckpointPublishTimer = nil
	}
	stream.mu.Unlock()
	log.Printf("forwarder checkpoint queued request_id=%s hash=%s wire_size=%d force=%t pending_execs=%d pending_interactions=%d", strings.TrimSpace(requestID), wireHash[:12], wireSize, force, len(pendingExecs), len(pendingInteractions))
	if service.debug != nil {
		service.debug.LogRuntime(context.Background(), requestID, stream.ConversationID, "checkpoint_queued", map[string]any{
			"wire_hash": wireHash, "wire_size": wireSize, "force": force, "pending_exec_count": len(pendingExecs), "pending_interaction_count": len(pendingInteractions),
		})
	}
	return service.queueCheckpointProjection(stream, projection, terminalAction)
}

// flushAssistantText 把本轮累计的 assistant 文本一次性写回 history。
func (service *Service) flushAssistantText(stream *ActiveStream, conversationID string, turnSeq int64, requestID string, text string, reasoningContent string, reasoningSignature string, reasoningSignatureSource string, reasoningItemID string, reasoningStatus string, reasoningSummary json.RawMessage, allowReasoningOnly bool) error {
	if strings.TrimSpace(text) == "" && (!allowReasoningOnly || !hasReplayableReasoningPayload(reasoningContent, reasoningSignature, reasoningSignatureSource)) {
		return nil
	}
	_, err := service.appendConversationEntries(stream, conversationID, []HistoryEntry{
		newAssistantTextEntryWithProviderMetadata(turnSeq, requestID, text, reasoningContent, reasoningSignature, reasoningSignatureSource, reasoningItemID, reasoningStatus, reasoningSummary),
	})
	return err
}

// failStream 在 provider 或投影失败时把错误写入 history 并收口活动流。
func (service *Service) failStream(stream *ActiveStream, terminalCode string, cause error) error {
	if stream == nil {
		return nil
	}
	errorText := "unknown error"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		errorText = strings.TrimSpace(cause.Error())
	}
	resolvedTerminalCode := resolveTerminalCode(terminalCode, cause)
	metadataType := "failed"
	var providerErr providerTerminalError
	if errors.As(cause, &providerErr) || resolvedTerminalCode == "provider_error" {
		metadataType = "provider_error"
	}
	_, _ = service.appendConversationEntries(stream, stream.ConversationID, []HistoryEntry{
		newMetadataEntry(stream.TurnSeq, stream.RequestID, metadataType, map[string]any{
			"error": errorText,
		}),
	})
	return service.failActiveStream(
		stream,
		stream.ConversationID,
		stream.RequestID,
		stream.CurrentModelCallID,
		resolvedTerminalCode,
		errorText,
	)
}

func resolveTerminalCode(fallback string, cause error) string {
	terminalCode := firstNonEmpty(strings.TrimSpace(fallback), "unknown")
	if cause == nil || terminalCode != "unknown" {
		return terminalCode
	}
	var coded interface{ TerminalCode() string }
	if errors.As(cause, &coded) && strings.TrimSpace(coded.TerminalCode()) != "" {
		return strings.TrimSpace(coded.TerminalCode())
	}
	return terminalCode
}

func (service *Service) failActiveStream(stream *ActiveStream, conversationID string, requestID string, modelCallID string, terminalCode string, terminalMessage string) error {
	if stream == nil {
		return nil
	}
	stream.mu.Lock()
	activePending := len(stream.PendingExecs)
	phase := stream.Phase
	status := stream.Status
	stream.mu.Unlock()
	log.Printf("forwarder fail_active_stream request_id=%s conversation_id=%s model_call_id=%s terminal_code=%s phase=%s status=%s pending_execs=%d message=%q", strings.TrimSpace(requestID), strings.TrimSpace(conversationID), strings.TrimSpace(modelCallID), strings.TrimSpace(terminalCode), phase, status, activePending, strings.TrimSpace(terminalMessage))
	service.clearProvider400Recovery(requestID, stream.TurnSeq)
	clearPendingProviderCompletion(stream)
	stream.mu.Lock()
	cancel := stream.ProviderCancel
	stream.ProviderActive = false
	stream.ProviderCancel = nil
	stream.PendingProviderAction = providerActionNone
	stream.UpdatedAt = time.Now().UTC()
	stream.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if service.multitaskDelegation != nil {
		service.multitaskDelegation.CancelStream(stream)
	}
	service.setTurnPhase(stream, TurnPhaseFailed)
	var firstErr error
	if err := service.syncSummaryCarryForward(conversationID, requestID, modelCallID); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := service.publishCheckpointForce(requestID, conversationID); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := service.broker.Fail(requestID, terminalCode, terminalMessage); err != nil && firstErr == nil {
		firstErr = err
	}
	// 当前 turn 终态后，排空该会话因「子代理运行期间」排队的新消息。
	service.drainRunQueue(conversationID)
	return firstErr
}

func provider400RecoveryKey(requestID string, turnSeq int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", requestID, turnSeq)
}

func (service *Service) claimProvider400Recovery(requestID string, turnSeq int64) bool {
	if service == nil {
		return false
	}
	key := provider400RecoveryKey(requestID, turnSeq)
	if key == "" {
		return false
	}
	service.provider400RecoveryMu.Lock()
	defer service.provider400RecoveryMu.Unlock()
	if service.provider400RecoveryTurns == nil {
		service.provider400RecoveryTurns = make(map[string]struct{})
	}
	if _, exists := service.provider400RecoveryTurns[key]; exists {
		return false
	}
	service.provider400RecoveryTurns[key] = struct{}{}
	return true
}

func (service *Service) clearProvider400Recovery(requestID string, turnSeq int64) {
	if service == nil {
		return
	}
	key := provider400RecoveryKey(requestID, turnSeq)
	if key == "" {
		return
	}
	service.provider400RecoveryMu.Lock()
	delete(service.provider400RecoveryTurns, key)
	service.provider400RecoveryMu.Unlock()
}

// extractUserMessage 从 legacy run_request 中提取用户消息。
func extractUserMessage(message *agentv1.AgentClientMessage) *agentv1.UserMessage {
	if message == nil || message.GetRunRequest() == nil || message.GetRunRequest().GetAction() == nil {
		return nil
	}
	switch item := message.GetRunRequest().GetAction().GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetUserMessage()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetUserMessage()
	default:
		return nil
	}
}

// extractRequestContext 从 legacy 请求中提取 request_context。
func extractRequestContext(message *agentv1.AgentClientMessage) *agentv1.RequestContext {
	if message == nil || message.GetRunRequest() == nil || message.GetRunRequest().GetAction() == nil {
		return nil
	}
	switch item := message.GetRunRequest().GetAction().GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetRequestContext()
	case *agentv1.ConversationAction_ResumeAction:
		return item.ResumeAction.GetRequestContext()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetRequestContext()
	case *agentv1.ConversationAction_ExecutePlanAction:
		return item.ExecutePlanAction.GetRequestContext()
	default:
		return nil
	}
}

func (service *Service) shouldIgnoreEmptyResumeRunRequest(requestID string, runRequest *agentv1.AgentRunRequest, userMessage *agentv1.UserMessage, requestContext *agentv1.RequestContext) bool {
	if runRequest == nil || !conversationActionIsResume(runRequest.GetAction()) {
		return false
	}
	if userMessage != nil || requestContextHasPayload(requestContext) {
		return false
	}
	state := runRequest.GetConversationState()
	if state != nil && len(state.GetPendingToolCalls()) > 0 {
		return false
	}
	conversationID := strings.TrimSpace(runRequest.GetConversationId())
	if conversationID == "" || service.hasActiveConversationStream(conversationID, requestID) {
		return false
	}
	conversation, err := service.loadConversationForResumeGuard(conversationID)
	if err != nil || conversation == nil {
		return false
	}
	return emptyResumeCanBeIgnoredForConversation(conversation)
}

func requestContextHasPayload(requestContext *agentv1.RequestContext) bool {
	return requestContext != nil && proto.Size(requestContext) > 0
}

func (service *Service) loadConversationForResumeGuard(conversationID string) (*ConversationFile, error) {
	if service == nil || service.store == nil {
		return nil, nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, nil
	}
	return service.store.LoadConversation(conversationID)
}

func (service *Service) hasActiveConversationStream(conversationID string, requestID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if service == nil || service.broker == nil || conversationID == "" {
		return false
	}
	if len(service.broker.OtherConversationRequestIDs(conversationID, requestID)) > 0 {
		return true
	}
	stream, ok := service.broker.Get(requestID)
	if !ok || stream == nil {
		return false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if strings.TrimSpace(stream.ConversationID) != conversationID {
		return false
	}
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

func emptyResumeCanBeIgnoredForConversation(conversation *ConversationFile) bool {
	if conversation == nil {
		return false
	}
	status := strings.TrimSpace(conversation.CurrentLoopStatus)
	currentRequestID := strings.TrimSpace(conversation.CurrentRequestID)
	if status == "" {
		return currentRequestID == ""
	}
	switch status {
	case "completed", "idle":
		return true
	default:
		return false
	}
}

func extractConversationActionUserMessage(action *agentv1.ConversationAction) *agentv1.UserMessage {
	if action == nil {
		return nil
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetUserMessage()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetUserMessage()
	default:
		return nil
	}
}

func extractConversationActionRequestContext(action *agentv1.ConversationAction) *agentv1.RequestContext {
	if action == nil {
		return nil
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction:
		return item.UserMessageAction.GetRequestContext()
	case *agentv1.ConversationAction_ResumeAction:
		return item.ResumeAction.GetRequestContext()
	case *agentv1.ConversationAction_StartPlanAction:
		return item.StartPlanAction.GetRequestContext()
	case *agentv1.ConversationAction_ExecutePlanAction:
		return item.ExecutePlanAction.GetRequestContext()
	default:
		return nil
	}
}

func conversationActionIsResume(action *agentv1.ConversationAction) bool {
	if action == nil {
		return false
	}
	_, ok := action.GetAction().(*agentv1.ConversationAction_ResumeAction)
	return ok
}

func inboundConversationAction(message *agentv1.AgentClientMessage) *agentv1.ConversationAction {
	if message == nil {
		return nil
	}
	if action := message.GetConversationAction(); action != nil {
		return action
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return runRequest.GetAction()
	}
	return nil
}

func conversationActionIsSummarize(action *agentv1.ConversationAction) bool {
	if action == nil {
		return false
	}
	_, ok := action.GetAction().(*agentv1.ConversationAction_SummarizeAction)
	return ok
}

func resolveInboundManualCompaction(message *agentv1.AgentClientMessage, userMessage *agentv1.UserMessage) manualCompactionDirective {
	instruction, requested := parseManualCompactionRequest(userMessage)
	if conversationActionIsSummarize(inboundConversationAction(message)) {
		requested = true
	}
	return manualCompactionDirective{
		Requested:   requested,
		Instruction: instruction,
	}
}

func conversationActionStartsRun(action *agentv1.ConversationAction) bool {
	if action == nil {
		return false
	}
	switch action.GetAction().(type) {
	case *agentv1.ConversationAction_UserMessageAction,
		*agentv1.ConversationAction_ResumeAction,
		*agentv1.ConversationAction_SummarizeAction,
		*agentv1.ConversationAction_StartPlanAction,
		*agentv1.ConversationAction_ExecutePlanAction:
		return true
	default:
		return false
	}
}

// extractRunMode 推导本轮应使用的 mode。
func extractRunMode(message *agentv1.AgentClientMessage) (agentv1.AgentMode, ModeSource, bool, error) {
	if message != nil && message.GetRunRequest() != nil && message.GetRunRequest().GetAction() != nil {
		switch item := message.GetRunRequest().GetAction().GetAction().(type) {
		case *agentv1.ConversationAction_StartPlanAction:
			return resolveExplicitMode(agentv1.AgentMode_AGENT_MODE_PLAN, ModeSourceStartPlanAction)
		case *agentv1.ConversationAction_ExecutePlanAction:
			mode := agentv1.AgentMode_AGENT_MODE_AGENT
			if item.ExecutePlanAction != nil && item.ExecutePlanAction.GetExecutionMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
				mode = item.ExecutePlanAction.GetExecutionMode()
			}
			return resolveExplicitMode(mode, ModeSourceExecutePlanAction)
		}
	}
	if userMessage := extractUserMessage(message); userMessage != nil && userMessage.GetMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return resolveExplicitMode(userMessage.GetMode(), ModeSourceUserMessage)
	}
	if message != nil && message.GetRunRequest() != nil && message.GetRunRequest().GetConversationState() != nil {
		if mode := message.GetRunRequest().GetConversationState().GetMode(); mode != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
			return resolveExplicitMode(mode, ModeSourceConversationState)
		}
	}
	return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
}

func extractPrewarmMode(request *agentv1.PrewarmRequest) (agentv1.AgentMode, ModeSource, bool, error) {
	if request == nil || request.GetConversationState() == nil {
		return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
	}
	mode := request.GetConversationState().GetMode()
	if mode == agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
	}
	return resolveExplicitMode(mode, ModeSourceConversationState)
}

func extractConversationActionMode(action *agentv1.ConversationAction) (agentv1.AgentMode, ModeSource, bool, error) {
	if action == nil {
		return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
	}
	switch item := action.GetAction().(type) {
	case *agentv1.ConversationAction_StartPlanAction:
		return resolveExplicitMode(agentv1.AgentMode_AGENT_MODE_PLAN, ModeSourceStartPlanAction)
	case *agentv1.ConversationAction_ExecutePlanAction:
		mode := agentv1.AgentMode_AGENT_MODE_AGENT
		if item.ExecutePlanAction != nil && item.ExecutePlanAction.GetExecutionMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
			mode = item.ExecutePlanAction.GetExecutionMode()
		}
		return resolveExplicitMode(mode, ModeSourceExecutePlanAction)
	}
	if userMessage := extractConversationActionUserMessage(action); userMessage != nil && userMessage.GetMode() != agentv1.AgentMode_AGENT_MODE_UNSPECIFIED {
		return resolveExplicitMode(userMessage.GetMode(), ModeSourceUserMessage)
	}
	return agentv1.AgentMode_AGENT_MODE_AGENT, ModeSourceUnknown, false, nil
}

// extractRequestedModelID 提取本轮显式请求的模型 ID。
func extractRequestedModelID(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return firstNonEmpty(extractRequestedModelIDFromRequestedModel(runRequest.GetRequestedModel()), runRequest.GetModelDetails().GetModelId())
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		return firstNonEmpty(extractRequestedModelIDFromRequestedModel(prewarm.GetRequestedModel()), prewarm.GetModelDetails().GetModelId())
	}
	return ""
}

func extractRequestedModelIDFromRequestedModel(model *agentv1.RequestedModel) string {
	if model == nil {
		return ""
	}
	if model.GetIsVariantStringRepresentation() {
		modelID, _ := splitRuntimeThinkingEffortVariantString(model.GetModelId())
		if modelID != "" {
			return modelID
		}
		// variant 拆分失败（如 hash 格式的 channel ID 无冒号）时，
		// 回退到原始 model_id，避免丢失用户选择的模型
		return strings.TrimSpace(model.GetModelId())
	}
	return strings.TrimSpace(model.GetModelId())
}

func extractRuntimeThinkingEffort(message *agentv1.AgentClientMessage) string {
	if message == nil {
		return ""
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		return extractRuntimeThinkingEffortFromRequestedModel(runRequest.GetRequestedModel())
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		return extractRuntimeThinkingEffortFromRequestedModel(prewarm.GetRequestedModel())
	}
	return ""
}

func extractRuntimeThinkingEffortFromRequestedModel(model *agentv1.RequestedModel) string {
	if model == nil {
		return ""
	}
	for _, parameter := range model.GetParameters() {
		if parameter == nil || !isRuntimeThinkingEffortParameterID(parameter.GetId()) {
			continue
		}
		if effort := normalizeRuntimeThinkingEffort(parameter.GetValue()); effort != "" {
			return effort
		}
	}
	if model.GetIsVariantStringRepresentation() {
		if _, effort := splitRuntimeThinkingEffortVariantString(model.GetModelId()); effort != "" {
			return effort
		}
		return normalizeRuntimeThinkingEffort(model.GetModelId())
	}
	return ""
}

// extractRequestedMaxMode 提取本轮请求的 max_mode 开关。
func extractRequestedMaxMode(message *agentv1.AgentClientMessage) bool {
	if message == nil {
		return false
	}
	if runRequest := message.GetRunRequest(); runRequest != nil {
		if model := runRequest.GetRequestedModel(); model != nil {
			return model.GetMaxMode()
		}
	}
	if prewarm := message.GetPrewarmRequest(); prewarm != nil {
		if model := prewarm.GetRequestedModel(); model != nil {
			return model.GetMaxMode()
		}
	}
	return false
}

func isRuntimeThinkingEffortParameterID(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case runtimeThinkingEffortParameterID,
		"reasoning",
		"reasoning_effort",
		"thinking_intensity",
		"anthropic_thinking_effort",
		"openai_reasoning_effort":
		return true
	default:
		return false
	}
}

func normalizeRuntimeThinkingEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "disabled", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(raw))
	case "disable", "off", "none", "false", "no", "0":
		return "disabled"
	case "very_high", "very-high", "veryhigh", "x-high", "extra_high", "extra-high", "extrahigh":
		return "xhigh"
	case "maximum":
		return "max"
	default:
		return ""
	}
}

func splitRuntimeThinkingEffortVariantString(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", ""
	}
	if effort := normalizeRuntimeThinkingEffort(text); effort != "" {
		return "", effort
	}
	index := strings.LastIndex(text, ":")
	if index <= 0 || index >= len(text)-1 {
		return "", ""
	}
	modelID := strings.TrimSpace(text[:index])
	effort := normalizeRuntimeThinkingEffort(text[index+1:])
	if modelID == "" || effort == "" {
		return "", ""
	}
	return modelID, effort
}

func (service *Service) resolveRequestedModelName(message *agentv1.AgentClientMessage, modelID string) string {
	if message != nil {
		if runRequest := message.GetRunRequest(); runRequest != nil {
			if name := firstNonEmpty(
				runRequest.GetModelDetails().GetDisplayName(),
				runRequest.GetModelDetails().GetDisplayModelId(),
			); name != "" {
				return name
			}
		}
		if prewarm := message.GetPrewarmRequest(); prewarm != nil {
			if name := firstNonEmpty(
				prewarm.GetModelDetails().GetDisplayName(),
				prewarm.GetModelDetails().GetDisplayModelId(),
			); name != "" {
				return name
			}
		}
	}
	if service != nil && service.resolver != nil {
		channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
		if err == nil && channel != nil {
			if name := firstNonEmpty(channel.Name, channel.Model); name != "" {
				return name
			}
		}
	}
	return strings.TrimSpace(modelID)
}

func (service *Service) resolveContextWindowTokens(modelID string) uint32 {
	if service == nil || service.resolver == nil {
		return projectedConversationMaxTokens
	}
	channel, err := service.resolver.SelectChannelForModel(context.Background(), strings.TrimSpace(modelID))
	if err != nil || channel == nil || channel.ContextWindowTokens <= 0 {
		return projectedConversationMaxTokens
	}
	return clampInt64ToUint32(int64(channel.ContextWindowTokens))
}

func (service *Service) syncConversationContextWindowTokens(stream *ActiveStream, conversationID string, conversation *ConversationFile) (*ConversationFile, error) {
	if stream == nil || conversation == nil {
		return conversation, nil
	}
	stream.mu.Lock()
	modelID := stream.ModelID
	stream.mu.Unlock()
	target := service.resolveContextWindowTokens(modelID)
	if target == 0 || conversation.TokenDetailsMaxTokens == target {
		return conversation, nil
	}
	return service.updateConversationMetaAndCheckpoint(stream, conversationID, func(item *ConversationFile) error {
		if item == nil {
			return nil
		}
		item.TokenDetailsMaxTokens = target
		return nil
	})
}

// userMessageText 返回用户消息中的纯文本。
func userMessageText(message *agentv1.UserMessage) string {
	if message == nil {
		return ""
	}
	return strings.TrimSpace(message.GetText())
}

func currentProviderPass(stream *ActiveStream) int {
	if stream == nil {
		return 0
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.ProviderPassCount
}

func currentStreamMode(stream *ActiveStream) agentv1.AgentMode {
	if stream == nil {
		return agentv1.AgentMode_AGENT_MODE_AGENT
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if normalized, err := validateSupportedActiveMode(stream.Mode); err == nil {
		return normalized
	}
	return stream.Mode
}

// selectPendingExec 按 exec_id 或 message_id 在当前流里查找挂起执行桥。
func selectPendingExec(execID string, messageID uint32, stream *ActiveStream) (runtimecore.PendingExec, bool) {
	if stream == nil {
		return runtimecore.PendingExec{}, false
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	normalizedExecID := strings.TrimSpace(execID)
	if normalizedExecID != "" {
		if item, ok := stream.PendingExecs[normalizedExecID]; ok {
			// exec_id is the stable execution identity. The bridge message id is
			// transport metadata and may be zero or reassigned by the client.
			return item, true
		}
		return runtimecore.PendingExec{}, false
	}
	if messageID != 0 {
		for _, item := range stream.PendingExecs {
			if item.MessageID == messageID {
				return item, true
			}
		}
	}
	return runtimecore.PendingExec{}, false
}

func selectPendingInteraction(message *agentv1.InteractionResponse, stream *ActiveStream) (runtimecore.PendingInteraction, bool) {
	if stream == nil || message == nil {
		return runtimecore.PendingInteraction{}, false
	}
	interactionID := fmt.Sprintf("%d", message.GetId())
	stream.mu.Lock()
	defer stream.mu.Unlock()
	item, ok := stream.PendingInteractions[interactionID]
	return item, ok
}

// selectPendingExecByControl 根据控制消息的桥消息 ID 查找挂起执行桥。
func selectPendingExecByControl(message *agentv1.ExecClientControlMessage, stream *ActiveStream) (runtimecore.PendingExec, bool) {
	messageID, ok := execControlMessageID(message)
	if !ok {
		return runtimecore.PendingExec{}, false
	}
	return selectPendingExec("", messageID, stream)
}

func execControlMessageID(message *agentv1.ExecClientControlMessage) (uint32, bool) {
	if message == nil {
		return 0, false
	}
	switch item := message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_StreamClose:
		return item.StreamClose.GetId(), true
	case *agentv1.ExecClientControlMessage_Throw:
		return item.Throw.GetId(), true
	case *agentv1.ExecClientControlMessage_Heartbeat:
		return item.Heartbeat.GetId(), true
	default:
		return 0, false
	}
}

func shouldIgnoreMissingExecResult(message *agentv1.ExecClientMessage, stream *ActiveStream) bool {
	if message == nil {
		return false
	}
	return recentlyCompletedExecExists(stream, message.GetId())
}

func shouldIgnoreMissingExecControl(message *agentv1.ExecClientControlMessage, stream *ActiveStream) bool {
	if shouldIgnoreStaleExecControl(message) {
		return true
	}
	messageID, ok := execControlMessageID(message)
	if !ok {
		return false
	}
	return recentlyCompletedExecExists(stream, messageID)
}

func shouldIgnoreStaleExecControl(message *agentv1.ExecClientControlMessage) bool {
	if message == nil {
		return false
	}
	switch message.GetMessage().(type) {
	case *agentv1.ExecClientControlMessage_Heartbeat, *agentv1.ExecClientControlMessage_StreamClose:
		// Reconnecting Cursor clients may keep sending transport-level exec
		// heartbeats / close acks after the original in-memory pending state is gone.
		// Treat these as idempotent noise instead of surfacing protocol 400s.
		return true
	default:
		return false
	}
}

type pendingAssistantMessage struct {
	ID      string                    `json:"id,omitempty"`
	Role    string                    `json:"role,omitempty"`
	Content []pendingAssistantContent `json:"content,omitempty"`
}

type pendingAssistantContent struct {
	Type       string          `json:"type,omitempty"`
	Text       string          `json:"text,omitempty"`
	Signature  string          `json:"signature,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
}

type pendingToolCallReplay struct {
	OpenedAt time.Time
	SortKey  string
	Raw      string
}

func buildPendingToolCalls(pendingExecs []runtimecore.PendingExec, pendingInteractions []runtimecore.PendingInteraction) []string {
	if len(pendingExecs) == 0 && len(pendingInteractions) == 0 {
		return nil
	}

	items := make([]pendingToolCallReplay, 0, len(pendingExecs)+len(pendingInteractions))
	for _, pending := range pendingExecs {
		raw, ok := encodePendingExecAsAssistantOutput(pending)
		if !ok {
			continue
		}
		items = append(items, pendingToolCallReplay{
			OpenedAt: pending.OpenedAt,
			SortKey:  fmt.Sprintf("exec-%020d", pending.MessageID),
			Raw:      raw,
		})
	}
	for _, pending := range pendingInteractions {
		raw, ok := encodePendingInteractionAsAssistantOutput(pending)
		if !ok {
			continue
		}
		items = append(items, pendingToolCallReplay{
			OpenedAt: pending.OpenedAt,
			SortKey:  "interaction-" + strings.TrimSpace(pending.InteractionID),
			Raw:      raw,
		})
	}
	if len(items) == 0 {
		return nil
	}

	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		switch {
		case left.OpenedAt.Equal(right.OpenedAt):
			return left.SortKey < right.SortKey
		case left.OpenedAt.IsZero():
			return false
		case right.OpenedAt.IsZero():
			return true
		default:
			return left.OpenedAt.Before(right.OpenedAt)
		}
	})

	encoded := make([]string, 0, len(items))
	for _, item := range items {
		encoded = append(encoded, item.Raw)
	}
	return encoded
}

func encodePendingExecAsAssistantOutput(pending runtimecore.PendingExec) (string, bool) {
	toolCallID := strings.TrimSpace(pending.ToolCallID)
	toolName, argsJSON, ok := pendingAssistantToolShape(pending)
	if toolCallID == "" || !ok || strings.TrimSpace(toolName) == "" {
		return "", false
	}

	payload, err := json.Marshal(pendingAssistantMessage{
		ID:      "1",
		Role:    "assistant",
		Content: buildPendingAssistantContents(pending.ReasoningContent, pending.ReasoningSignature, toolCallID, toolName, argsJSON),
	})
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func encodePendingInteractionAsAssistantOutput(pending runtimecore.PendingInteraction) (string, bool) {
	toolCallID := strings.TrimSpace(pending.ToolCallID)
	toolName := strings.TrimSpace(deriveToolNameFromPendingInteraction(pending))
	if toolCallID == "" || toolName == "" {
		return "", false
	}
	payload, err := json.Marshal(pendingAssistantMessage{
		ID:      "1",
		Role:    "assistant",
		Content: buildPendingAssistantContents(pending.ReasoningContent, pending.ReasoningSignature, toolCallID, toolName, pending.ArgsJSON),
	})
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func buildPendingAssistantContents(reasoningContent string, reasoningSignature string, toolCallID string, toolName string, argsJSON []byte) []pendingAssistantContent {
	items := make([]pendingAssistantContent, 0, 2)
	if strings.TrimSpace(reasoningContent) != "" {
		items = append(items, pendingAssistantContent{
			Type:      "reasoning",
			Text:      reasoningContent,
			Signature: strings.TrimSpace(reasoningSignature),
		})
	}
	items = append(items, pendingAssistantContent{
		Type:       "tool-call",
		ToolCallID: toolCallID,
		ToolName:   strings.TrimSpace(toolName),
		Args:       append(json.RawMessage(nil), argsJSON...),
	})
	return items
}

func pendingAssistantToolShape(pending runtimecore.PendingExec) (string, []byte, bool) {
	switch strings.TrimSpace(pending.ExecKind) {
	case patchEditReadExecKindName, patchEditWriteExecKindName, patchEditPostReadExecKindName:
		payload, err := decodePendingPatchEditPayload(pending.ArgsJSON)
		if err != nil {
			return "", nil, false
		}
		argsJSON, err := patchEditPayloadArgsJSON(payload)
		if err != nil {
			return "", nil, false
		}
		return firstNonEmpty(strings.TrimSpace(payload.ToolName), patchEditToolName), argsJSON, true
	case writeReadExecKind, writeWriteExecKind, writePostReadExecKind:
		payload, err := decodePendingWritePayload(pending.ArgsJSON)
		if err != nil {
			return "", nil, false
		}
		argsJSON, err := payload.VisibleArgs.MarshalJSON()
		if err != nil {
			return "", nil, false
		}
		return "Write", argsJSON, true
	default:
		toolName := strings.TrimSpace(deriveToolNameFromPendingExec(pending))
		if toolName == "" {
			return "", nil, false
		}
		return toolName, append([]byte(nil), pending.ArgsJSON...), true
	}
}

// markExecCompleted 保留一个短时 tombstone，避免迟到的 transport-level control 被误判为协议错误。
func markExecCompleted(stream *ActiveStream, pending runtimecore.PendingExec) {
	if stream == nil {
		return
	}
	now := time.Now().UTC()
	cutoff := now.Add(-completedExecRetention)

	stream.mu.Lock()
	delete(stream.PendingExecs, pending.ExecID)
	if pending.MessageID != 0 {
		if stream.RecentCompletedExecs == nil {
			stream.RecentCompletedExecs = make(map[uint32]time.Time)
		}
		for messageID, completedAt := range stream.RecentCompletedExecs {
			if completedAt.Before(cutoff) {
				delete(stream.RecentCompletedExecs, messageID)
			}
		}
		stream.RecentCompletedExecs[pending.MessageID] = now
	}
	stream.UpdatedAt = now
	stream.mu.Unlock()
}

// ignoreStaleExecProviderPass ignores only an exec whose exact identity is no
// longer pending. A provider pass mismatch by itself is diagnostic metadata and
// must not discard a still-pending terminal result.
func (service *Service) ignoreStaleExecProviderPass(stream *ActiveStream, pending runtimecore.PendingExec, source string) bool {
	if stream == nil || strings.TrimSpace(pending.ExecID) == "" {
		return false
	}
	stream.mu.Lock()
	currentPass := stream.ProviderPassCount
	_, stillPending := stream.PendingExecs[pending.ExecID]
	stream.mu.Unlock()
	// Once the exact exec_id is still pending, message-id drift is transport
	// metadata and must not turn a valid terminal result into a stale result.
	identityMatches := stillPending
	if !identityMatches {
		if service != nil && service.debug != nil {
			service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "stale_exec_result_ignored", map[string]any{
				"source":        strings.TrimSpace(source),
				"exec_id":       strings.TrimSpace(pending.ExecID),
				"message_id":    pending.MessageID,
				"provider_pass": pending.ProviderPass,
				"current_pass":  currentPass,
				"tool_call_id":  strings.TrimSpace(pending.ToolCallID),
				"reason":        "pending identity no longer active",
			})
		}
		return true
	}
	if currentPass <= 0 || pending.ProviderPass <= 0 || currentPass == pending.ProviderPass {
		return false
	}
	// provider_pass changes when the provider resumes, but it does not change the
	// identity or validity of an exec that is still pending. Keep the watchdog and
	// let the terminal result complete the original exec; pass is diagnostic only.
	if service != nil && service.debug != nil {
		service.debug.LogRuntime(context.Background(), stream.RequestID, stream.ConversationID, "late_exec_result_accepted", map[string]any{
			"source":        strings.TrimSpace(source),
			"exec_id":       strings.TrimSpace(pending.ExecID),
			"message_id":    pending.MessageID,
			"provider_pass": pending.ProviderPass,
			"current_pass":  currentPass,
			"tool_call_id":  strings.TrimSpace(pending.ToolCallID),
		})
	}
	return false
}

func recentlyCompletedExecExists(stream *ActiveStream, messageID uint32) bool {
	if stream == nil || messageID == 0 {
		return false
	}
	now := time.Now().UTC()
	cutoff := now.Add(-completedExecRetention)

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.RecentCompletedExecs) == 0 {
		return false
	}
	completedAt, ok := stream.RecentCompletedExecs[messageID]
	for id, ts := range stream.RecentCompletedExecs {
		if ts.Before(cutoff) {
			delete(stream.RecentCompletedExecs, id)
		}
	}
	if !ok {
		return false
	}
	if completedAt.Before(cutoff) {
		delete(stream.RecentCompletedExecs, messageID)
		return false
	}
	return true
}


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
		"Fetch", "RecordScreen", "ComputerUse", "ForceBackgroundSubagent", "SubagentAwait":
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
