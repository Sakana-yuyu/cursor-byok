package client

import (
	"strings"
	"sync/atomic"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/logger"
	"cursor/internal/safego"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func HasBalanceQueryCapability(adapter serverconfig.ModelAdapterConfig) bool {
	profile := strings.ToLower(strings.TrimSpace(adapter.BalanceProfile))
	if profile == "none" || profile == "" || profile == "auto" {
		return strings.TrimSpace(adapter.BalanceQueryURL) != "" ||
			strings.TrimSpace(adapter.BalanceAccessToken) != "" ||
			strings.TrimSpace(adapter.BalanceUserID) != ""
	}
	return true
}

func providerBalanceRequestFromAdapter(adapter serverconfig.ModelAdapterConfig, forceRefresh bool) ProviderBalanceRequest {
	request := ProviderBalanceRequest{
		Type:                      adapter.Type,
		SupplierID:                adapter.SupplierID,
		BaseURL:                   adapter.BaseURL,
		APIKey:                    adapter.APIKey,
		BalanceProfile:            adapter.BalanceProfile,
		BalanceAccessToken:        adapter.BalanceAccessToken,
		BalanceUserID:             adapter.BalanceUserID,
		BalanceCodingPlanProvider: adapter.BalanceCodingPlanProvider,
		BalanceQueryURL:           adapter.BalanceQueryURL,
		BalanceQueryField:         adapter.BalanceQueryField,
		BalanceQueryHeaders:       adapter.BalanceQueryHeaders,
		ForceRefresh:              forceRefresh,
	}
	return request
}

func (s *ProxyService) invalidateProviderBalanceCaches() {
	if s == nil {
		return
	}
	if s.providerBalanceCache != nil {
		s.providerBalanceCache.clearAll()
	}
	if s.providerBalanceNegativeCache != nil {
		s.providerBalanceNegativeCache.clearAll()
	}
	s.invalidateModelCatalogCaches()
}

// syncedLoginSessionsMax 限制登录去重集合容量：sessionID 只增不删会让长期驻留的
// 桌面进程内存无界增长；超限后整体清空，最坏情况只是对同一会话多同步一次。
const syncedLoginSessionsMax = 128

// providerBalancesSyncedEvent 在每轮账号变更后的余额同步结束（含提前返回）时
// 发给前端：前端据此重载余额快照，避免在「缓存已清、尚未回填」的窗口期并行重打上游。
const providerBalancesSyncedEvent = "provider-balances-synced"

// ProviderBalancesSyncedPayload 是 provider-balances-synced 事件的载荷。
type ProviderBalancesSyncedPayload struct {
	// Refreshed 是本轮实际完成全量刷新的适配器数量；提前返回时为 0。
	Refreshed int `json:"refreshed"`
}

// emitProviderBalancesSynced 向前端发送余额同步完成事件；Wails 应用尚未就绪时静默跳过。
func (s *ProxyService) emitProviderBalancesSynced(refreshed int) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit(providerBalancesSyncedEvent, ProviderBalancesSyncedPayload{Refreshed: refreshed})
}

// SyncProviderBalancesAfterAccountChange clears balance caches and refreshes every
// configured vendor balance so routing metrics and UI stay aligned with the active account.
// 导入/切换/登录完成等多个入口都会并发触发本方法：用独立互斥串行化，避免对上游
// 重复发起 ForceRefresh 全量查询以及「A 刚回填缓存、B 又清空」的交错；再用代际号
// 合并排队者——获锁时若已有更新一轮完整同步完成，则本轮无需重复执行。
func (s *ProxyService) SyncProviderBalancesAfterAccountChange() int {
	if s == nil {
		return 0
	}
	// 获锁前先快照代际号：排队等待期间任何一轮完整同步结束都会使代际前进。
	observedGeneration := atomic.LoadUint64(&s.syncGeneration)
	s.syncProviderBalancesMu.Lock()
	defer s.syncProviderBalancesMu.Unlock()
	if atomic.LoadUint64(&s.syncGeneration) != observedGeneration {
		// 排队期间已有更新一轮同步完成（该轮已发过完成事件），直接返回，
		// 避免重复清缓存 + 全量刷新造成 N×15s 的串行放大。
		return 0
	}
	// 先确认配置可用再清缓存：若配置读取失败或计费查询被关闭，
	// 已清空的路由指标不会丢失，路由决策保留上一次的余额信号。
	cfg, err := s.LoadUserConfig()
	if err != nil || !cfg.BillingQuery.Enabled {
		s.emitProviderBalancesSynced(0)
		return 0
	}
	s.invalidateProviderBalanceCaches()
	if s.routingMetrics != nil {
		s.routingMetrics.Clear()
	}
	refreshed := 0
	for _, adapter := range cfg.ModelAdapters {
		if !HasBalanceQueryCapability(adapter) {
			continue
		}
		s.refreshProviderBalanceForAdapter(adapter)
		refreshed++
	}
	atomic.AddUint64(&s.syncGeneration, 1)
	s.emitProviderBalancesSynced(refreshed)
	return refreshed
}

// refreshProviderBalanceForAdapter 查询单个适配器余额并记录路由指标。
// 与 bridge.QueryAllProviderBalances 保持一致的 panic 隔离：单个供应商的解析/
// 网络异常绝不拖垮整个进程（bridge 层注释记录过无保护时 Wails 主进程闪退的事故）。
func (s *ProxyService) refreshProviderBalanceForAdapter(adapter serverconfig.ModelAdapterConfig) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("client sync provider balance panic recovered adapter_id=%s panic=%v",
				strings.TrimSpace(adapter.ID), r)
		}
	}()
	balance := s.QueryProviderBalance(providerBalanceRequestFromAdapter(adapter, true))
	s.RecordRoutingMetrics(adapter.ID, balance)
}

// triggerProviderBalanceSyncAfterAccountChange 在后台执行账号变更后的余额同步：
// 全量刷新可能持续 N×15s（providerBalanceTimeout），绝不能阻塞 Wails 绑定调用方。
// SyncProviderBalancesAfterAccountChange 内部有互斥，并发触发的排队者会被
// 代际号合并：若等待期间已有完整同步轮次结束则直接返回。
func (s *ProxyService) triggerProviderBalanceSyncAfterAccountChange() {
	if s == nil {
		return
	}
	safego.Go("client:账号变更后余额同步", func() { s.SyncProviderBalancesAfterAccountChange() })
}

func (s *ProxyService) maybeSyncAfterLoginSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.loginSyncMu.Lock()
	if s.syncedLoginSessions == nil {
		s.syncedLoginSessions = make(map[string]struct{})
	}
	if len(s.syncedLoginSessions) >= syncedLoginSessionsMax {
		s.syncedLoginSessions = make(map[string]struct{})
	}
	if _, seen := s.syncedLoginSessions[sessionID]; seen {
		s.loginSyncMu.Unlock()
		return
	}
	s.syncedLoginSessions[sessionID] = struct{}{}
	s.loginSyncMu.Unlock()
	s.SyncProviderBalancesAfterAccountChange()
}
