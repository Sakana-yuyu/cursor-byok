// provider_cache.go 实现精确匹配（byte-identical）的本地 LLM 响应缓存网关。
//
// 稳定性优先：该缓存默认关闭，只有配置显式开启后才生效。关闭时 StartStream 直接
// 透传底层网关，sink 事件流与今日逐字节一致，热路径上仅多一次布尔判断。
//
// 命中判定为完全精确匹配（对整个归一化请求做 sha256），绝不做模糊匹配；仅当一次流
// 正常收口（出现 TurnFinished 且无 ProviderError、无取消）时才写入缓存，绝不缓存
// 部分、错误或被取消的流。存储仅在内存，带 TTL 与最大条目数上限（FIFO 淘汰）。
package forwarder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/localcache"
)

// localResponseCacheSettingsProvider 由 resolver（配置管理器）实现，用于按调用即时读取
// 本地响应缓存的启用状态、TTL、最大条目数与持久化开关（支持配置热加载）。
type localResponseCacheSettingsProvider interface {
	LocalResponseCacheSettings() (enabled bool, ttl time.Duration, maxEntries int, persist bool)
}

type modelSourceCacheResolver interface {
	ModelSourceForModel(context.Context, string) string
}

// cachingProviderGateway 是包装底层 ProviderGateway 的响应缓存网关。
type cachingProviderGateway struct {
	inner       ProviderGateway
	settings    func() (enabled bool, ttl time.Duration, maxEntries int, persist bool)
	modelSource func(context.Context, string) string
	store       *responseCacheStore
}

// newCachingProviderGateway 构造响应缓存网关。persistPath 为空时不做磁盘持久化。
func newCachingProviderGateway(inner ProviderGateway, settings func() (bool, time.Duration, int, bool), persistPath string, modelSources ...func(context.Context, string) string) *cachingProviderGateway {
	var modelSource func(context.Context, string) string
	if len(modelSources) > 0 {
		modelSource = modelSources[0]
	}
	return &cachingProviderGateway{
		inner:       inner,
		settings:    settings,
		modelSource: modelSource,
		store: newResponseCacheStore(persistPath, func() time.Duration {
			_, ttl, _, _ := settings()
			return ttl
		}, func() bool {
			_, _, _, persist := settings()
			return persist
		}),
	}
}

// StartStream 在缓存关闭时直接透传；开启时按精确匹配尝试命中或录制后有条件写入，
// 只复用已完整收口的响应。进行中的流不做合并，避免用户重试或重复发送时等待旧流
// 取消收尾，把旧流剩余时长直接叠加到新请求 TTFT。
func (gateway *cachingProviderGateway) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	enabled, ttl, maxEntries, _ := gateway.settings()
	if !enabled {
		// 禁用路径：与今日完全一致，不做哈希、不录制、不产生额外延迟。
		return gateway.inner.StartStream(ctx, req, sink)
	}
	if strings.TrimSpace(req.ModelSource) == "" {
		req.ModelSource = "third_party"
		if gateway.modelSource != nil {
			if resolved := strings.TrimSpace(gateway.modelSource(ctx, req.ModelID)); resolved != "" {
				req.ModelSource = resolved
			}
		}
	}

	key := providerCacheKey(req)
	if key == "" {
		// 无法归一化 key（序列化失败）：直接透传，不参与缓存与合并。
		return gateway.inner.StartStream(ctx, req, sink)
	}

	if entry, ok := gateway.store.get(key, time.Now()); ok {
		// 命中：直接把缓存事件序列回放给 sink，等价于底层 provider 成功流。
		for _, event := range entry.events {
			if err := sink(event); err != nil {
				return err
			}
		}
		localcache.RecordHit(entry.savedInputTokens, entry.savedOutputTokens)
		return nil
	}

	recorder := &streamRecorder{}
	innerErr := gateway.inner.StartStream(ctx, req, func(event modeladapter.ModelEvent) error {
		// 先原样投递给真实 sink，保证行为不变；投递成功后再录制副本。
		if err := sink(event); err != nil {
			return err
		}
		recorder.record(event)
		return nil
	})
	localcache.RecordMiss()

	if key != "" && innerErr == nil && ctx.Err() == nil && recorder.completedCleanly() {
		gateway.store.put(key, &responseCacheEntry{
			events:            recorder.events,
			savedInputTokens:  recorder.maxInputTokens,
			savedOutputTokens: recorder.maxOutputTokens,
			expiresAt:         time.Now().Add(ttl),
			lastAccessAt:      time.Now(),
		}, maxEntries)
	}
	return innerErr
}

// streamRecorder 录制一次流的事件序列，并跟踪收口/错误状态与 usage 峰值。
type streamRecorder struct {
	events             []modeladapter.ModelEvent
	hasTurnFinished    bool
	hasProviderError   bool
	finishReason       string
	hadAssistantOutput bool
	hadToolCall        bool
	maxInputTokens     int64
	maxOutputTokens    int64
}

func (recorder *streamRecorder) record(event modeladapter.ModelEvent) {
	recorder.events = append(recorder.events, event)
	switch event.Kind {
	case modeladapter.ModelEventKindTurnFinished:
		recorder.hasTurnFinished = true
		recorder.finishReason = strings.TrimSpace(event.FinishReason)
	case modeladapter.ModelEventKindProviderError:
		recorder.hasProviderError = true
	case modeladapter.ModelEventKindTextDelta:
		// 模型产出了可见的助手正文增量，视为有效输出。
		recorder.hadAssistantOutput = true
	case modeladapter.ModelEventKindPartialToolCall,
		modeladapter.ModelEventKindToolCallDelta,
		modeladapter.ModelEventKindToolLikeCompleted:
		// 模型发起了工具调用，视为有效输出（工具调用结果回合也可能没有 TextDelta）。
		// 但含工具调用的回合绝不允许写入本地响应缓存：命中回放会再次派发同一工具，
		// 造成 shell/写文件等副作用重复执行。
		recorder.hadAssistantOutput = true
		recorder.hadToolCall = true
	}
	if event.ToolInvocation != nil {
		recorder.hadAssistantOutput = true
	}
	if event.Err != nil {
		recorder.hasProviderError = true
	}
	if event.InputTokens > recorder.maxInputTokens {
		recorder.maxInputTokens = event.InputTokens
	}
	if event.OutputTokens > recorder.maxOutputTokens {
		recorder.maxOutputTokens = event.OutputTokens
	}
}

// completedCleanly 表示流正常收口：出现 TurnFinished 且无任何 ProviderError。
// 额外排除两类不应被缓存的「看似成功」流：
//   - finishReason 为截断类（max_output_tokens/length/content_filter）：响应被输出预算截断，
//     不是完整结果，缓存后重试只会回放同样的截断流，导致任务永久卡死。
//   - 整回合没有任何助手正文或工具调用（如纯 reasoning 触顶）：空结果缓存无意义且会遮蔽真实重试。
func (recorder *streamRecorder) completedCleanly() bool {
	return recorder.hasTurnFinished &&
		!recorder.hasProviderError &&
		!recorder.hadToolCall &&
		!isTruncationFinishReason(recorder.finishReason) &&
		recorder.hadAssistantOutput
}

// isTruncationFinishReason 判断收口原因是否表示响应被截断（而非模型主动结束）。
// 这类响应不应进入本地响应缓存，否则重试会回放相同的截断流。
func isTruncationFinishReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "max_output_tokens", "length", "content_filter":
		return true
	default:
		return false
	}
}

// responseCacheEntry 是一条缓存的响应事件序列及其估算节省 token 与过期时间。
type responseCacheEntry struct {
	events            []modeladapter.ModelEvent
	savedInputTokens  int64
	savedOutputTokens int64
	expiresAt         time.Time
	lastAccessAt      time.Time // 最近一次命中时间，用于 LRU 排序与磁盘恢复
}

// responseCacheStore 是带 TTL 与 LRU 淘汰上限的内存响应缓存，可持久化到磁盘。
type responseCacheStore struct {
	mu                  sync.Mutex
	path                string               // 磁盘持久化路径；空表示不支持持久化
	ttlProvider         func() time.Duration // 当前 TTL（热加载），用于磁盘恢复时续期
	persistenceProvider func() bool          // 当前是否允许读写磁盘缓存（支持设置热加载）
	persistActive       bool
	loaded              bool
	dirty               bool
	saveTimer           *time.Timer
	entries             map[string]*responseCacheEntry
	order               []string // LRU 顺序：头部最久未使用，尾部最近使用
}

func newResponseCacheStore(path string, ttlProvider func() time.Duration, persistenceProviders ...func() bool) *responseCacheStore {
	var persistenceProvider func() bool
	if len(persistenceProviders) > 0 {
		persistenceProvider = persistenceProviders[0]
	}
	return &responseCacheStore{
		path:                path,
		ttlProvider:         ttlProvider,
		persistenceProvider: persistenceProvider,
		persistActive:       path != "",
		entries:             make(map[string]*responseCacheEntry),
	}
}

// syncPersistenceLocked 在每次缓存读写前同步设置。关闭时放弃尚未落盘的脏状态，
// 保证用户关闭开关后不再把仅内存期间的条目写回磁盘。
func (store *responseCacheStore) syncPersistenceLocked() {
	wantPersist := store.path != ""
	if wantPersist && store.persistenceProvider != nil {
		wantPersist = store.persistenceProvider()
	}
	if store.persistActive && !wantPersist {
		store.dirty = false
	}
	store.persistActive = wantPersist
}

// ensureLoaded 懒加载磁盘缓存（首次访问时执行一次）。
// 加载时按「最近访问时间 + 当前 TTL」续期：TTL 语义是"多久未访问则失效"，
// 用户调长 TTL 后，此前写入的条目（旧 ExpiresAt）不应立即过期，而应继续可用。
func (store *responseCacheStore) ensureLoaded() {
	if store.loaded || !store.persistActive {
		return
	}
	if store.path == "" {
		store.loaded = true
		return
	}
	store.loaded = true
	loaded := loadResponseCacheFromDisk(store.path)
	if len(loaded) == 0 {
		return
	}
	now := time.Now()
	ttl := store.currentTTL()
	for key, entry := range loaded {
		if ttl > 0 && !entry.lastAccessAt.IsZero() {
			renewed := entry.lastAccessAt.Add(ttl)
			if renewed.After(entry.expiresAt) {
				entry.expiresAt = renewed
			}
		}
		if now.After(entry.expiresAt) {
			continue
		}
		if _, exists := store.entries[key]; exists {
			// 运行中先写入内存的条目更新，不能被稍后启用持久化时读取的旧磁盘副本覆盖。
			continue
		}
		store.entries[key] = entry
	}
	// 按最近访问时间升序重建 LRU 顺序（磁盘时间戳缺失视为最早）。
	store.order = make([]string, 0, len(store.entries))
	for key := range store.entries {
		store.order = append(store.order, key)
	}
	sort.Slice(store.order, func(i, j int) bool {
		left := store.entries[store.order[i]].lastAccessAt
		right := store.entries[store.order[j]].lastAccessAt
		if left.IsZero() {
			return true
		}
		if right.IsZero() {
			return false
		}
		return left.Before(right)
	})
}

// currentTTL 读取当前热加载 TTL；无 provider 时返回 0（不续期）。
func (store *responseCacheStore) currentTTL() time.Duration {
	if store.ttlProvider == nil {
		return 0
	}
	return store.ttlProvider()
}

// touchLocked 将 key 移到 LRU 末尾（最近使用）。
func (store *responseCacheStore) touchLocked(key string) {
	for index, candidate := range store.order {
		if candidate == key {
			store.order = append(store.order[:index], store.order[index+1:]...)
			break
		}
	}
	store.order = append(store.order, key)
}

func (store *responseCacheStore) get(key string, now time.Time) (*responseCacheEntry, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncPersistenceLocked()
	store.ensureLoaded()
	entry, ok := store.entries[key]
	if !ok {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		delete(store.entries, key)
		store.touchLocked(key)
		store.order = store.order[:len(store.order)-1]
		store.markDirtyLocked()
		return nil, false
	}
	entry.lastAccessAt = now
	store.touchLocked(key)
	return entry, true
}

func (store *responseCacheStore) put(key string, entry *responseCacheEntry, maxEntries int) {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncPersistenceLocked()
	store.ensureLoaded()
	if _, exists := store.entries[key]; !exists {
		store.order = append(store.order, key)
	}
	store.entries[key] = entry
	// LRU 淘汰：超出上限时逐出最久未使用的条目。
	for len(store.entries) > maxEntries && len(store.order) > 0 {
		oldest := store.order[0]
		store.order = store.order[1:]
		delete(store.entries, oldest)
	}
	store.markDirtyLocked()
}

// markDirtyLocked 标记缓存已变更并调度节流落盘（2s 合并多次写入）。
func (store *responseCacheStore) markDirtyLocked() {
	if !store.persistActive {
		return
	}
	store.dirty = true
	if store.saveTimer != nil {
		return
	}
	store.saveTimer = time.AfterFunc(saveDebounceDelay, func() {
		store.flushToDisk()
	})
}

// flushToDisk 立即把当前内存内容写入磁盘（节流回调与关闭前兜底共用）。
func (store *responseCacheStore) flushToDisk() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saveTimer = nil
	store.syncPersistenceLocked()
	if !store.dirty || !store.persistActive {
		return
	}
	snapshot := make(map[string]*responseCacheEntry, len(store.entries))
	for key, entry := range store.entries {
		snapshot[key] = entry
	}
	if err := saveResponseCacheToDisk(store.path, snapshot); err == nil {
		store.dirty = false
	}
}

// providerCacheKeyShape 是参与哈希的请求归一化视图，只包含决定输出的字段。
// 刻意排除每次调用都不同的临时标识（RequestID/RunID/ModelCallID）与
// 非确定性的观测/工件指针，以保证 byte-identical 的请求得到一致缓存键。
//
// ConversationID 属于会话隔离域，必须参与哈希：两个不同 conversation 即使
// provider-visible 请求完全相同，也不能共享本地响应缓存（否则 A 的完成流、
// 推理签名等 provider 元数据会被回放进 B 的 history）。它不是临时调用标识。
//
// Messages 不直接使用 modeladapter.Message，而走 normalizedCacheMessage 视图：
// 后者剔除 provider 每次调用都会变化但又不影响请求语义的字段
// （ReasoningSignature/ReasoningSignatureSource、OpenAIResponsesReasoningID/Status/Summary、
// ToolCall 的 ID/OpenAIResponsesID/OpenAIResponsesCallID/OpenAIResponsesStatus）。
// 这些字段是 provider 对某一次特定调用的临时背书/标识，不是请求的语义内容；
// 若不剔除，只要历史里出现过 thinking 或工具调用，缓存 key 每次都不同，命中率趋近于 0。
// 归一化只影响 key 计算，缓存的回放内容仍是完整的原始事件序列（含正确 signature）。
type providerCacheKeyShape struct {
	ConversationID     string                   `json:"conversation_id"`
	ModelID            string                   `json:"model_id"`
	ModelSource        string                   `json:"model_source"`
	Mode               int32                    `json:"mode"`
	ThinkingEffort     string                   `json:"thinking_effort"`
	Messages           []normalizedCacheMessage `json:"messages"`
	StableMessageCount int                      `json:"stable_message_count"`
	Tools              []json.RawMessage        `json:"tools"`
	MaxTokens          int                      `json:"max_tokens"`
	// RequestKnobs 包含动态估算字段（compiled_prompt_tokens_estimate 等），导致每次请求 key 不同
	// CompileSummary 每次 compile 可能变化
	// 移除这两个字段以提高缓存命中率
	RequestBodyOverride map[string]any `json:"request_body_override"`
}

// normalizedCacheMessage 是参与缓存键计算的消息归一化视图。
// 只保留决定请求语义的字段：Role、正文 Content、结构化 ContentParts、推理正文 ReasoningContent，
// 以及工具调用的语义部分（Function.Name/Arguments、ToolCallID 关联、tool 名称）。
// 刻意省略 provider 临时背书类字段（见 providerCacheKeyShape 注释）。
type normalizedCacheMessage struct {
	Role             string                     `json:"role"`
	Content          string                     `json:"content"`
	ContentParts     []modeladapter.ContentPart `json:"content_parts,omitempty"`
	ReasoningContent string                     `json:"reasoning_content,omitempty"`
	ToolCalls        []normalizedCacheToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string                     `json:"tool_call_id,omitempty"`
	Name             string                     `json:"name,omitempty"`
}

// normalizedCacheToolCall 保留工具调用的语义字段，剔除 provider 临时 ID/状态。
type normalizedCacheToolCall struct {
	Index    int                                `json:"index,omitempty"`
	Type     string                             `json:"type"`
	Function modeladapter.ToolCallFunctionShape `json:"function"`
}

// normalizeMessagesForCacheKey 把原始消息列表转成参与缓存键的归一化视图，
// 剔除每次调用都变化但不影响语义的 provider 临时字段。
func normalizeMessagesForCacheKey(messages []modeladapter.Message) []normalizedCacheMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]normalizedCacheMessage, len(messages))
	for i, msg := range messages {
		normalized := normalizedCacheMessage{
			Role:             msg.Role,
			Content:          msg.Content,
			ReasoningContent: msg.ReasoningContent,
			ToolCallID:       msg.ToolCallID,
			Name:             msg.Name,
		}
		// ContentParts 含文本/图片等语义内容，整体保留（图片数据本身就是语义）。
		if len(msg.ContentParts) > 0 {
			normalized.ContentParts = msg.ContentParts
		}
		if len(msg.ToolCalls) > 0 {
			calls := make([]normalizedCacheToolCall, len(msg.ToolCalls))
			for j, call := range msg.ToolCalls {
				calls[j] = normalizedCacheToolCall{
					Index:    call.Index,
					Type:     call.Type,
					Function: call.Function,
				}
			}
			normalized.ToolCalls = calls
		}
		out[i] = normalized
	}
	return out
}

// normalizeCacheMaxTokens 把 max_tokens 归一化到 1024 分桶：同一请求因预算恢复/调整
// 产生的 max_tokens 差异（如 400 降级恢复后的 cap）不再改变缓存键。回放的是完整
// 成功流，不受 max_tokens 影响，因此归一化只提升命中率、不影响正确性。
func normalizeCacheMaxTokens(value int) int {
	if value <= 0 {
		return 0
	}
	return (value + 1023) / 1024 * 1024
}

// providerCacheKeyHead 是除 messages/tools/override 外参与哈希的元数据视图。
// ModelSource 参与哈希：账户模型与第三方模型即使 provider-visible 请求一致，
// 也不能共享本地响应缓存条目。
type providerCacheKeyHead struct {
	ConversationID     string `json:"conversation_id"`
	ModelID            string `json:"model_id"`
	ModelSource        string `json:"model_source"`
	Mode               int32  `json:"mode"`
	ThinkingEffort     string `json:"thinking_effort"`
	StableMessageCount int    `json:"stable_message_count"`
	MaxTokens          int    `json:"max_tokens"`
}

// providerCacheKey 对归一化请求做 sha256，返回稳定的十六进制缓存键；序列化失败返回空串（不缓存）。
// 稳定前缀 messages[:StableMessageCount] 单独摘要后写入，尾部 messages 直接编码，tools 单独
// 摘要，避免每次把完整工具定义与全部历史 messages 拼成一个大 JSON 再哈希。
func providerCacheKey(req ProviderRequest) string {
	messages := normalizeMessagesForCacheKey(req.Messages)
	head := providerCacheKeyHead{
		ConversationID:     strings.TrimSpace(req.ConversationID),
		ModelID:            req.ModelID,
		ModelSource:        strings.TrimSpace(req.ModelSource),
		Mode:               int32(req.Mode),
		ThinkingEffort:     req.ThinkingEffort,
		StableMessageCount: req.StableMessageCount,
		MaxTokens:          normalizeCacheMaxTokens(req.MaxTokens),
	}
	headEncoded, err := json.Marshal(head)
	if err != nil {
		return ""
	}

	stable := req.StableMessageCount
	if stable < 0 {
		stable = 0
	}
	if stable > len(messages) {
		stable = len(messages)
	}

	hasher := sha256.New()
	hasher.Write(headEncoded)

	if stable > 0 {
		stableEncoded, err := json.Marshal(messages[:stable])
		if err != nil {
			return ""
		}
		stableDigest := sha256.Sum256(stableEncoded)
		hasher.Write(stableDigest[:])
	}
	if stable < len(messages) {
		tailEncoded, err := json.Marshal(messages[stable:])
		if err != nil {
			return ""
		}
		hasher.Write(tailEncoded)
	} else if len(messages) == 0 {
		hasher.Write([]byte("[]"))
	}

	if len(req.Tools) > 0 {
		toolsEncoded, err := json.Marshal(req.Tools)
		if err != nil {
			return ""
		}
		toolsDigest := sha256.Sum256(toolsEncoded)
		hasher.Write(toolsDigest[:])
	}
	if len(req.RequestBodyOverride) > 0 {
		overrideEncoded, err := json.Marshal(req.RequestBodyOverride)
		if err != nil {
			return ""
		}
		hasher.Write(overrideEncoded)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}
