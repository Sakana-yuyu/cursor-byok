// metadata_cache.go 为 provider 元数据（模型列表、余额）提供进程内 TTL 缓存，
// 用于减少重复网络调用。缓存按 (type, 归一化 baseURL, apiKey 哈希) 键控，
// 线程安全，绝不缓存错误，也绝不记录明文密钥（key 中仅保存 apiKey 的哈希）。
package client

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"cursor/internal/modelchannel"
)

const (
	// modelCatalogCacheTTL 表示模型列表缓存的存活时长。
	modelCatalogCacheTTL = 5 * time.Minute
	// providerBalanceCacheTTL 表示余额查询成功结果的缓存存活时长。
	providerBalanceCacheTTL = 60 * time.Second
	// providerBalanceNegativeCacheTTL 表示「确定性不支持/失败」余额查询结果的负缓存存活时长。
	// 上游未开启计费接口时，避免每轮 60s 轮询都全策略链重打上游（单轮最多 6-8 个请求）。
	// 瞬时传输失败不进负缓存；ForceRefresh 显式刷新可绕过。
	providerBalanceNegativeCacheTTL = 10 * time.Minute
)

// metadataCacheEntry 保存单条缓存值及其过期时间。
type metadataCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// metadataCache 是一个线程安全、按 TTL 过期的通用键值缓存。
type metadataCache[T any] struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]metadataCacheEntry[T]
}

// newMetadataCache 创建带指定 TTL 的元数据缓存。
func newMetadataCache[T any](ttl time.Duration) *metadataCache[T] {
	return &metadataCache[T]{ttl: ttl, entries: make(map[string]metadataCacheEntry[T])}
}

// get 读取未过期的缓存值；过期或缺失时返回 ok=false。
func (c *metadataCache[T]) get(key string) (T, bool) {
	var zero T
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return zero, false
	}
	return entry.value, true
}

// set 写入缓存值并刷新过期时间。仅供成功结果调用（绝不缓存错误）。
func (c *metadataCache[T]) set(key string, value T) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = metadataCacheEntry[T]{value: value, expiresAt: time.Now().Add(c.ttl)}
}

// invalidate 主动删除指定键，供显式刷新时绕过缓存。
func (c *metadataCache[T]) invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *metadataCache[T]) clearAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]metadataCacheEntry[T])
}

type metadataCacheDiagnostics struct {
	EntryCount           int
	TTLSeconds           int64
	OldestStoredAtUnixMS int64
	NextExpiryAtUnixMS   int64
}

// diagnostics returns aggregate live-entry metadata only. Cache keys and values
// never cross this boundary.
func (c *metadataCache[T]) diagnostics(now time.Time) metadataCacheDiagnostics {
	if c == nil {
		return metadataCacheDiagnostics{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := metadataCacheDiagnostics{TTLSeconds: int64(c.ttl / time.Second)}
	for _, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			continue
		}
		result.EntryCount++
		storedAt := entry.expiresAt.Add(-c.ttl).UnixMilli()
		if result.OldestStoredAtUnixMS == 0 || storedAt < result.OldestStoredAtUnixMS {
			result.OldestStoredAtUnixMS = storedAt
		}
		expiresAt := entry.expiresAt.UnixMilli()
		if result.NextExpiryAtUnixMS == 0 || expiresAt < result.NextExpiryAtUnixMS {
			result.NextExpiryAtUnixMS = expiresAt
		}
	}
	return result
}

// metadataCacheKey hashes the complete normalized identity so neither credentials
// nor endpoint URLs appear in cache keys, diagnostics, crash dumps, or logs.
func metadataCacheKey(typeName, baseURL, apiKey string) string {
	normalizedBaseURL := strings.TrimSpace(baseURL)
	if resolved, err := modelchannel.NormalizeBaseURL(baseURL); err == nil {
		normalizedBaseURL = resolved
	}
	return metadataIdentityHash(
		strings.ToLower(strings.TrimSpace(typeName)),
		normalizedBaseURL,
		strings.TrimSpace(apiKey),
	)
}

func metadataIdentityHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}
