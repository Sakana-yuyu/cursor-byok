package bridge

import (
	"context"
	"cursor/internal/appdata"
	"cursor/internal/backend/forwarder"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/certs"
	"cursor/internal/client"
	"cursor/internal/cursor"
	"cursor/internal/logger"
	"cursor/internal/mitm"
	"cursor/internal/promptinject"
	"cursor/internal/terminalenv"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed vision_mcp_server.py
var bundledVisionMCPScript []byte

// Public DTOs remain in package main for Wails service compatibility.
// ProxyState 定义了当前模块中的 ProxyState 类型。
type ProxyState = client.ProxyState

// UserConfig 定义了当前模块中的 UserConfig 类型。
type UserConfig = client.UserConfig

// ModelAdapterConfig 定义模型测速使用的模型配置结构。
type ModelAdapterConfig = serverconfig.ModelAdapterConfig

// DelegationConfig 定义 Multitask 委派与监督配置结构。
type DelegationConfig = serverconfig.DelegationConfig

// ModelAdapterTestResult 定义一次模型测速结果。
type ModelAdapterTestResult = client.ModelAdapterTestResult

// ModelAdapterTestResultsPayload 定义测速结果事件载荷。
type ModelAdapterTestResultsPayload = client.ModelAdapterTestResultsPayload

// ModelCatalogRequest 定义模型列表拉取请求。
type ModelCatalogRequest = client.ModelCatalogRequest

// ModelCatalogItem 定义模型列表项。
type ModelCatalogItem = client.ModelCatalogItem

// ModelCatalogResult 定义模型列表结果。
type ModelCatalogResult = client.ModelCatalogResult

// AutoMatchResult 定义一次「自动配对上下文窗口」的汇总结果。
type AutoMatchResult = client.AutoMatchResult

// DiagnosticResult 定义模型适配器诊断结果。
type DiagnosticResult = serverconfig.DiagnosticResult

// ModelAdapterProbeResult 定义一次轻量模型可用性探测结果。
type ModelAdapterProbeResult = client.ModelAdapterProbeResult

// ProviderBalanceRequest 定义中转站余额查询请求。
type ProviderBalanceRequest = client.ProviderBalanceRequest

// ProviderBalance 定义中转站余额查询结果。
type ProviderBalance = client.ProviderBalance

// PromptInjectionConfig 定义提示词注入设置。不会包含模型 API key。
type PromptInjectionConfig = promptinject.Config

// PromptInjectionStatus 定义提示词注入状态。
type PromptInjectionStatus = promptinject.Status

// DelegationTaskSnapshot is the desktop-safe Multitask worker state.
type DelegationTaskSnapshot = forwarder.DelegationTaskSnapshot

// LicenseActionRequest 定义了当前模块中的 LicenseActionRequest 类型。
type LicenseActionRequest = client.LicenseActionRequest

// LicenseSwitchDeviceRequest 定义了当前模块中的 LicenseSwitchDeviceRequest 类型。
type LicenseSwitchDeviceRequest = client.LicenseSwitchDeviceRequest

// LicenseAPIResult 定义了当前模块中的 LicenseAPIResult 类型。
type LicenseAPIResult = client.LicenseAPIResult

// UsageRecordsRequest 定义了当前模块中的 UsageRecordsRequest 类型。
type UsageRecordsRequest = client.UsageRecordsRequest

// UsageRecord 定义了当前模块中的 UsageRecord 类型。
type UsageRecord = client.UsageRecord

// UsageRecordsData 定义了当前模块中的 UsageRecordsData 类型。
type UsageRecordsData = client.UsageRecordsData

// UsageRecordsResult 定义了当前模块中的 UsageRecordsResult 类型。
type UsageRecordsResult = client.UsageRecordsResult

// ProxyService 定义了当前模块中的 ProxyService 类型。
type ProxyService struct {
	// core 表示当前声明中的 core。
	core            *client.ProxyService
	promptInjection *promptinject.Manager
	mcpConnectMu    sync.Mutex
	mcpConnects     map[string]mcpConnectHandle
	mcpConnectSeq   uint64

	// caRepairedAt 记录本次启动 CA 材料被自动修复（cert/key 失配重建）的时间；
	// 零值表示未发生自动修复，前端据此展示「重启 Cursor」提示。
	caRepairedAt time.Time
}

type mcpConnectHandle struct {
	identifier   string
	runtimeScope string
	generation   uint64
	cancel       context.CancelFunc
}

// NewProxyService 用于处理与 NewProxyService 相关的逻辑。
func NewProxyService(proxy *mitm.ProxyServer, certManager *certs.Manager, caCertPEM []byte) *ProxyService {
	manager := promptinject.New()
	if _, err := manager.Load(); err != nil {
		manager = promptinject.New()
	}
	return &ProxyService{
		core:            client.NewProxyService(proxy, certManager, caCertPEM),
		promptInjection: manager,
		mcpConnects:     make(map[string]mcpConnectHandle),
		caRepairedAt:    certManagerRepairedAt(certManager),
	}
}

// certManagerRepairedAt 读取 certManager 的自动修复标记（certManager 可能为 nil）。
func certManagerRepairedAt(certManager *certs.Manager) time.Time {
	if certManager == nil {
		return time.Time{}
	}
	return certManager.RepairedAt
}

// StartProxy 用于处理与 StartProxy 相关的逻辑。
func (s *ProxyService) StartProxy() (ProxyState, error) {
	return s.core.StartProxy()
}

// StopProxy 用于处理与 StopProxy 相关的逻辑。
func (s *ProxyService) StopProxy() (ProxyState, error) {
	return s.core.StopProxy()
}

// ResetUsageMetrics 清空会话与站点消耗共用的用量记录。
func (s *ProxyService) ResetUsageMetrics() error {
	return s.core.ResetUsageMetrics()
}

// GetHistorySessions returns metadata for every retained history session.
func (s *ProxyService) GetHistorySessions() ([]HistorySession, error) {
	var isActive func(conversationID, requestID string) bool
	if s != nil && s.core != nil {
		isActive = s.core.HasActiveConversation
	}
	return scanHistorySessions(isActive)
}

// DeleteHistorySessions removes the given history sessions (UUID ids).
func (s *ProxyService) DeleteHistorySessions(sessionIDs []string) error {
	return deleteHistorySessions(sessionIDs)
}

// ClearHistory removes every history session plus orphan debug data and resets usage stats.
// Returns the number of removed session directories.
func (s *ProxyService) ClearHistory() (int, error) {
	return clearHistory(s.ResetUsageMetrics)
}

// DeleteHistoryDebugLogs 只删除指定会话的调试日志（debug 子目录），保留会话本体。
// 返回释放的字节数。
func (s *ProxyService) DeleteHistoryDebugLogs(sessionIDs []string) (int64, error) {
	return deleteHistoryDebugLogs(sessionIDs)
}

// PurgeAllHistoryDebugLogs 清理全部调试日志（含无会话归属的孤儿日志），保留会话本体。
// 返回释放的字节数。前端无需先枚举会话 ID。
func (s *ProxyService) PurgeAllHistoryDebugLogs() (int64, error) {
	return purgeAllHistoryDebugLogs()
}

// GetHistoryDebugUsage 返回所有调试日志的总占用字节数（含孤儿日志）。
func (s *ProxyService) GetHistoryDebugUsage() (int64, error) {
	return historyDebugUsage()
}

// ExportSessionDebugBundle 把指定会话的排查证据（state.json、context.json、debug/*）
// 打包成 zip，返回 zip 文件路径。会话目录或 debug 子目录不存在时返回明确错误。
func (s *ProxyService) ExportSessionDebugBundle(sessionID string) (string, error) {
	return exportSessionDebugBundle(sessionID)
}

// ListSessionDebugFiles 列出指定会话 debug 子目录下的文件元信息（名字/大小/mtime）。
// debug 目录不存在时返回空切片。
func (s *ProxyService) ListSessionDebugFiles(sessionID string) ([]SessionDebugFile, error) {
	return listSessionDebugFiles(sessionID)
}

// ReadSessionDebugTail 只读指定会话 debug 文件的尾部内容。
// filename 必须命中固定白名单，否则拒绝；maxBytes<=0 时使用默认 64KiB。
func (s *ProxyService) ReadSessionDebugTail(sessionID, filename string, maxBytes int64) (string, error) {
	return readSessionDebugTail(sessionID, filename, maxBytes)
}

// GetDelegationTaskSnapshots returns retained Multitask worker state.
func (s *ProxyService) GetDelegationTaskSnapshots() []DelegationTaskSnapshot {
	return s.core.GetDelegationTaskSnapshots()
}

// GetGoals 返回当前 forwarder 的 goal 状态快照。
func (s *ProxyService) GetGoals() []forwarder.GoalSnapshot {
	return s.core.GetGoals()
}

// StartGoal 以 goal 模式启动新会话，返回 conversationID。
func (s *ProxyService) StartGoal(goalText, modelID string) (string, error) {
	return s.core.StartGoal(goalText, modelID)
}

// StopGoal 停止指定会话的 goal 执行。
func (s *ProxyService) StopGoal(conversationID string) error {
	return s.core.StopGoal(conversationID)
}

// GetDelegationConfig returns the normalized delegation settings subtree.
func (s *ProxyService) GetDelegationConfig() (DelegationConfig, error) {
	return s.core.GetDelegationConfig()
}

// SaveDelegationConfig persists only the delegation settings subtree.
// 保存后若视觉委派已启用，自动把识图模型的网关信息同步到读图 MCP（vision-reader）
// 作为 MCP 兜底；同步失败不影响保存结果，仅记录日志。
func (s *ProxyService) SaveDelegationConfig(cfg DelegationConfig) (DelegationConfig, error) {
	saved, err := s.core.SaveDelegationConfig(cfg)
	if err != nil {
		return saved, err
	}
	if err := s.syncVisionReaderFromDelegation(saved); err != nil {
		logger.Errorf("proxy sync vision reader mcp failed: %v", err)
	}
	return saved, nil
}

// CancelDelegationTask cancels one Multitask worker without stopping siblings.
func (s *ProxyService) CancelDelegationTask(taskID string) bool {
	return s.core.CancelDelegationTask(taskID)
}

// GetState 用于处理与 GetState 相关的逻辑。
func (s *ProxyService) GetState() ProxyState {
	return s.core.GetState()
}

// GetTerminalEnvironmentStatus returns the shell and Python 3 that Cursor will use.
func (s *ProxyService) GetTerminalEnvironmentStatus() terminalenv.Status {
	return terminalenv.Detect()
}

// ApplyTerminalEnvironment refreshes the managed terminal and Python settings.
func (s *ProxyService) ApplyTerminalEnvironment() (terminalenv.Status, error) {
	return cursor.EnsureTerminalEnvironmentSettings()
}

// InstallTerminalDependency 通过 winget 异步安装 PowerShell 7 或 Python 3。
// 立即返回（不阻塞 RPC），安装进度通过 wails 事件 terminalenv:install-progress 推送，
// 前端监听该事件刷新进度；收到 stage=done 后应调用 GetTerminalEnvironmentStatus 重新探测。
func (s *ProxyService) InstallTerminalDependency(target string) error {
	normalized := strings.ToLower(strings.TrimSpace(target))
	if normalized != terminalenv.TargetPowerShell && normalized != terminalenv.TargetPython {
		return fmt.Errorf("不支持的安装目标 %q（仅支持 %s / %s）", target, terminalenv.TargetPowerShell, terminalenv.TargetPython)
	}
	go func() {
		if err := terminalenv.Install(context.Background(), normalized); err != nil {
			// 进度与错误已通过事件推送，这里仅记日志，避免 goroutine 静默失败。
			logger.Errorf("InstallTerminalDependency target=%s failed: %v", normalized, err)
		}
	}()
	return nil
}

// PrepareCursorLaunch ensures local-mode Cursor starts only after the proxy
// listener, backend, and Cursor proxy configuration are all ready. Upstream
// mode deliberately bypasses this requirement because it does not use BYOK.
func (s *ProxyService) PrepareCursorLaunch() error {
	if s == nil || s.core == nil {
		return errors.New("本地代理服务未初始化")
	}
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("读取本地代理配置失败: %w", err)
	}
	if cfg.Routing.Mode == "upstream" {
		return nil
	}
	if isCursorProxyReady(s.core.GetState()) {
		return nil
	}
	state, err := s.core.StartProxy()
	if err != nil {
		return fmt.Errorf("启动本地代理失败: %w", err)
	}
	if !isCursorProxyReady(state) {
		return errors.New("本地代理启动后未完成监听或 Cursor 配置校验")
	}
	return nil
}

func isCursorProxyReady(state client.ProxyState) bool {
	return state.BackendRunning && state.ProxyRunning && state.CursorSettingsApplied
}

// RepairProxySettings 一键修复 Cursor 代理配置（重新注入 settings.json 并校验）。
func (s *ProxyService) RepairProxySettings() (client.ProxyRepairResult, error) {
	return s.core.RepairProxySettings()
}

// CARepairResult 是一次「一键修复 CA」的执行结果。
type CARepairResult struct {
	// Repaired 表示是否实际重建了 CA（false 表示材料齐全，无需修复）。
	Repaired bool `json:"repaired"`
	// BackupPath 表示残留文件的备份路径（空表示无需备份）。
	BackupPath string `json:"backupPath"`
	// Detail 表示执行摘要。
	Detail string `json:"detail"`
}

// CARepairStatus 是前端查询「本次启动 CA 是否被自动修复」的状态（只读）。
type CARepairStatus struct {
	// Repaired 表示本次启动检测到 CA 材料异常并已自动修复（cert/key 失配重建）。
	Repaired bool `json:"repaired"`
	// RepairedAt 表示自动修复发生的时间（RFC3339，空表示未修复）。
	RepairedAt string `json:"repairedAt"`
	// Detail 表示给用户看的提示文案。
	Detail string `json:"detail"`
}

// RepairCACorruption 一键修复 CA 材料不完整：把残留文件备份改名后重新生成 CA 落盘。
// 修复后需重启应用使新 CA 生效（本地代理与 MITM 在启动时构建）。
func (s *ProxyService) RepairCACorruption() (CARepairResult, error) {
	if s == nil {
		return CARepairResult{}, errors.New("proxy service is not initialized")
	}
	backup, err := certs.RepairIncompleteCA(appdata.CACertFilePath(), appdata.CAKeyFilePath())
	if err != nil {
		return CARepairResult{}, fmt.Errorf("repair CA: %w", err)
	}
	if backup == "" {
		return CARepairResult{
			Repaired: false,
			Detail:   "CA 材料完整，无需修复",
		}, nil
	}
	return CARepairResult{
		Repaired:   true,
		BackupPath: backup,
		Detail:     "已备份残留文件并重新生成 CA，重启应用后生效",
	}, nil
}

// GetCARepairStatus 返回本次启动 CA 是否被自动修复（失配重建）的状态。
// 自动修复只发生在启动时（NewPersistentManager 检测到 cert/key 失配），
// 新 CA 已重新落盘并重装信任，但 Cursor 客户端需重启才能重新连接。
func (s *ProxyService) GetCARepairStatus() CARepairStatus {
	if s == nil || s.caRepairedAt.IsZero() {
		return CARepairStatus{Repaired: false}
	}
	return CARepairStatus{
		Repaired:   true,
		RepairedAt: s.caRepairedAt.Format(time.RFC3339),
		Detail:     "检测到本地 CA 异常，已自动修复，请重启 Cursor 使连接生效",
	}
}

// MarkCAIncomplete 记录 CA 初始化失败状态（应用降级启动时由 runner 调用），
// 本地代理因此停用，前端据此展示「一键修复」入口。
// 参数为错误信息字符串（error 类型参数会触发 wails bindings 生成器崩溃）。
func (s *ProxyService) MarkCAIncomplete(message string) {
	if s == nil {
		return
	}
	var err error
	if strings.TrimSpace(message) != "" {
		err = errors.New(message)
	}
	s.core.MarkCAIncomplete(err)
}

// ClearLastError 用于处理与 ClearLastError 相关的逻辑。
func (s *ProxyService) ClearLastError() ProxyState {
	return s.core.ClearLastError()
}

// SetBaseURL 用于处理与 SetBaseURL 相关的逻辑。
func (s *ProxyService) SetBaseURL(baseURL string) (ProxyState, error) {
	return s.core.SetBaseURL(baseURL)
}

// LoadUserConfig 用于处理与 LoadUserConfig 相关的逻辑。
func (s *ProxyService) LoadUserConfig() (UserConfig, error) {
	return s.core.LoadUserConfig()
}

// SaveUserConfig 用于处理与 SaveUserConfig 相关的逻辑。
func (s *ProxyService) SaveUserConfig(cfg UserConfig) error {
	return s.core.SaveUserConfig(cfg)
}

// GetCursorAccountStatus 返回 cursor-byok 独立 Cursor 账号的脱敏状态。
func (s *ProxyService) GetCursorAccountStatus() CursorAccountStatus {
	return s.core.GetCursorAccountStatus()
}

// StartCursorAccountLogin 打开官方浏览器登录并异步等待结果。
func (s *ProxyService) StartCursorAccountLogin() (CursorAccountStatus, error) {
	return s.core.StartCursorAccountLogin()
}

// DisconnectCursorAccount 只断开 cursor-byok 自己的账号。
func (s *ProxyService) DisconnectCursorAccount() (CursorAccountStatus, error) {
	return s.core.DisconnectCursorAccount()
}

// TestModelAdapter 用于处理与 TestModelAdapter 相关的逻辑。
func (s *ProxyService) TestModelAdapter(adapter ModelAdapterConfig) (ModelAdapterTestResult, error) {
	return s.core.TestModelAdapter(adapter)
}

// FetchModelCatalog 用于处理与 FetchModelCatalog 相关的逻辑。
func (s *ProxyService) FetchModelCatalog(request ModelCatalogRequest) (ModelCatalogResult, error) {
	return s.core.FetchModelCatalog(request)
}

// AutoMatchContextWindows 自动为所有已存储模型适配器配对正确的上下文窗口：
// 目录命中仅下调（保留用户手动设置的更小窗口），目录未命中则探测 provider /models 回填。
// 供前端「一键自动配对」按钮调用。
// force=true 时无视 autoMatchContextWindow 开关强制执行（供「一键诊断优化」手动触发）。
func (s *ProxyService) AutoMatchContextWindows(ctx context.Context, force bool) (AutoMatchResult, error) {
	return s.core.AutoMatchContextWindows(ctx, force)
}

// DiagnoseModelAdapters 扫描已导入模型的 provider 协议配置。
func (s *ProxyService) DiagnoseModelAdapters() (DiagnosticResult, error) {
	return s.core.DiagnoseModelAdapters()
}

// ApplyDiagnosticFixes 修正用户选中的模型协议配置。
func (s *ProxyService) ApplyDiagnosticFixes(channelIDs []string) (DiagnosticResult, error) {
	return s.core.ApplyDiagnosticFixes(channelIDs)
}

// ProbeModelAdapter 轻量探测单个模型是否可用，用于批量拉取后的可用性体检。
func (s *ProxyService) ProbeModelAdapter(adapter ModelAdapterConfig) ModelAdapterProbeResult {
	return s.core.ProbeModelAdapter(adapter)
}

// QueryProviderBalance 查询中转站余额/额度，失败时返回结构化的 unsupported 结果。
func (s *ProxyService) QueryProviderBalance(request ProviderBalanceRequest) ProviderBalance {
	return s.core.QueryProviderBalance(request)
}

// ProviderBalanceSummaryItem 是首页「站点余额」展示条目：模型通道名 + 余额查询结果。
type ProviderBalanceSummaryItem struct {
	AdapterID   string                 `json:"adapterId"`
	DisplayName string                 `json:"displayName"`
	GroupName   string                 `json:"groupName,omitempty"`
	BaseURL     string                 `json:"baseURL,omitempty"`
	ModelID     string                 `json:"modelID"`
	Balance     client.ProviderBalance `json:"balance"`
}

// hasBalanceQueryCapability 判断模型通道是否配置了余额查询能力。
// auto/空/none 需要至少一个显式凭据才算配置；其余 profile 视为显式启用。
func hasBalanceQueryCapability(adapter serverconfig.ModelAdapterConfig) bool {
	profile := strings.ToLower(strings.TrimSpace(adapter.BalanceProfile))
	if profile == "none" || profile == "" || profile == "auto" {
		return strings.TrimSpace(adapter.BalanceQueryURL) != "" ||
			strings.TrimSpace(adapter.BalanceAccessToken) != "" ||
			strings.TrimSpace(adapter.BalanceUserID) != ""
	}
	return true
}

// QueryAllProviderBalances 汇总所有已配置余额查询的模型通道余额，供首页展示。
// 复用单通道查询的 TTL 缓存与凭据补齐逻辑；未配置余额查询的通道不会发起请求。
// QueryAllProviderBalances 并发查询所有已配置余额能力的供应商余额。
// 每个适配器独立查询并隔离 panic：单个供应商的解析/网络异常绝不崩掉整个进程
// （此前串行同步查询，任何一步 panic 都会导致 Wails 主进程闪退）。
func (s *ProxyService) QueryAllProviderBalances() []ProviderBalanceSummaryItem {
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		return nil
	}
	type adapterJob struct {
		adapter serverconfig.ModelAdapterConfig
	}
	jobs := make([]adapterJob, 0, len(cfg.ModelAdapters))
	for _, adapter := range cfg.ModelAdapters {
		if !hasBalanceQueryCapability(adapter) {
			continue
		}
		jobs = append(jobs, adapterJob{adapter: adapter})
	}
	if len(jobs) == 0 {
		return nil
	}
	const maxParallel = 3
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	results := make([]ProviderBalanceSummaryItem, len(jobs))
	for index, job := range jobs {
		wg.Add(1)
		go func(index int, adapter serverconfig.ModelAdapterConfig) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Infof("bridge query provider balance panic recovered adapter_id=%s panic=%v",
						strings.TrimSpace(adapter.ID), r)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := ProviderBalanceSummaryItem{
				AdapterID:   adapter.ID,
				DisplayName: adapter.DisplayName,
				GroupName:   adapter.GroupName,
				BaseURL:     adapter.BaseURL,
				ModelID:     adapter.ModelID,
			}
			item.Balance = s.queryProviderBalanceSafe(adapter)
			results[index] = item
		}(index, job.adapter)
	}
	wg.Wait()
	out := make([]ProviderBalanceSummaryItem, 0, len(results))
	for _, item := range results {
		if item.AdapterID != "" || item.Balance.Supported || item.Balance.Message != "" {
			out = append(out, item)
		}
	}
	return out
}

// queryProviderBalanceSafe 包装单次余额查询：panic 转为失败结果，保证调用方进程存活。
func (s *ProxyService) queryProviderBalanceSafe(adapter serverconfig.ModelAdapterConfig) (result ProviderBalance) {
	defer func() {
		if r := recover(); r != nil {
			result = ProviderBalance{
				Supported: false,
				Transient: false,
				Message:   fmt.Sprintf("余额查询内部异常：%v", r),
			}
		}
	}()
	return s.core.QueryProviderBalance(ProviderBalanceRequest{
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
	})
}

// GetModelAdapterTestResults 用于处理与 GetModelAdapterTestResults 相关的逻辑。
func (s *ProxyService) GetModelAdapterTestResults() []ModelAdapterTestResult {
	return s.core.GetModelAdapterTestResults()
}

// GetDeviceID 用于处理与 GetDeviceID 相关的逻辑。
func (s *ProxyService) GetDeviceID() (string, error) {
	return s.core.GetDeviceID()
}

// ActivateLicense 用于处理与 ActivateLicense 相关的逻辑。
func (s *ProxyService) ActivateLicense(req LicenseActionRequest) (LicenseAPIResult, error) {
	return s.core.ActivateLicense(req)
}

// BindLicenseDevice 用于处理与 BindLicenseDevice 相关的逻辑。
func (s *ProxyService) BindLicenseDevice(req LicenseActionRequest) (LicenseAPIResult, error) {
	return s.core.BindLicenseDevice(req)
}

// SwitchLicenseDevice 用于处理与 SwitchLicenseDevice 相关的逻辑。
func (s *ProxyService) SwitchLicenseDevice(req LicenseSwitchDeviceRequest) (LicenseAPIResult, error) {
	return s.core.SwitchLicenseDevice(req)
}

// QueryUsageRecords 用于处理与 QueryUsageRecords 相关的逻辑。
func (s *ProxyService) QueryUsageRecords(req UsageRecordsRequest) (UsageRecordsResult, error) {
	return s.core.QueryUsageRecords(req)
}

// ApplyCursorSettings 用于处理与 ApplyCursorSettings 相关的逻辑。
func (s *ProxyService) ApplyCursorSettings() error {
	return s.core.ApplyCursorSettings()
}

// ClearCursorSettings 用于处理与 ClearCursorSettings 相关的逻辑。
func (s *ProxyService) ClearCursorSettings() error {
	return s.core.ClearCursorSettings()
}

// ShutdownForQuit 用于处理与 ShutdownForQuit 相关的逻辑。
func (s *ProxyService) ShutdownForQuit() {
	s.core.ShutdownForQuit()
}

// GetPromptInjectionSettings 返回提示词注入设置；其中不包含任何模型凭据。
func (s *ProxyService) GetPromptInjectionSettings() (PromptInjectionStatus, error) {
	if s == nil || s.promptInjection == nil {
		return PromptInjectionStatus{Config: promptinject.DefaultConfig()}, nil
	}
	return s.promptInjection.Status()
}

// SavePromptInjectionSettings 保存提示词注入设置，默认仍保持关闭。
func (s *ProxyService) SavePromptInjectionSettings(cfg PromptInjectionConfig) (PromptInjectionStatus, error) {
	if s == nil || s.promptInjection == nil {
		return PromptInjectionStatus{}, fmt.Errorf("prompt injection manager is not initialized")
	}
	return s.promptInjection.Save(cfg)
}

// RefreshPromptInjection 拉取用户选择的单个 examples/*.md 文件。
func (s *ProxyService) RefreshPromptInjection() (PromptInjectionStatus, error) {
	if s == nil || s.promptInjection == nil {
		return PromptInjectionStatus{}, fmt.Errorf("prompt injection manager is not initialized")
	}
	return s.promptInjection.Refresh(context.Background())
}

// RefreshPromptInjectionCatalog 拉取仓库 examples 目录及其 Markdown 内容。
func (s *ProxyService) RefreshPromptInjectionCatalog() (PromptInjectionStatus, error) {
	if s == nil || s.promptInjection == nil {
		return PromptInjectionStatus{}, fmt.Errorf("prompt injection manager is not initialized")
	}
	return s.promptInjection.RefreshCatalog(context.Background())
}

// IsWindows 用于处理与 IsWindows 相关的逻辑。
func (s *ProxyService) IsWindows() bool {
	return runtime.GOOS == "windows"
}

// SkillsMCPScanSnapshot 是管理界面展示用的扫描结果快照（技能 + MCP server）。
type SkillsMCPScanSnapshot struct {
	Skills     []forwarder.SkillSnapshotItem     `json:"skills"`
	MCPServers []forwarder.MCPServerSnapshotItem `json:"mcpServers"`
	Config     serverconfig.SkillMCPScanConfig   `json:"config"`
}

// GetSkillsMCPScanSnapshot 扫描各工具的 Skills/MCP 配置，返回去重分类后的快照及当前开关配置。
// workspaceRoot 为空时仅扫描用户级目录。供管理界面「Skills & MCP」tab 展示。
func (s *ProxyService) GetSkillsMCPScanSnapshot(workspaceRoot string) (SkillsMCPScanSnapshot, error) {
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		cfg = serverconfig.DefaultConfig()
	}
	return SkillsMCPScanSnapshot{
		Skills:     forwarder.SnapshotSourcedSkills(workspaceRoot),
		MCPServers: forwarder.SnapshotMCPServersWithSettings(workspaceRoot, skillMCPScanSettings(cfg.SkillMCPScan)),
		Config:     cfg.SkillMCPScan,
	}, nil
}

// RefreshSkillsMCPScan 清除扫描缓存并重新扫描，返回最新快照。供「重新扫描」按钮调用。
func (s *ProxyService) RefreshSkillsMCPScan(workspaceRoot string) (SkillsMCPScanSnapshot, error) {
	forwarder.InvalidateSkillScanCache()
	forwarder.InvalidateMCPScanCache()
	return s.GetSkillsMCPScanSnapshot(workspaceRoot)
}

// SaveSkillsMCPScanConfig 单独保存 Skills/MCP 扫描配置（合并进现有 config 后落盘）。
func (s *ProxyService) SaveSkillsMCPScanConfig(scanCfg serverconfig.SkillMCPScanConfig) error {
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		return err
	}
	cfg.SkillMCPScan = scanCfg
	return s.core.SaveUserConfig(cfg)
}

// ReaderMCPResult 是一键启用读图 MCP 的返回结果。
type ReaderMCPResult struct {
	Identifier string `json:"identifier"`
	ScriptPath string `json:"scriptPath"`
	WasAdded   bool   `json:"wasAdded"`
}

// readerMCPIdentifier 是写入 Cursor mcp.json 的读图 MCP 服务名。
const readerMCPIdentifier = "vision-reader"

// readerMCPBundledScriptName 是内置读图 MCP 脚本的文件名（go:embed vision_mcp_server.py）。
const readerMCPBundledScriptName = "vision_mcp_server.py"

// readerMCPScriptCandidates 返回 vision_mcp_server.py 可能存在的路径
// （image-see 技能会同步到 .claude/.cursor/.codex 三个 skills 目录）。
func readerMCPScriptCandidates() []string {
	home, _ := os.UserHomeDir()
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude", "skills", "image-see", "scripts", "vision_mcp_server.py"),
		filepath.Join(home, ".cursor", "skills", "image-see", "scripts", "vision_mcp_server.py"),
		filepath.Join(home, ".codex", "skills", "image-see", "scripts", "vision_mcp_server.py"),
	}
}

// detectVisionReaderScript 探测本地已存在的读图 MCP 脚本，返回第一个存在的绝对路径；不存在返回空串。
func detectVisionReaderScript() string {
	for _, candidate := range readerMCPScriptCandidates() {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// ensureVisionReaderScript 返回读图 MCP 脚本绝对路径。优先复用已安装的
// image-see 技能脚本；不存在时把内置脚本落盘到 ~/.cursor/skills/image-see/scripts/
// （原子写：tmp 文件 + rename），保证「一键启用读图 MCP」不依赖外部技能安装。
func ensureVisionReaderScript() (string, error) {
	if existing := detectVisionReaderScript(); existing != "" {
		return existing, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("无法确定用户主目录")
	}
	scriptDir := filepath.Join(home, ".cursor", "skills", "image-see", "scripts")
	scriptPath := filepath.Join(scriptDir, readerMCPBundledScriptName)
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return "", fmt.Errorf("创建技能目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(scriptDir, ".vision-*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建临时脚本失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bundledVisionMCPScript); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("写入临时脚本失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("关闭临时脚本失败: %w", err)
	}
	if err := os.Rename(tmpName, scriptPath); err != nil {
		return "", fmt.Errorf("写入脚本 %s 失败: %w", scriptPath, err)
	}
	return scriptPath, nil
}

// resolvePythonCommand 探测可用的 python 解释器绝对路径。
// Windows GUI 启动的进程可能缺少用户级 PATH，因此除 LookPath 外再兜底常见安装目录。
func resolvePythonCommand() (string, error) {
	for _, name := range []string{"python", "python3", "py"} {
		if resolved, err := exec.LookPath(name); err == nil && strings.TrimSpace(resolved) != "" {
			return resolved, nil
		}
	}
	home, _ := os.UserHomeDir()
	var fallbackDirs []string
	if programFiles := strings.TrimSpace(os.Getenv("ProgramFiles")); programFiles != "" {
		for _, version := range []string{"Python313", "Python312", "Python311", "Python310", "Python39"} {
			fallbackDirs = append(fallbackDirs, filepath.Join(programFiles, version))
		}
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		for _, version := range []string{"Python313", "Python312", "Python311", "Python310"} {
			fallbackDirs = append(fallbackDirs, filepath.Join(localAppData, "Programs", "Python", version))
		}
	}
	if home != "" {
		fallbackDirs = append(fallbackDirs, filepath.Join(home, "AppData", "Local", "Programs", "Python", "Python313"))
	}
	for _, dir := range fallbackDirs {
		candidate := filepath.Join(dir, "python.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 python 解释器（python/python3/py）。请安装 Python，或在 MCP 配置中使用 python 的绝对路径")
}

// EnableReaderMCP 把读图 MCP（stdio：python + vision_mcp_server.py，env 注入网关三项）
// upsert 进 Cursor 全局 ~/.cursor/mcp.json 并失效扫描缓存，使其出现在
// 「设置 → Skills 与 MCP」列表并自动启用（未写入 disabledMcpServers 即默认启用）。
// 脚本由本程序内置，首次启用时自动落盘，不再要求用户预先安装 image-see 技能。
func (s *ProxyService) EnableReaderMCP(url, apiKey, model string) (ReaderMCPResult, error) {
	baseURL := strings.TrimSpace(url)
	if baseURL == "" {
		return ReaderMCPResult{}, fmt.Errorf("读图网关地址（url）不能为空")
	}
	readerModel := strings.TrimSpace(model)
	if readerModel == "" {
		readerModel = "gpt-5.6-luna"
	}
	mcpPath, err := cursorUserMCPConfigPath()
	if err != nil {
		return ReaderMCPResult{}, err
	}
	doc, err := readCursorMCPDocument(mcpPath)
	if err != nil {
		return ReaderMCPResult{}, err
	}
	_, existed := cursorMCPServersFromDocument(doc)[readerMCPIdentifier]
	// 手动启用入口无协议指定，默认 OpenAI 兼容 chat/completions；
	// 由视觉委派联动写入时会带上与委派一致的 endpoint。
	scriptPath, err := s.upsertReaderMCPServer(baseURL, strings.TrimSpace(apiKey), readerModel, "/v1/chat/completions")
	if err != nil {
		return ReaderMCPResult{}, err
	}
	return ReaderMCPResult{
		Identifier: readerMCPIdentifier,
		ScriptPath: scriptPath,
		WasAdded:   !existed,
	}, nil
}

// upsertReaderMCPServer 把读图 MCP（stdio：python + vision_mcp_server.py，env 注入网关三项
// + 请求端点）upsert 进 Cursor 全局 ~/.cursor/mcp.json 并失效扫描缓存。供 EnableReaderMCP
// 与视觉委派自动联动共用；脚本内置，首次写入时自动落盘。
// endpoint 是与视觉委派所用协议对齐的请求端点（如 /v1/chat/completions、/v1/responses）。
func (s *ProxyService) upsertReaderMCPServer(baseURL, apiKey, model, endpoint string) (string, error) {
	scriptPath, err := ensureVisionReaderScript()
	if err != nil {
		return "", err
	}
	pythonCmd, err := resolvePythonCommand()
	if err != nil {
		return "", err
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("读图网关地址（baseURL）不能为空")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gpt-5.6-luna"
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "/v1/chat/completions"
	}
	mcpPath, err := cursorUserMCPConfigPath()
	if err != nil {
		return "", err
	}
	doc, err := readCursorMCPDocument(mcpPath)
	if err != nil {
		return "", err
	}
	servers := cursorMCPServersFromDocument(doc)
	servers[readerMCPIdentifier] = map[string]any{
		"command": pythonCmd,
		"args":    []any{scriptPath},
		"env": map[string]any{
			"IMAGE_SEE_BASE_URL": baseURL,
			"IMAGE_SEE_ENDPOINT": endpoint,
			"IMAGE_SEE_API_KEY":  apiKey,
			"IMAGE_SEE_MODEL":    model,
		},
	}
	doc["mcpServers"] = servers
	if err := writeCursorMCPDocument(mcpPath, doc); err != nil {
		return "", err
	}
	forwarder.InvalidateMCPScanCache()
	return scriptPath, nil
}

// syncVisionReaderFromDelegation 在视觉委派配置保存后，自动把识图模型对应的
// 网关信息（baseURL / apiKey / 模型名 / 请求端点）同步到读图 MCP（vision-reader），
// 作为委派失败或不可用时的 MCP 兜底通道。端点与视觉委派所用协议保持一致
// （OpenAIEndpoint 优先，其次按 OpenAIRequestGroup 推断），保证 MCP 兜底与
// 委派请求打同一个网关、用同一种协议。同步失败不阻断保存，仅记录日志。
func (s *ProxyService) syncVisionReaderFromDelegation(cfg DelegationConfig) error {
	if !cfg.VisionDelegation.Enabled {
		return nil
	}
	visionID := strings.TrimSpace(cfg.VisionDelegation.VisionModelID)
	if visionID == "" {
		return nil
	}
	userCfg, err := s.core.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("load user config: %w", err)
	}
	var adapter *serverconfig.ModelAdapterConfig
	for i := range userCfg.ModelAdapters {
		if strings.TrimSpace(userCfg.ModelAdapters[i].ID) == visionID {
			adapter = &userCfg.ModelAdapters[i]
			break
		}
	}
	if adapter == nil {
		return fmt.Errorf("vision model %q not found in model adapters", visionID)
	}
	baseURL := strings.TrimSpace(adapter.BaseURL)
	if baseURL == "" {
		return fmt.Errorf("vision model %q has no baseURL", visionID)
	}
	model := strings.TrimSpace(adapter.ModelID)
	if model == "" {
		model = visionID
	}
	endpoint := strings.TrimSpace(adapter.OpenAIEndpoint)
	if endpoint == "" {
		switch strings.ToLower(strings.TrimSpace(adapter.OpenAIRequestGroup)) {
		case "responses":
			endpoint = "/v1/responses"
		default:
			endpoint = "/v1/chat/completions"
		}
	}
	if _, err := s.upsertReaderMCPServer(baseURL, strings.TrimSpace(adapter.APIKey), model, endpoint); err != nil {
		return err
	}
	return nil
}

// cursorUserMCPConfigPath 返回用户级 Cursor MCP 配置文件路径（与 mcp_scanner 一致）。
func cursorUserMCPConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("无法确定用户主目录")
	}
	return filepath.Join(home, ".cursor", "mcp.json"), nil
}

// readCursorMCPServers 读取 Cursor mcp.json 的 mcpServers 映射；文件不存在时返回空映射。
func readCursorMCPServers(path string) (map[string]any, error) {
	doc, err := readCursorMCPDocument(path)
	if err != nil {
		return nil, err
	}
	return cursorMCPServersFromDocument(doc), nil
}

func cursorMCPServersFromDocument(doc map[string]any) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	return servers
}

func readCursorMCPDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// writeCursorMCPServers 原子写回 Cursor mcp.json（tmp 文件 + rename）。
func writeCursorMCPServers(path string, servers map[string]any) error {
	return writeCursorMCPDocument(path, map[string]any{"mcpServers": servers})
}

func writeCursorMCPDocument(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return nil
}

// ConnectMCPServer explicitly trusts and connects one discovered MCP server.
func (s *ProxyService) ConnectMCPServer(workspaceRoot string, identifier string, attemptID string) (forwarder.MCPServerSnapshotItem, error) {
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		return forwarder.MCPServerSnapshotItem{}, err
	}
	settings := skillMCPScanSettings(cfg.SkillMCPScan)
	// 显式连接即用户信任该 server：不受扫描总开关影响（总开关只控制是否注入 agent 会话），
	// 但仍尊重 mcp.json 配置级 enabled 与 DisabledMCPServers 显式禁用。
	connectSettings := settings
	connectSettings.Enabled = true
	configs := forwarder.ScanMCPServerConfigs(workspaceRoot, connectSettings)
	registry := forwarder.SharedMCPRuntimeRegistry()
	forwarder.SyncMCPRuntimeForWorkspace(registry, workspaceRoot, enabledMCPConfigs(configs))
	target, ok := findEnabledMCPServerConfig(configs, forwarder.MCPRuntimeScope(workspaceRoot), identifier)
	if !ok {
		return forwarder.MCPServerSnapshotItem{}, fmt.Errorf("enabled mcp server %q not found in current scan", identifier)
	}
	connectCtx, cancel := context.WithCancel(context.Background())
	key := strings.ToLower(strings.TrimSpace(identifier))
	runtimeScope := target.RuntimeScope
	attemptKey := strings.TrimSpace(attemptID)
	if attemptKey == "" {
		cancel()
		return forwarder.MCPServerSnapshotItem{}, fmt.Errorf("mcp connect attempt id is required")
	}
	s.mcpConnectMu.Lock()
	if _, exists := s.mcpConnects[attemptKey]; exists {
		s.mcpConnectMu.Unlock()
		cancel()
		return forwarder.MCPServerSnapshotItem{}, fmt.Errorf("mcp connect attempt id %q is already active", attemptID)
	}
	for existingAttempt, previous := range s.mcpConnects {
		if previous.identifier == key && previous.runtimeScope == runtimeScope {
			previous.cancel()
			delete(s.mcpConnects, existingAttempt)
		}
	}
	s.mcpConnectSeq++
	generation := s.mcpConnectSeq
	s.mcpConnects[attemptKey] = mcpConnectHandle{identifier: key, runtimeScope: runtimeScope, generation: generation, cancel: cancel}
	s.mcpConnectMu.Unlock()
	defer func() {
		cancel()
		s.mcpConnectMu.Lock()
		if current, ok := s.mcpConnects[attemptKey]; ok && current.generation == generation {
			delete(s.mcpConnects, attemptKey)
		}
		s.mcpConnectMu.Unlock()
	}()
	if err := registry.Connect(connectCtx, runtimeScope, target.Identifier); err != nil {
		return forwarder.MCPServerSnapshotItem{}, err
	}
	return findMCPServerSnapshot(forwarder.SnapshotMCPServersWithSettings(workspaceRoot, settings), identifier)
}

// DisconnectMCPServer closes one active MCP session without changing its persisted scan setting.
func (s *ProxyService) DisconnectMCPServer(workspaceRoot string, identifier string) (forwarder.MCPServerSnapshotItem, error) {
	cfg, err := s.core.LoadUserConfig()
	if err != nil {
		return forwarder.MCPServerSnapshotItem{}, err
	}
	settings := skillMCPScanSettings(cfg.SkillMCPScan)
	configs := forwarder.ScanMCPServerConfigs(workspaceRoot, settings)
	registry := forwarder.SharedMCPRuntimeRegistry()
	forwarder.SyncMCPRuntimeForWorkspace(registry, workspaceRoot, enabledMCPConfigs(configs))
	target, ok := findMCPServerConfig(configs, forwarder.MCPRuntimeScope(workspaceRoot), identifier)
	runtimeScope := forwarder.MCPRuntimeScope(workspaceRoot)
	if ok {
		runtimeScope = target.RuntimeScope
	} else {
		runtimeScope = registry.ResolveScope(runtimeScope, identifier)
	}
	s.cancelMCPServerConnections(runtimeScope, identifier)
	if err := registry.Disconnect(runtimeScope, identifier); err != nil {
		return forwarder.MCPServerSnapshotItem{}, err
	}
	return findMCPServerSnapshot(forwarder.SnapshotMCPServersWithSettings(workspaceRoot, settings), identifier)
}

// CancelMCPServerConnection cancels an in-flight explicit connect attempt.
func (s *ProxyService) CancelMCPServerConnection(identifier string, attemptID string) bool {
	key := strings.ToLower(strings.TrimSpace(identifier))
	attemptKey := strings.TrimSpace(attemptID)
	s.mcpConnectMu.Lock()
	handle, ok := s.mcpConnects[attemptKey]
	if ok && handle.identifier == key {
		delete(s.mcpConnects, attemptKey)
	} else {
		ok = false
	}
	s.mcpConnectMu.Unlock()
	if ok && handle.cancel != nil {
		handle.cancel()
	}
	return ok
}

func (s *ProxyService) cancelMCPServerConnections(runtimeScope string, identifier string) {
	key := strings.ToLower(strings.TrimSpace(identifier))
	runtimeScope = strings.TrimSpace(runtimeScope)
	var cancels []context.CancelFunc
	s.mcpConnectMu.Lock()
	for attemptID, handle := range s.mcpConnects {
		if handle.identifier == key && handle.runtimeScope == runtimeScope {
			cancels = append(cancels, handle.cancel)
			delete(s.mcpConnects, attemptID)
		}
	}
	s.mcpConnectMu.Unlock()
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func skillMCPScanSettings(cfg serverconfig.SkillMCPScanConfig) forwarder.SkillMCPScanSettings {
	return forwarder.SkillMCPScanSettings{
		Enabled:            cfg.Enabled,
		SkillSources:       cfg.SkillSources,
		MCPSources:         cfg.MCPSources,
		DisabledSkills:     cfg.DisabledSkills,
		DisabledMCPServers: cfg.DisabledMCPServers,
	}
}

func enabledMCPConfigs(configs []forwarder.MCPServerConfig) []forwarder.MCPServerConfig {
	result := make([]forwarder.MCPServerConfig, 0, len(configs))
	for _, config := range configs {
		if config.Enabled {
			result = append(result, config)
		}
	}
	return result
}

func findEnabledMCPServerConfig(configs []forwarder.MCPServerConfig, preferredScope string, identifier string) (forwarder.MCPServerConfig, bool) {
	return findMCPServerConfigMatching(configs, preferredScope, identifier, true)
}

func findMCPServerConfig(configs []forwarder.MCPServerConfig, preferredScope string, identifier string) (forwarder.MCPServerConfig, bool) {
	return findMCPServerConfigMatching(configs, preferredScope, identifier, false)
}

func findMCPServerConfigMatching(configs []forwarder.MCPServerConfig, preferredScope string, identifier string, enabledOnly bool) (forwarder.MCPServerConfig, bool) {
	var fallback forwarder.MCPServerConfig
	foundFallback := false
	for _, config := range configs {
		if !strings.EqualFold(strings.TrimSpace(config.Identifier), strings.TrimSpace(identifier)) || (enabledOnly && !config.Enabled) {
			continue
		}
		if strings.TrimSpace(config.RuntimeScope) == strings.TrimSpace(preferredScope) {
			return config, true
		}
		if !foundFallback {
			fallback = config
			foundFallback = true
		}
	}
	return fallback, foundFallback
}

func findMCPServerSnapshot(items []forwarder.MCPServerSnapshotItem, identifier string) (forwarder.MCPServerSnapshotItem, error) {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Identifier), strings.TrimSpace(identifier)) {
			return item, nil
		}
	}
	return forwarder.MCPServerSnapshotItem{}, fmt.Errorf("mcp server %q not found", identifier)
}
