// session_affinity.go 会话粘性（借鉴 magpie 的 prompt cache 亲和设计）：
// 同一 Cursor 对话在同一模型的连续调用之间优先复用上次成功应答的渠道
// （渠道指纹包含 APIKey，即精确到密钥），让上游 prompt cache 命中率不被
// 密钥池轮换稀释。粘性只在渠道健康且本轮未失败过时生效；失败或冷却时
// 交回正常轮换，新的成功渠道会接管粘性。状态仅存内存：进程重启后上游
// 前缀缓存同样已冷，无需持久化。
package modeladapter

import (
	"strings"
	"sync"
	"time"

	legacyruntime "cursor/internal/runtime"
)

// sessionAffinityTTL 是粘性的保持窗口：超过该时长未复用即视为缓存已冷
// （Anthropic ephemeral 缓存 5 分钟过期，OpenAI 前缀缓存更短），不再粘滞。
const sessionAffinityTTL = 5 * time.Minute

// sessionAffinityEntry 记录一个（会话, 模型）维度的粘性目标。
type sessionAffinityEntry struct {
	// fingerprint 是粘性渠道的配置指纹（channelConfigurationFingerprint）。
	fingerprint string
	// updatedAt 是最近一次成功复用/建立粘性的时间，用于 TTL 判定与惰性清理。
	updatedAt time.Time
}

// sessionAffinity 维护会话维度的粘性映射。零值可用。
type sessionAffinity struct {
	mu    sync.Mutex
	byKey map[string]sessionAffinityEntry
}

// sessionAffinityKey 返回粘性键（会话 + 模型维度）；会话为空时返回空串，
// 调用方据此跳过粘性。
func sessionAffinityKey(conversationID, modelID string) string {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	return conversationID + "\x00" + strings.TrimSpace(modelID)
}

// fingerprintOf 返回粘性目标指纹；目标已失效（TTL 过期）时返回空串并顺手
// 清理条目。
func (a *sessionAffinity) fingerprintOf(key string, now time.Time) string {
	if a == nil || key == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.byKey[key]
	if !ok {
		return ""
	}
	if now.Sub(entry.updatedAt) > sessionAffinityTTL {
		delete(a.byKey, key)
		return ""
	}
	return entry.fingerprint
}

// record 建立或刷新粘性。写入时顺带做惰性过期清理，避免 map 随历史会话
// 无界增长（条目量级 = 活跃会话数，遍历代价可忽略）。
func (a *sessionAffinity) record(key, fingerprint string, now time.Time) {
	if a == nil || key == "" || fingerprint == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.byKey == nil {
		a.byKey = make(map[string]sessionAffinityEntry)
	}
	for existingKey, entry := range a.byKey {
		if now.Sub(entry.updatedAt) > sessionAffinityTTL {
			delete(a.byKey, existingKey)
		}
	}
	a.byKey[key] = sessionAffinityEntry{fingerprint: fingerprint, updatedAt: now}
}

// dropFingerprint 清除所有指向该渠道指纹的粘性（渠道失败时调用：失败的
// 渠道不应继续吸住新会话轮次）。密钥池克隆渠道的指纹含 APIKey，只会清除
// 粘在该渠道上的会话，不影响同池其他密钥的粘性。
func (a *sessionAffinity) dropFingerprint(fingerprint string) {
	if a == nil || fingerprint == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, entry := range a.byKey {
		if entry.fingerprint == fingerprint {
			delete(a.byKey, key)
		}
	}
}

// pickStickyChannel 在候选渠道列表中查找粘性渠道：指纹匹配、本轮尚未尝试、
// 且不在冷却中。任一条件不满足则返回 nil，交回 preferHealthyChannel 正常轮换。
func (router *Router) pickStickyChannel(channels []*legacyruntime.ResolvedChannel, conversationID, modelID string, tried map[string]struct{}) *legacyruntime.ResolvedChannel {
	if router == nil || router.affinity == nil || len(channels) == 0 {
		return nil
	}
	key := sessionAffinityKey(conversationID, modelID)
	if key == "" {
		return nil
	}
	fingerprint := router.affinity.fingerprintOf(key, time.Now())
	if fingerprint == "" {
		return nil
	}
	for _, channel := range channels {
		if channel == nil || strings.TrimSpace(channel.ID) == "" {
			continue
		}
		if channelConfigurationFingerprint(channel) != fingerprint {
			continue
		}
		// 粘性渠道本轮已失败过：继续粘会原地打转，交回正常轮换。
		if tried != nil {
			if _, done := tried[channelHealthMapKey(channel)]; done {
				return nil
			}
		}
		// 粘性渠道冷却中：本轮跳过粘性但保留记录，冷却结束后会话自动回流。
		if router.isChannelCoolingDown(channel) {
			return nil
		}
		return channel
	}
	// 指纹对应的渠道已不在候选列表（配置被修改/删除）：粘性自然失效。
	return nil
}

// isChannelCoolingDown 报告渠道是否处于未过期的冷却中，并顺手清理过期条目。
func (router *Router) isChannelCoolingDown(channel *legacyruntime.ResolvedChannel) bool {
	if router == nil || channel == nil {
		return false
	}
	now := time.Now()
	key := channelHealthMapKey(channel)
	router.healthMu.Lock()
	defer router.healthMu.Unlock()
	health, ok := router.healthByChannel[key]
	if !ok {
		return false
	}
	if !health.cooldownUntil.After(now) {
		delete(router.healthByChannel, key)
		return false
	}
	return true
}

// recordSessionAffinity 在渠道成功应答后建立/刷新该（会话, 模型）的粘性。
func (router *Router) recordSessionAffinity(req StreamRequest, channel *legacyruntime.ResolvedChannel) {
	if router == nil || router.affinity == nil || channel == nil {
		return
	}
	key := sessionAffinityKey(req.ConversationID, req.ModelID)
	if key == "" {
		return
	}
	router.affinity.record(key, channelConfigurationFingerprint(channel), time.Now())
}

// dropSessionAffinity 在渠道失败后清除指向该渠道的粘性。仅当当前会话的
// 粘性目标就是失败渠道时才清除——粘性指向其他渠道（如冷却中被跳过）时
// 保留原粘性。
func (router *Router) dropSessionAffinity(req StreamRequest, channel *legacyruntime.ResolvedChannel) {
	if router == nil || router.affinity == nil || channel == nil {
		return
	}
	key := sessionAffinityKey(req.ConversationID, req.ModelID)
	if key == "" {
		return
	}
	fingerprint := channelConfigurationFingerprint(channel)
	if router.affinity.fingerprintOf(key, time.Now()) != fingerprint {
		return
	}
	router.affinity.dropFingerprint(fingerprint)
}
