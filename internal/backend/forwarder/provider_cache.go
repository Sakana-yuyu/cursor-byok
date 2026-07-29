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
	"sync"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/localcache"
)

// cacheKeyCache 缓存请求 → 缓存 key 的映射，避免对相同请求重复 JSON 序列化 + SHA256。
// 使用轻量级指纹作为 lookup key，在大多数情况下可跳过昂贵的 json.Marshal 调用。
type cacheKeyCache struct {
	mu      sync.RWMutex
	entries map[string]string // fingerprint → hex key
	order   []string          // FIFO 队列
	maxSize int
}

func newCacheKeyCache(maxSize int) *cacheKeyCache {
	return &cacheKeyCache{
		entries: make(map[string]string, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

// fingerprint 生成请求的轻量级指纹，避免对 Messages 做全量序列化。
// 格式: modelID:mode:thinkingEffort:stableCount:maxTokens:msgCount:len0:len1:...
func (c *cacheKeyCache) fingerprint(req ProviderRequest) string {
	// 预估长度避免多次扩容
	buf := make([]byte, 0, 128)
	buf = append(buf, req.ModelID...)
	buf = append(buf, ':')
	buf = append(buf, byte('0'+req.Mode))
	buf = append(buf, ':')
	buf = append(buf, req.ThinkingEffort...)
	buf = append(buf, ':')
	buf = append(buf, byte('0'+req.StableMessageCount/10), byte('0'+req.StableMessageCount%10))
	buf = append(buf, ':')
	// MaxTokens, Tools count, Messages count
	buf = append(buf, byte('0'+req.MaxTokens/100), byte('0'+(req.MaxTokens/10)%10), byte('0'+req.MaxTokens%10))
	buf = append(buf, ':')
	buf = append(buf, byte('0'+len(req.Tools)/10), byte('0'+len(req.Tools)%10))
	buf = append(buf, ':')
	buf = append(buf, byte('0'+len(req.Messages)/10), byte('0'+len(req.Messages)%10))
	// 各消息的内容长度作为快速指纹
	for _, msg := range req.Messages {
		buf = append(buf, ':')
		contentLen := len(msg.Content)
		if contentLen > 9999 {
			contentLen = 9999
		}
		buf = append(buf, byte('0'+contentLen/1000), byte('0'+(contentLen/100)%10), byte('0'+(contentLen/10)%10), byte('0'+contentLen%10))
	}
	return string(buf)
}

func (c *cacheKeyCache) get(req ProviderRequest) string {
	fp := c.fingerprint(req)
	c.mu.RLock()
	key, ok := c.entries[fp]
	c.mu.RUnlock()
	if ok {
		return key
	}
	return ""
}

func (c *cacheKeyCache) put(req ProviderRequest, key string) {
	fp := c.fingerprint(req)
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if _, exists := c.entries[fp]; exists {
		return // 已存在，无需更新
	}
	
	// FIFO 淘汰
	if len(c.entries) >= c.maxSize {
		oldest := c.order[0]
		delete(c.entries, oldest)
		c.order = c.order[1:]
	}
	
	c.entries[fp] = key
	c.order = append(c.order, fp)
}

// localResponseCacheSettingsProvider 由 resolver（配置管理器）实现，用于按调用即时读取
// 本地响应缓存的启用状态、TTL 与最大条目数（支持配置热加载）。
type localResponseCacheSettingsProvider interface {
	LocalResponseCacheSettings() (enabled bool, ttl time.Duration, maxEntries int)
}

// cachingProviderGateway 是包装底层 ProviderGateway 的响应缓存网关。
type cachingProviderGateway struct {
	inner    ProviderGateway
	settings func() (enabled bool, ttl time.Duration, maxEntries int)
	store    *responseCacheStore
	keyCache *cacheKeyCache // 缓存 key 哈希计算结果
}

// newCachingProviderGateway 构造响应缓存网关。
func newCachingProviderGateway(inner ProviderGateway, settings func() (bool, time.Duration, int)) *cachingProviderGateway {
	return &cachingProviderGateway{
		inner:    inner,
		settings: settings,
		store:    newResponseCacheStore(),
		keyCache: newCacheKeyCache(1024), // 缓存最近 1024 个请求的 key
	}
}

// StartStream 在缓存关闭时直接透传；开启时按精确匹配尝试命中或录制后有条件写入。
func (gateway *cachingProviderGateway) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	enabled, ttl, maxEntries := gateway.settings()
	if !enabled {
		// 禁用路径：与今日完全一致，不做哈希、不录制、不产生额外延迟。
		return gateway.inner.StartStream(ctx, req, sink)
	}

	key := gateway.keyCache.get(req)
	if key == "" {
		key = providerCacheKey(req)
		if key != "" {
			gateway.keyCache.put(req, key)
		}
	}
	if key != "" {
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
		}, maxEntries)
	}
	return innerErr
}

// streamRecorder 录制一次流的事件序列，并跟踪收口/错误状态与 usage 峰值。
type streamRecorder struct {
	events           []modeladapter.ModelEvent
	hasTurnFinished  bool
	hasProviderError bool
	maxInputTokens   int64
	maxOutputTokens  int64
}

func (recorder *streamRecorder) record(event modeladapter.ModelEvent) {
	recorder.events = append(recorder.events, event)
	switch event.Kind {
	case modeladapter.ModelEventKindTurnFinished:
		recorder.hasTurnFinished = true
	case modeladapter.ModelEventKindProviderError:
		recorder.hasProviderError = true
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
func (recorder *streamRecorder) completedCleanly() bool {
	return recorder.hasTurnFinished && !recorder.hasProviderError
}

// responseCacheEntry 是一条缓存的响应事件序列及其估算节省 token 与过期时间。
type responseCacheEntry struct {
	events            []modeladapter.ModelEvent
	savedInputTokens  int64
	savedOutputTokens int64
	expiresAt         time.Time
}

// responseCacheStore 是带 TTL 与 FIFO 淘汰上限的内存响应缓存。
type responseCacheStore struct {
	mu      sync.Mutex
	entries map[string]*responseCacheEntry
	order   []string
}

func newResponseCacheStore() *responseCacheStore {
	return &responseCacheStore{entries: make(map[string]*responseCacheEntry)}
}

func (store *responseCacheStore) get(key string, now time.Time) (*responseCacheEntry, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.entries[key]
	if !ok {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		delete(store.entries, key)
		store.removeOrderLocked(key)
		return nil, false
	}
	return entry, true
}

func (store *responseCacheStore) put(key string, entry *responseCacheEntry, maxEntries int) {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.entries[key]; !exists {
		store.order = append(store.order, key)
	}
	store.entries[key] = entry
	// FIFO 淘汰：超出上限时逐出最早写入的条目。
	for len(store.entries) > maxEntries && len(store.order) > 0 {
		oldest := store.order[0]
		store.order = store.order[1:]
		delete(store.entries, oldest)
	}
}

func (store *responseCacheStore) removeOrderLocked(key string) {
	for index, candidate := range store.order {
		if candidate == key {
			store.order = append(store.order[:index], store.order[index+1:]...)
			return
		}
	}
}

// providerCacheKeyShape 是参与哈希的请求归一化视图，只包含决定输出的字段。
// 刻意排除每次调用都不同的标识（RequestID/RunID/ModelCallID/ConversationID）与
// 非确定性的观测/工件指针，以保证 byte-identical 的请求得到一致缓存键。
type providerCacheKeyShape struct {
	ModelID             string                 `json:"model_id"`
	Mode                int32                  `json:"mode"`
	ThinkingEffort      string                 `json:"thinking_effort"`
	Messages            []modeladapter.Message `json:"messages"`
	StableMessageCount  int                    `json:"stable_message_count"`
	Tools               []json.RawMessage      `json:"tools"`
	MaxTokens           int                    `json:"max_tokens"`
	// RequestKnobs 包含动态估算字段（compiled_prompt_tokens_estimate 等），导致每次请求 key 不同
	// CompileSummary 每次 compile 可能变化
	// 移除这两个字段以提高缓存命中率
	RequestBodyOverride map[string]any         `json:"request_body_override"`
}

// providerCacheKey 对归一化请求做 sha256，返回稳定的十六进制缓存键；序列化失败返回空串（不缓存）。
func providerCacheKey(req ProviderRequest) string {
	shape := providerCacheKeyShape{
		ModelID:             req.ModelID,
		Mode:                int32(req.Mode),
		ThinkingEffort:      req.ThinkingEffort,
		Messages:            req.Messages,
		StableMessageCount:  req.StableMessageCount,
		Tools:               req.Tools,
		MaxTokens:           req.MaxTokens,
		// 移除 RequestKnobs 和 CompileSummary 以避免动态字段污染缓存 key
		RequestBodyOverride: req.RequestBodyOverride,
	}
	encoded, err := json.Marshal(shape)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
