package config

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cursor/internal/backend/delegation"
	"cursor/internal/backend/forwarder"
	"cursor/internal/historymetrics"
	"cursor/internal/logger"
	legacyruntime "cursor/internal/runtime"
)

const configHotReloadMinInterval = 500 * time.Millisecond

type Manager struct {
	store   *Store
	current atomic.Pointer[Config]
	// saveMu 串行化「读快照→修改→整包落盘」周期，防止后台持久化任务
	// （PersistChannelContextWindow / max_tokens cap / LastAgentModelHash / delegation）
	// 与用户 SaveUserConfig 并发时用旧快照覆盖对方改动（lost-update）。
	saveMu           sync.Mutex
	listenersMu      sync.RWMutex
	listeners        []func(Config)
	reloadMu         sync.Mutex
	snapshot         fileSnapshot
	lastReload       time.Time
	reloadError      string
	selectionMu      sync.Mutex
	selectionOffsets map[string]int
}

func NewManager(ctx context.Context, store *Store) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("config store is required")
	}
	cfg, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		store:            store,
		snapshot:         store.snapshot(),
		selectionOffsets: make(map[string]int),
	}
	manager.setCurrent(cfg)
	return manager, nil
}

func (manager *Manager) Current() Config {
	if manager == nil {
		return DefaultConfig()
	}
	manager.reloadIfChanged(context.Background())
	return manager.currentConfig()
}

// ComputerUseMode 返回当前的 ComputerUse 执行模式与浏览器初始 URL。
// 供 forwarder 注入点按模式选择本地执行后端（desktop/browser）。
// 返回基础类型避免 config<->forwarder 循环依赖。
func (manager *Manager) ComputerUseMode() (mode string, browserStartURL string) {
	cfg := manager.Current().ComputerUse
	return cfg.Mode, cfg.BrowserStartURL
}

func (manager *Manager) currentConfig() Config {
	if manager == nil {
		return DefaultConfig()
	}
	if current := manager.current.Load(); current != nil {
		return cloneConfig(*current)
	}
	return DefaultConfig()
}

func (manager *Manager) Load(ctx context.Context) (Config, error) {
	if manager == nil {
		return DefaultConfig(), nil
	}
	manager.reloadIfChanged(ctx)
	return manager.currentConfig(), nil
}

func (manager *Manager) Save(ctx context.Context, cfg Config) (Config, error) {
	if manager == nil || manager.store == nil {
		return Config{}, fmt.Errorf("config manager is not initialized")
	}
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	// Workspace MCP trust is changed only through the explicit grant/revoke
	// methods. A stale full settings snapshot must not silently alter it.
	cfg.MCPTrustGrants = cloneMCPTrustRecords(manager.currentConfig().MCPTrustGrants)
	return manager.saveLocked(ctx, cfg)
}

// saveLocked 在 saveMu 持有期间执行落盘与状态更新（公开 Save 与读-改-写持久化方法共用）。
func (manager *Manager) saveLocked(ctx context.Context, cfg Config) (Config, error) {
	normalized, err := manager.store.Save(ctx, cfg)
	if err != nil {
		return Config{}, err
	}
	manager.setCurrent(normalized)
	manager.reloadMu.Lock()
	manager.snapshot = manager.store.snapshot()
	manager.lastReload = time.Now()
	manager.reloadError = ""
	manager.reloadMu.Unlock()
	manager.notify(normalized)
	return normalized, nil
}

func (manager *Manager) LastAgentModelHash() string {
	return strings.TrimSpace(manager.Current().LastAgentModelHash)
}

func (manager *Manager) GetDelegationConfig(ctx context.Context) (DelegationConfig, error) {
	if manager == nil {
		return cloneDelegationConfig(DefaultConfig().Delegation), nil
	}
	cfg, err := manager.Load(ctx)
	if err != nil {
		return cloneDelegationConfig(DefaultConfig().Delegation), err
	}
	return cloneDelegationConfig(cfg.Delegation), nil
}

func (manager *Manager) SaveDelegationConfig(ctx context.Context, cfg DelegationConfig) (DelegationConfig, error) {
	if manager == nil {
		return DelegationConfig{}, fmt.Errorf("config manager is not initialized")
	}
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	current, err := manager.Load(ctx)
	if err != nil {
		return cloneDelegationConfig(DefaultConfig().Delegation), err
	}
	current.Delegation = cloneDelegationConfig(cfg)
	normalized, err := manager.saveLocked(ctx, current)
	if err != nil {
		return cloneDelegationConfig(DefaultConfig().Delegation), err
	}
	return cloneDelegationConfig(normalized.Delegation), nil
}

// PricingRates 返回当前配置中各模型适配器的价格条目快照，供费用估算使用。
func (manager *Manager) PricingRates() []historymetrics.PriceRate {
	if manager == nil {
		return nil
	}
	rates := make([]historymetrics.PriceRate, 0)
	for _, adapter := range manager.Current().ModelAdapters {
		pricing := adapter.Pricing
		if pricing == nil {
			continue
		}
		rates = append(rates, historymetrics.PriceRate{
			Model:      adapter.ModelID,
			Provider:   adapter.Type,
			BaseURL:    adapter.BaseURL,
			Input:      pricing.Input,
			Output:     pricing.Output,
			CacheRead:  pricing.CacheRead,
			CacheWrite: pricing.CacheWrite,
		})
	}
	return rates
}

// GoalRuntimeConfig 返回 goal 循环执行的运行时配置（forwarder 消费）。
func (manager *Manager) GoalRuntimeConfig() forwarder.GoalRuntimeConfig {
	cfg := forwarder.GoalRuntimeConfig{
		MaxProviderPasses: 30,
		SelfCheckPasses:   2,
		VerifyMaxRetries:  3,
		ErrorMaxRetries:   3,
		ProgressInterval:  5,
	}
	if manager == nil {
		return cfg
	}
	current := manager.Current().Goal
	cfg.Enabled = current.Enabled
	cfg.MaxProviderPasses = current.MaxProviderPasses
	cfg.MaxDuration = time.Duration(current.MaxDurationSeconds) * time.Second
	cfg.MaxCostUSD = current.MaxCostUSD
	cfg.SelfCheckPasses = current.SelfCheckPasses
	cfg.VerifyMaxRetries = current.VerifyMaxRetries
	cfg.ErrorMaxRetries = current.ErrorMaxRetries
	cfg.ProgressInterval = current.ProgressInterval
	return cfg
}

func (manager *Manager) DelegationRuntimeConfig() delegation.RuntimeConfig {
	config := manager.Current()
	current := config.Delegation
	result := delegation.RuntimeConfig{
		Enabled:                 current.Enabled,
		MaxConcurrency:          current.MaxConcurrency,
		Groups:                  make([]delegation.RuntimeModelGroup, 0, len(current.Groups)),
		ModelNames:              make(map[string]string),
		SupervisionEnabled:      current.Supervision.Enabled,
		SupervisorModelID:       strings.TrimSpace(current.Supervision.SupervisorModelID),
		ReviewerModelID:         strings.TrimSpace(current.Supervision.ReviewerModelID),
		WorkerGroupID:           strings.TrimSpace(current.Supervision.WorkerGroupID),
		MaxCorrections:          current.Supervision.MaxCorrections,
		MaxRetries:              current.Supervision.MaxRetries,
		MaxRounds:               current.Supervision.MaxRounds,
		AllowReassign:           current.Supervision.AllowReassign,
		AllowEscalate:           current.Supervision.AllowEscalate,
		StrictUnavailable:       current.Supervision.StrictUnavailable,
		VisionDelegationEnabled: current.VisionDelegation.Enabled,
		VisionModelID:           strings.TrimSpace(current.VisionDelegation.VisionModelID),
		VisionMode:              strings.TrimSpace(current.VisionDelegation.Mode),
		SubagentProfiles:        delegationSubagentProfileMap(current.SubagentProfiles),
		ExecutorFailoverLimit:   current.ExecutorFailoverLimit,
		Executors:               make([]delegation.RuntimeExecutorConfig, 0, len(current.Executors)),
	}
	for _, adapter := range config.ModelAdapters {
		adapterID := strings.TrimSpace(adapter.ID)
		if adapterID == "" {
			continue
		}
		result.ModelNames[adapterID] = firstNonEmptyConfigValue(adapter.DisplayName, adapter.ModelID, adapterID)
	}
	for _, group := range current.Groups {
		result.Groups = append(result.Groups, delegation.RuntimeModelGroup{
			ID:              strings.TrimSpace(group.ID),
			Name:            strings.TrimSpace(group.Name),
			Enabled:         group.Enabled,
			ModelIDs:        append([]string(nil), group.ModelIDs...),
			DefaultModelID:  strings.TrimSpace(group.DefaultModelID),
			ExecutionMode:   delegation.NormalizeExecutionMode(group.ExecutionMode),
			ToolPermissions: cloneBoolMap(group.ToolPermissions),
		})
	}
	for _, executor := range current.Executors {
		result.Executors = append(result.Executors, delegation.RuntimeExecutorConfig{
			ID:                      delegation.ExecutorID(executor.ID),
			Kind:                    executor.Kind,
			DisplayName:             executor.DisplayName,
			Enabled:                 executor.Enabled,
			Priority:                executor.Priority,
			Executable:              executor.Executable,
			ProbeTimeoutSeconds:     executor.ProbeTimeoutSeconds,
			ExecutionTimeoutSeconds: executor.ExecutionTimeoutSeconds,
			EnvironmentVariables:    append([]string(nil), executor.EnvironmentVariables...),
			Options:                 cloneStringMap(executor.Options),
		})
	}
	return delegation.NormalizeRuntimeConfig(result)
}

func firstNonEmptyConfigValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// delegationSubagentProfileMap 把子代理角色覆盖列表归一为 map（subagentType → 片段）。
func delegationSubagentProfileMap(items []SubagentProfileOverride) map[string]string {
	if len(items) == 0 {
		return nil
	}
	result := make(map[string]string, len(items))
	for _, item := range items {
		subagentType := strings.TrimSpace(item.SubagentType)
		if subagentType == "" {
			continue
		}
		result[subagentType] = strings.TrimSpace(item.PromptFragment)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// SkillMCPScanEnabled 返回 Skills/MCP 扫描总开关（满足 forwarder.scanConfig 接口）。
func (manager *Manager) SkillMCPScanEnabled() bool {
	return manager.Current().SkillMCPScan.Enabled
}

func (manager *Manager) SkillMCPScanSkillSources() map[string]bool {
	return manager.Current().SkillMCPScan.SkillSources
}

func (manager *Manager) SkillMCPScanMCPSources() map[string]bool {
	return manager.Current().SkillMCPScan.MCPSources
}

func (manager *Manager) SkillMCPScanEnabledSkills() map[string]bool {
	return manager.Current().SkillMCPScan.EnabledSkills
}

func (manager *Manager) SkillMCPScanDisabledMCPServers() map[string]bool {
	return manager.Current().SkillMCPScan.DisabledMCPServers
}

func (manager *Manager) SkillMCPScanTrustRecords() []forwarder.MCPTrustRecord {
	return append([]forwarder.MCPTrustRecord(nil), manager.Current().MCPTrustGrants...)
}

// PersistChannelMaxTokensCap 将 provider 反馈的 max_tokens 上限持久化到指定渠道。
// 只更新命中 channelID 的配置，并且不会把已有更小的限制放大。
func (manager *Manager) PersistChannelMaxTokensCap(ctx context.Context, channelID string, maxTokens int) error {
	if manager == nil {
		return fmt.Errorf("config manager is not initialized")
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || maxTokens <= 0 {
		return nil
	}
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	current := manager.Current()
	matched := false
	for i := range current.ModelAdapters {
		item := &current.ModelAdapters[i]
		if strings.TrimSpace(item.ID) != channelID {
			continue
		}
		matched = true
		if item.MaxCompletionTokens <= 0 || maxTokens < item.MaxCompletionTokens {
			item.MaxCompletionTokens = maxTokens
		}
		if item.AnthropicMaxTokens <= 0 || maxTokens < item.AnthropicMaxTokens {
			item.AnthropicMaxTokens = maxTokens
		}
		break
	}
	if !matched {
		return nil
	}
	_, err := manager.saveLocked(ctx, current)
	return err
}

// PersistChannelContextWindow 把中转站自适应探测到的真实上下文窗口写回某个 adapter 条目。
// 仅在该值小于当前配置值时下调（中转站限制通常比 catalog 理论值更小）；
// 上调不在此处理（避免把探测偏差写大）。粒度是「某一用户的某一供应商的某一模型」，不影响全局。
func (manager *Manager) PersistChannelContextWindow(ctx context.Context, channelID string, contextWindowTokens int) error {
	if manager == nil {
		return fmt.Errorf("config manager is not initialized")
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || contextWindowTokens <= 0 {
		return nil
	}
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	current := manager.Current()
	matched := false
	for i := range current.ModelAdapters {
		item := &current.ModelAdapters[i]
		if strings.TrimSpace(item.ID) != channelID {
			continue
		}
		matched = true
		// 仅下调：保留用户手动设置的更大值，只在中转站实测更小时收敛。
		if item.ContextWindowTokens <= 0 || contextWindowTokens < item.ContextWindowTokens {
			item.ContextWindowTokens = contextWindowTokens
		}
		break
	}
	if !matched {
		return nil
	}
	_, err := manager.saveLocked(ctx, current)
	return err
}

func (manager *Manager) SaveLastAgentModelHash(ctx context.Context, value string) error {
	if manager == nil {
		return fmt.Errorf("config manager is not initialized")
	}
	normalizedValue := strings.TrimSpace(value)
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	current := manager.Current()
	if strings.TrimSpace(current.LastAgentModelHash) == normalizedValue {
		return nil
	}
	current.LastAgentModelHash = normalizedValue
	_, err := manager.saveLocked(ctx, current)
	return err
}

func (manager *Manager) ProviderStreamIdleTimeout(ctx context.Context) time.Duration {
	if manager == nil {
		return time.Duration(DefaultProviderStreamIdleTimeoutSeconds) * time.Second
	}
	manager.reloadIfChanged(ctx)
	seconds := normalizeProviderStreamIdleTimeout(manager.currentConfig().ProviderStreamIdleTimeout)
	return time.Duration(seconds) * time.Second
}

// TurnStaleTimeout 返回 turn-staleness 看门狗的触发阈值：
// 一轮回合进入「等待外部（工具/交互结果）」后，若在此时长内无任何进展，则触发两段式自救
// （先重对齐 append 序列 + 宽限，再强制收口并自动继续 provider）。读取热加载后的最新配置。
func (manager *Manager) TurnStaleTimeout(ctx context.Context) time.Duration {
	if manager == nil {
		return time.Duration(DefaultTurnStaleTimeoutSeconds) * time.Second
	}
	manager.reloadIfChanged(ctx)
	seconds := normalizeTurnStaleTimeout(manager.currentConfig().TurnStaleTimeout)
	return time.Duration(seconds) * time.Second
}

// NativeDelegationProgressTimeout 返回 native Cursor 子代理「无有效进展」看门狗的触发阈值：
// 子代理既无工具结果、又无模型输出/思考活动超过此时长时判定超时。读取热加载后的最新配置。
func (manager *Manager) NativeDelegationProgressTimeout(ctx context.Context) time.Duration {
	if manager == nil {
		return time.Duration(DefaultNativeDelegationProgressTimeoutSeconds) * time.Second
	}
	manager.reloadIfChanged(ctx)
	seconds := normalizeNativeDelegationProgressTimeout(manager.currentConfig().NativeDelegationProgressTimeout)
	return time.Duration(seconds) * time.Second
}

// LocalResponseCacheSettings 返回本地响应缓存的当前配置：是否启用、TTL、最大条目数
// 与是否持久化到磁盘。该方法读取热加载后的最新配置，供 provider 网关按调用即时判断。
func (manager *Manager) LocalResponseCacheSettings() (enabled bool, ttl time.Duration, maxEntries int, persist bool) {
	if manager == nil {
		return false, time.Duration(DefaultLocalResponseCacheTTLSeconds) * time.Second, DefaultLocalResponseCacheMaxEntries, true
	}
	cfg := manager.Current().LocalResponseCache
	ttlSeconds := cfg.TTLSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = DefaultLocalResponseCacheTTLSeconds
	}
	entries := cfg.MaxEntries
	if entries <= 0 {
		entries = DefaultLocalResponseCacheMaxEntries
	}
	persist = cfg.Persist
	if !cfg.Enabled && cfg.TTLSeconds == 0 && cfg.MaxEntries == 0 && !persist {
		persist = true
	}
	return cfg.Enabled, time.Duration(ttlSeconds) * time.Second, entries, persist
}

func (manager *Manager) IsObservabilityLogEnabled(ctx context.Context) bool {
	if manager == nil {
		return false
	}
	manager.reloadIfChanged(ctx)
	if current := manager.current.Load(); current != nil {
		return current.Log
	}
	return false
}

// DebugLogMaxBytes 返回单个 debug jsonl 文件的字节上限（热加载生效）。
// 0 表示用默认值，负数表示不限制。
func (manager *Manager) DebugLogMaxBytes(ctx context.Context) int {
	if manager == nil {
		return 0
	}
	manager.reloadIfChanged(ctx)
	if current := manager.current.Load(); current != nil {
		return current.DebugLogMaxBytes
	}
	return 0
}

// MirrorCaptureEnabled 返回镜像记录开关（热加载生效）。
func (manager *Manager) MirrorCaptureEnabled(ctx context.Context) bool {
	if manager == nil {
		return false
	}
	manager.reloadIfChanged(ctx)
	return manager.currentConfig().MirrorCapture.Enabled
}

// MirrorCaptureHosts 返回镜像记录域名列表；空配置回落默认列表。
func (manager *Manager) MirrorCaptureHosts() []string {
	if manager == nil {
		return nil
	}
	hosts := manager.currentConfig().MirrorCapture.Hosts
	if len(hosts) == 0 {
		return DefaultMirrorHosts
	}
	return hosts
}

// RoutingMode 返回代理请求分流模式，并在读取前检查配置热加载。
func (manager *Manager) RoutingMode(ctx context.Context) string {
	if manager == nil {
		return DefaultRoutingMode
	}
	manager.reloadIfChanged(ctx)
	return manager.currentConfig().Routing.Mode
}

func (manager *Manager) Subscribe(listener func(Config)) func() {
	if manager == nil || listener == nil {
		return func() {}
	}
	manager.listenersMu.Lock()
	manager.listeners = append(manager.listeners, listener)
	index := len(manager.listeners) - 1
	manager.listenersMu.Unlock()
	return func() {
		manager.listenersMu.Lock()
		defer manager.listenersMu.Unlock()
		if index < 0 || index >= len(manager.listeners) {
			return
		}
		manager.listeners[index] = nil
	}
}

func (manager *Manager) LegacyRuntimeSnapshot(_ context.Context) (legacyruntime.RuntimeConfigSnapshot, error) {
	cfg := manager.Current()
	adapters := make([]legacyruntime.ModelAdapterConfig, 0, len(cfg.ModelAdapters))
	for _, item := range cfg.ModelAdapters {
		adapters = append(adapters, legacyruntime.ModelAdapterConfig{
			ID:              item.ID,
			Source:          item.Source,
			CredentialScope: item.CredentialScope,
			DisplayName:     item.DisplayName,
			GroupName:       item.GroupName,
			Type:            item.Type,
			SupplierID:      item.SupplierID,
			ProtocolMode:    item.ProtocolMode,

			ProtocolGroup:               item.ProtocolGroup,
			BaseURL:                     item.BaseURL,
			APIKey:                      item.APIKey,
			TooltipData:                 item.TooltipData,
			ModelID:                     item.ModelID,
			ReasoningEffort:             item.ReasoningEffort,
			OpenAIEndpoint:              item.OpenAIEndpoint,
			OpenAIRequestGroup:          item.OpenAIRequestGroup,
			OpenAIExtraParamsEnabled:    item.OpenAIExtraParamsEnabled,
			OpenAIExtraParamsJSON:       item.OpenAIExtraParamsJSON,
			CustomHeadersEnabled:        item.CustomHeadersEnabled,
			CustomHeadersJSON:           item.CustomHeadersJSON,
			AnthropicExtraParamsEnabled: item.AnthropicExtraParamsEnabled,
			AnthropicExtraParamsJSON:    item.AnthropicExtraParamsJSON,
			ContextWindowTokens:         item.ContextWindowTokens,
			MaxCompletionTokens:         item.MaxCompletionTokens,
			AnthropicMaxTokens:          item.AnthropicMaxTokens,
			AnthropicThinkingEffort:     item.AnthropicThinkingEffort,
			ThinkingBudgetTokens:        item.ThinkingBudgetTokens,
			Pricing:                     item.Pricing,
			FastMode:                    item.FastMode,
			OpenAIServiceTier:           item.OpenAIServiceTier,
		})
	}
	return legacyruntime.RuntimeConfigSnapshot{
		ObservabilityLogEnabled:   cfg.Log,
		ProviderStreamIdleTimeout: cfg.ProviderStreamIdleTimeout,
		ModelAdapters:             adapters,
	}, nil
}

func (manager *Manager) setCurrent(cfg Config) {
	next := cloneConfig(cfg)
	manager.current.Store(&next)
}

func cloneConfig(input Config) Config {
	output := input
	output.SkillMCPScan = cloneSkillMCPScanConfig(input.SkillMCPScan)
	output.MCPTrustGrants = cloneMCPTrustRecords(input.MCPTrustGrants)
	return output
}

func cloneSkillMCPScanConfig(input SkillMCPScanConfig) SkillMCPScanConfig {
	output := input
	output.SkillSources = cloneBoolMap(input.SkillSources)
	output.MCPSources = cloneBoolMap(input.MCPSources)
	output.EnabledSkills = cloneBoolMap(input.EnabledSkills)
	output.DisabledSkills = cloneBoolMap(input.DisabledSkills)
	output.DisabledMCPServers = cloneBoolMap(input.DisabledMCPServers)
	output.SkillSummaries = cloneStringMap(input.SkillSummaries)
	output.MCPSummaries = cloneStringMap(input.MCPSummaries)
	return output
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneMCPTrustRecords(input []forwarder.MCPTrustRecord) []forwarder.MCPTrustRecord {
	return append([]forwarder.MCPTrustRecord(nil), input...)
}

func (manager *Manager) GrantMCPServerTrust(ctx context.Context, workspaceScope string, identifier string, fingerprint string) error {
	if manager == nil || manager.store == nil {
		return fmt.Errorf("config manager is not initialized")
	}
	grantList := normalizeMCPTrustGrants([]forwarder.MCPTrustRecord{{
		RuntimeScope: workspaceScope,
		Identifier:   identifier,
		Fingerprint:  fingerprint,
	}})
	if len(grantList) != 1 {
		return fmt.Errorf("valid workspace MCP trust identity is required")
	}
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	current := manager.currentConfig()
	current.MCPTrustGrants = normalizeMCPTrustGrants(append(current.MCPTrustGrants, grantList[0]))
	_, err := manager.saveLocked(ctx, current)
	return err
}

func (manager *Manager) RevokeMCPServerTrust(ctx context.Context, workspaceScope string, identifier string) error {
	if manager == nil || manager.store == nil {
		return fmt.Errorf("config manager is not initialized")
	}
	workspaceScope = normalizeMCPTrustWorkspaceScope(workspaceScope)
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if workspaceScope == "" || identifier == "" {
		return fmt.Errorf("valid workspace MCP trust identity is required")
	}
	manager.saveMu.Lock()
	defer manager.saveMu.Unlock()
	current := manager.currentConfig()
	next := make([]forwarder.MCPTrustRecord, 0, len(current.MCPTrustGrants))
	for _, grant := range current.MCPTrustGrants {
		if grant.RuntimeScope == workspaceScope && grant.Identifier == identifier {
			continue
		}
		next = append(next, grant)
	}
	current.MCPTrustGrants = next
	_, err := manager.saveLocked(ctx, current)
	return err
}

func (manager *Manager) HasMCPServerTrust(workspaceScope string, identifier string, fingerprint string) bool {
	wanted := normalizeMCPTrustGrants([]forwarder.MCPTrustRecord{{
		RuntimeScope: workspaceScope,
		Identifier:   identifier,
		Fingerprint:  fingerprint,
	}})
	if len(wanted) != 1 {
		return false
	}
	for _, grant := range manager.Current().MCPTrustGrants {
		if grant == wanted[0] {
			return true
		}
	}
	return false
}

func (manager *Manager) reloadIfChanged(ctx context.Context) {
	if manager == nil || manager.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	manager.reloadMu.Lock()
	if !manager.lastReload.IsZero() && now.Sub(manager.lastReload) < configHotReloadMinInterval {
		manager.reloadMu.Unlock()
		return
	}
	manager.lastReload = now
	nextSnapshot := manager.store.snapshot()
	if nextSnapshot == manager.snapshot {
		manager.reloadMu.Unlock()
		return
	}
	cfg, err := manager.store.Load(ctx)
	if err != nil {
		errText := err.Error()
		if errText != manager.reloadError {
			logger.Errorf("config hot reload skipped path=%s error=%v", manager.store.Path(), err)
			manager.reloadError = errText
		}
		manager.reloadMu.Unlock()
		return
	}
	manager.snapshot = nextSnapshot
	manager.reloadError = ""
	manager.setCurrent(cfg)
	manager.reloadMu.Unlock()
	manager.notify(cfg)
}

func (manager *Manager) notify(cfg Config) {
	manager.listenersMu.RLock()
	listeners := append([]func(Config){}, manager.listeners...)
	manager.listenersMu.RUnlock()
	for _, listener := range listeners {
		if listener != nil {
			listener(cfg)
		}
	}
}
