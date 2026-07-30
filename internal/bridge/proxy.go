package bridge

import (
	"context"
	"cursor/internal/backend/forwarder"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/certs"
	"cursor/internal/client"
	"cursor/internal/mitm"
	"cursor/internal/promptinject"
	"fmt"
	"runtime"
)

// Public DTOs remain in package main for Wails service compatibility.
// ProxyState 定义了当前模块中的 ProxyState 类型。
type ProxyState = client.ProxyState

// UserConfig 定义了当前模块中的 UserConfig 类型。
type UserConfig = client.UserConfig

// ModelAdapterConfig 定义模型测速使用的模型配置结构。
type ModelAdapterConfig = serverconfig.ModelAdapterConfig

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

// PromptInjectionTemplate 定义单个可独立开关的提示词模板。
type PromptInjectionTemplate = promptinject.PromptTemplate

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
}

// NewProxyService 用于处理与 NewProxyService 相关的逻辑。
func NewProxyService(proxy *mitm.ProxyServer, certManager *certs.Manager, caCertPEM []byte) *ProxyService {
	manager := promptinject.New()
	if _, err := manager.Load(); err != nil {
		manager = promptinject.New()
	}
	return &ProxyService{core: client.NewProxyService(proxy, certManager, caCertPEM), promptInjection: manager}
}

// StartProxy 用于处理与 StartProxy 相关的逻辑。
func (s *ProxyService) StartProxy() (ProxyState, error) {
	return s.core.StartProxy()
}

// StopProxy 用于处理与 StopProxy 相关的逻辑。
func (s *ProxyService) StopProxy() (ProxyState, error) {
	return s.core.StopProxy()
}

// GetState 用于处理与 GetState 相关的逻辑。
func (s *ProxyService) GetState() ProxyState {
	return s.core.GetState()
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

// TestModelAdapter 用于处理与 TestModelAdapter 相关的逻辑。
func (s *ProxyService) TestModelAdapter(adapter ModelAdapterConfig) (ModelAdapterTestResult, error) {
	return s.core.TestModelAdapter(adapter)
}

// FetchModelCatalog 用于处理与 FetchModelCatalog 相关的逻辑。
func (s *ProxyService) FetchModelCatalog(request ModelCatalogRequest) (ModelCatalogResult, error) {
	return s.core.FetchModelCatalog(request)
}

// AutoMatchContextWindows 自动为所有已存储模型适配器配对正确的上下文窗口：
// 目录命中则覆盖，目录未命中则探测 provider /models 回填。供前端「一键自动配对」按钮调用。
func (s *ProxyService) AutoMatchContextWindows(ctx context.Context) (AutoMatchResult, error) {
	return s.core.AutoMatchContextWindows(ctx)
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
		MCPServers: forwarder.SnapshotMCPServers(workspaceRoot),
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
