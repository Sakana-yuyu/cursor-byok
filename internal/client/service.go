package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"cursor/internal/appdata"
	backend "cursor/internal/backend"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/certs"
	"cursor/internal/cursoraccount"
	"cursor/internal/historymetrics"
	"cursor/internal/logger"
	"cursor/internal/mitm"
	"cursor/internal/netproxy"
)

const (
	// publicAPITimeout 表示当前模块中的 publicAPITimeout 状态值。
	publicAPITimeout = 15 * time.Second
	// backendReadyTimeout 表示等待嵌入式 backend 就绪的最长时间。
	backendReadyTimeout = 15 * time.Second
	// backendHealthCheckInterval 表示轮询 backend 健康检查的间隔。
	backendHealthCheckInterval = 1 * time.Second
	// backendHealthCheckAttemptTimeout 限制单次健康检查耗时，避免一次阻塞吃掉全部启动预算。
	backendHealthCheckAttemptTimeout = 1 * time.Second
)

// ProxyService 定义了当前模块中的 ProxyService 类型。
type ProxyService struct {
	// proxy 表示当前声明中的 proxy。
	proxy *mitm.ProxyServer
	// certManager 用于在代理监听地址变化时重建 MITM 服务。
	certManager *certs.Manager
	// backendHost 表示当前嵌入式 backend 服务。
	backendHost *backend.Host
	// cursorAccount 持有仅供插件、Skills 和 MCP 控制面使用的真实 Cursor 身份。
	cursorAccount *cursoraccount.Manager

	// lifecycleMu serializes start/stop transitions so a Cursor launch cannot
	// observe a partially started proxy while the automatic startup is running.
	lifecycleMu sync.Mutex

	// mu 表示当前声明中的 mu。
	mu sync.RWMutex
	// lastError 表示当前声明中的 lastError。
	lastError string
	// cursorSettingsApplied 表示当前是否已完成宿主代理设置注入。
	cursorSettingsApplied bool
	// caIncomplete 表示本地 CA 材料不完整（cert/key 仅存其一），本地代理已降级停用。
	caIncomplete bool
	// caError 表示 CA 材料不完整的原始错误信息。
	caError string

	// configMu 表示当前声明中的 configMu。
	configMu sync.Mutex
	// configPath 表示当前声明中的 configPath。
	configPath string
	// store 表示统一的配置存储。
	store *serverconfig.Store
	// caCertPEM 表示当前声明中的 caCertPEM。
	caCertPEM []byte

	// caFileMu 表示当前声明中的 caFileMu。
	caFileMu sync.Mutex
	// caFilePath 表示当前声明中的 caFilePath。
	caFilePath string

	// publicClient 表示当前声明中的 publicClient。
	publicClient *http.Client
	// logsRoot 表示当前声明中的 logsRoot。
	logsRoot string
	// modelTestMu 保护模型测速缓存。
	modelTestMu sync.RWMutex
	// modelTestResults 保存当前进程内的模型测速结果。
	modelTestResults map[string]ModelAdapterTestResult

	// modelCatalogCache 缓存模型列表结果，减少重复网络调用。
	modelCatalogCache *metadataCache[ModelCatalogResult]
	// providerBalanceCache 缓存余额查询结果，减少重复网络调用。
	providerBalanceCache *metadataCache[ProviderBalance]
}

// MarkCAIncomplete 记录 CA 材料不完整状态（应用降级启动时由 runner 调用）。
// 本地代理因此停用，前端据此展示「一键修复」入口。
func (s *ProxyService) MarkCAIncomplete(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.caIncomplete = true
	s.caError = ""
	if err != nil {
		s.caError = err.Error()
	}
	s.mu.Unlock()
}

// ClearCAIncomplete 清除 CA 材料不完整状态（修复成功后调用），使 StartProxy 的
// caIncomplete 硬门放行。调用方应先确保 certManager/caCertPEM 已更新为有效 CA。
func (s *ProxyService) ClearCAIncomplete() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.caIncomplete = false
	s.caError = ""
	s.mu.Unlock()
}

// reloadCAFromDiskLocked 修复并重新加载本地 CA：当 CA 材料不完整（key 缺失等）时，
// 调用 certs.RepairAndReloadManager 备份残留 + 重新生成，并用新 Manager 更新内存状态
// （certManager / caCertPEM / caFilePath），随后清掉 caIncomplete 标志，使代理可立即启动。
// 调用方需持有 s.mu（或在外部串行化，避免与 StartProxy/ApplyCursorSettings 竞争）。
// 返回的 backupPath 为残留文件备份路径（空表示材料原本就完整、未重建）。
func (s *ProxyService) reloadCAFromDiskLocked() (backupPath string, err error) {
	if s == nil {
		return "", errors.New("proxy service is not initialized")
	}
	certPath := appdata.CACertFilePath()
	keyPath := appdata.CAKeyFilePath()
	manager, backup, err := certs.RepairAndReloadManager(certPath, keyPath)
	if err != nil {
		return backup, err
	}
	if manager == nil {
		return backup, errors.New("CA reload returned nil manager")
	}
	s.certManager = manager
	newPEM := manager.CACertPEM()
	copiedCert := make([]byte, len(newPEM))
	copy(copiedCert, newPEM)
	s.caCertPEM = copiedCert
	s.caFilePath = certPath
	s.caIncomplete = false
	s.caError = ""
	return backup, nil
}

// ReloadCAFromDisk 是 reloadCAFromDiskLocked 的公开入口（供 bridge 调用）。
// 在 s.mu 保护下执行修复+重载，并 emitState 让前端同步。
// 返回 backupPath（空表示材料已完整、未重建）。
func (s *ProxyService) ReloadCAFromDisk() (backupPath string, err error) {
	if s == nil {
		return "", errors.New("proxy service is not initialized")
	}
	s.mu.Lock()
	backup, err := s.reloadCAFromDiskLocked()
	s.mu.Unlock()
	if err != nil {
		s.setLastError(err)
		s.emitState()
		return backup, err
	}
	s.setLastError(nil)
	s.emitState()
	return backup, nil
}

// CertManagerRepairedAt 暴露当前 certManager 的 RepairedAt（供 bridge 在热重载后刷新）。
func (s *ProxyService) CertManagerRepairedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.certManager == nil {
		return time.Time{}
	}
	return s.certManager.RepairedAt
}

// NewProxyService 用于处理与 NewProxyService 相关的逻辑。
func NewProxyService(proxy *mitm.ProxyServer, certManager *certs.Manager, caCertPEM []byte) *ProxyService {
	if err := appdata.EnsureAssistantHome(); err != nil {
		logger.Errorf("ensure assistant home failed: %v", err)
	}
	copiedCert := make([]byte, len(caCertPEM))
	copy(copiedCert, caCertPEM)

	service := &ProxyService{
		proxy:            proxy,
		certManager:      certManager,
		configPath:       resolveUserConfigPath(),
		logsRoot:         resolveLogsRootPath(),
		caCertPEM:        copiedCert,
		publicClient:     netproxy.NewHTTPClient(publicAPITimeout),
		modelTestResults: make(map[string]ModelAdapterTestResult),

		modelCatalogCache:    newMetadataCache[ModelCatalogResult](modelCatalogCacheTTL),
		providerBalanceCache: newMetadataCache[ProviderBalance](providerBalanceCacheTTL),
	}
	service.cursorAccount = cursoraccount.NewManager(
		filepath.Join(appdata.DataRootPath(), "cursor-account.json"),
		netproxy.NewHTTPClient(publicAPITimeout),
	)
	service.loadPersistedModelAdapterTestResults()
	service.store = serverconfig.NewStore(service.configPath, service.logsRoot)
	host, err := backend.NewHost(service.store, service.cursorAccount)
	if err != nil {
		logger.Errorf("init backend host failed: %v", err)
	} else {
		service.backendHost = host
	}
	return service
}

func (s *ProxyService) ensureBackendHost() error {
	if s == nil {
		return nil
	}
	if s.backendHost != nil {
		return nil
	}
	host, err := backend.NewHost(s.store, s.cursorAccount)
	if err != nil {
		return err
	}
	s.backendHost = host
	return nil
}

// HasActiveConversation reports whether an embedded backend stream is still processing a conversation.
func (s *ProxyService) HasActiveConversation(conversationID, requestID string) bool {
	if s == nil || s.backendHost == nil {
		return false
	}
	return s.backendHost.HasActiveConversation(conversationID, requestID)
}

// GetRecentWorkspaceRoot returns the latest workspace seen by the embedded backend.
func (s *ProxyService) GetRecentWorkspaceRoot() string {
	if s == nil || s.backendHost == nil {
		return ""
	}
	return s.backendHost.GetRecentWorkspaceRoot()
}

// ResetUsageMetrics 清空当前 backend writer 管理的用量统计。
func (s *ProxyService) ResetUsageMetrics() error {
	if s == nil || s.backendHost == nil {
		return historymetrics.ResetUsageFile(appdata.UsageFilePath())
	}
	return s.backendHost.ResetUsageMetrics()
}

func (s *ProxyService) ensureProxy(cfg serverconfig.Config) error {
	if s == nil {
		return nil
	}
	baseURL := ""
	if s.backendHost != nil {
		baseURL = s.backendHost.BaseURL()
	}
	if baseURL == "" {
		baseURL = "http://" + cfg.BackendListenAddr
	}
	listenAddr := cfg.ProxyListenAddr

	if s.proxy != nil {
		snapshot := s.proxy.Snapshot()
		if snapshot.ListenAddr == listenAddr {
			return s.proxy.UpdateBaseURL(baseURL)
		}
		if snapshot.Running {
			return fmt.Errorf("代理正在运行，不能从 %s 切换到 %s，请先停止服务", snapshot.ListenAddr, listenAddr)
		}
	}

	proxyServer, err := mitm.NewProxyServer(listenAddr, baseURL, "", "", s.certManager)
	if err != nil {
		return err
	}
	s.proxy = proxyServer
	return nil
}

func (s *ProxyService) waitForBackend(ctx context.Context) error {
	if s == nil || s.backendHost == nil {
		return nil
	}
	ticker := time.NewTicker(backendHealthCheckInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		healthCtx, healthCancel := context.WithTimeout(ctx, backendHealthCheckAttemptTimeout)
		err := s.backendHost.HealthCheck(healthCtx)
		healthCancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("等待内置后端就绪失败: %w", lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
