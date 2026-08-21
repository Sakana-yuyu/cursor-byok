package backend

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"cursor/internal/ads"
	"cursor/internal/appdata"
	"cursor/internal/backend/delegation"
	delegationexecutors "cursor/internal/backend/delegation/executors"
	"cursor/internal/backend/forwarder"
	"cursor/internal/backend/server"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/backend/server/upstream"
	"cursor/internal/historymetrics"
	"cursor/internal/logger"
	"cursor/internal/netproxy"
	"cursor/internal/promptinject"
	"cursor/internal/routing"
	legacyruntime "cursor/internal/runtime"
	"cursor/internal/safego"
)

const healthPath = "/healthz"

const tabServerBaseURL = "https://tab.leokun.cn"

// delegationExecutorProbeConcurrency 限制手工刷新时同时启动的外部 CLI，
// 避免多个执行器的超时按注册顺序线性叠加。
const delegationExecutorProbeConcurrency = 3

type Host struct {
	store             *serverconfig.Store
	listenAddr        string
	configs           *serverconfig.Manager
	executorRegistry  *delegation.ExecutorRegistry
	executorMu        sync.Mutex
	customExecutorIDs map[delegation.ExecutorID]struct{}
	executorInstaller delegationExecutorInstaller
	executorInstalls  map[delegation.ExecutorID]struct{}
	healthHTTP        *http.Client
	controlPlaneAuth  upstream.AuthorizationProvider

	runMu      sync.RWMutex
	httpServer *http.Server
	// agentModule 持有当前已挂载的 forwarder 服务，关闭时需要先主动收口活动流。
	agentModule *forwarder.Module

	lastRunErr error

	mux http.Handler
}

func NewHost(store *serverconfig.Store, controlPlaneAuth upstream.AuthorizationProvider) (*Host, error) {
	if store == nil {
		return nil, fmt.Errorf("backend config store is required")
	}
	configs, err := serverconfig.NewManager(context.Background(), store)
	if err != nil {
		return nil, err
	}
	forwarder.SharedMCPRuntimeRegistry().SetTrustResolver(func() []forwarder.MCPTrustRecord {
		return configs.SkillMCPScanTrustRecords()
	})
	cfg := configs.Current()
	host := &Host{
		store:             store,
		listenAddr:        cfg.BackendListenAddr,
		configs:           configs,
		executorRegistry:  delegation.NewExecutorRegistry(delegation.ExecutorRegistryConfig{}),
		customExecutorIDs: make(map[delegation.ExecutorID]struct{}),
		executorInstaller: newBuiltInExecutorInstaller(),
		executorInstalls:  make(map[delegation.ExecutorID]struct{}),
		healthHTTP:        newLoopbackHTTPClient(),
		controlPlaneAuth:  controlPlaneAuth,
	}
	if err := host.syncDelegationExecutors(cfg); err != nil {
		return nil, err
	}
	configs.Subscribe(func(next serverconfig.Config) {
		if err := host.syncDelegationExecutors(next); err != nil {
			logger.Errorf("同步 delegation executor 配置失败 err=%v", err)
		}
	})
	if err := host.rebuild(cfg); err != nil {
		return nil, err
	}
	return host, nil
}

func (host *Host) syncDelegationExecutors(cfg serverconfig.Config) error {
	if host == nil || host.executorRegistry == nil {
		return nil
	}
	runner := delegation.NewProcessRunner(delegation.ProcessRunnerConfig{})
	registrations := make([]delegation.ExecutorRegistration, 0, 5+len(cfg.Delegation.Executors))
	factories := []struct {
		id     delegation.ExecutorID
		create func(delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error)
	}{
		{id: delegationexecutors.ClaudeCodeExecutorID, create: func(runtimeConfig delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
			return delegationexecutors.NewClaudeCodeRegistration(runner, runtimeConfig)
		}},
		{id: delegationexecutors.CodexCLIExecutorID, create: func(runtimeConfig delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
			return delegationexecutors.NewCodexCLIRegistration(runner, runtimeConfig)
		}},
		{id: delegationexecutors.GeminiCLIExecutorID, create: func(runtimeConfig delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
			return delegationexecutors.NewGeminiCLIRegistration(runner, runtimeConfig)
		}},
		{id: delegationexecutors.KiroCLIExecutorID, create: func(runtimeConfig delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
			return delegationexecutors.NewKiroCLIRegistration(runner, runtimeConfig)
		}},
		{id: delegationexecutors.CursorExecutorID, create: func(runtimeConfig delegation.RuntimeExecutorConfig) (delegation.ExecutorRegistration, error) {
			return delegationexecutors.NewCursorRegistration(
				runtimeConfig,
				delegationexecutors.NewCursorEditorDetector(runtimeConfig.Executable),
				host.cursorAgentExecutionAvailable,
				host.executeCursorAgent,
			)
		}},
	}
	for _, factory := range factories {
		registration, err := factory.create(hostRuntimeExecutorConfig(cfg, factory.id))
		if err != nil {
			return err
		}
		registrations = append(registrations, registration)
	}
	nextCustomIDs := make(map[delegation.ExecutorID]struct{})
	for _, item := range cfg.Delegation.Executors {
		if item.Kind != serverconfig.DelegationExecutorKindCustom {
			continue
		}
		runtimeConfig := hostRuntimeExecutorConfig(cfg, delegation.ExecutorID(item.ID))
		registration, err := delegationexecutors.NewCustomCLIRegistration(runner, runtimeConfig)
		if err != nil {
			return err
		}
		registrations = append(registrations, registration)
		nextCustomIDs[registration.ID] = struct{}{}
	}

	host.executorMu.Lock()
	defer host.executorMu.Unlock()
	for _, registration := range registrations {
		var err error
		if _, exists := host.executorRegistry.Snapshot(registration.ID); exists {
			err = host.executorRegistry.Replace(registration)
		} else {
			err = host.executorRegistry.Register(registration)
		}
		if err != nil {
			return err
		}
	}
	for id := range host.customExecutorIDs {
		if _, keep := nextCustomIDs[id]; !keep {
			host.executorRegistry.Unregister(id)
		}
	}
	host.customExecutorIDs = nextCustomIDs
	return nil
}

func (host *Host) cursorAgentExecutionAvailable(parentRequestID string) bool {
	if host == nil {
		return false
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	return module != nil && module.Service != nil && module.Service.CursorAgentExecutionAvailable(parentRequestID)
}

func (host *Host) executeCursorAgent(ctx context.Context, request delegation.TaskRequest) delegation.TaskResult {
	if host == nil {
		return delegation.TaskResult{Error: fmt.Errorf("backend host is nil")}
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	if module == nil || module.Service == nil {
		return delegation.TaskResult{Error: fmt.Errorf("Cursor agent service is unavailable")}
	}
	return module.Service.ExecuteCursorAgent(ctx, request)
}

func hostRuntimeExecutorConfig(cfg serverconfig.Config, id delegation.ExecutorID) delegation.RuntimeExecutorConfig {
	for _, item := range cfg.Delegation.Executors {
		if delegation.ExecutorID(item.ID) != id {
			continue
		}
		return delegation.RuntimeExecutorConfig{
			ID: id, Kind: item.Kind, DisplayName: item.DisplayName, Enabled: item.Enabled, Priority: item.Priority,
			Executable: item.Executable, ProbeTimeoutSeconds: item.ProbeTimeoutSeconds, ExecutionTimeoutSeconds: item.ExecutionTimeoutSeconds,
			EnvironmentVariables: append([]string{}, item.EnvironmentVariables...), Options: cloneHostStringMap(item.Options),
		}
	}
	return delegation.RuntimeExecutorConfig{ID: id}
}

func cloneHostStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func (host *Host) ExecutorRegistry() *delegation.ExecutorRegistry {
	if host == nil {
		return nil
	}
	return host.executorRegistry
}

func (host *Host) DelegationExecutorSnapshots() []delegation.ExecutorSnapshot {
	if host == nil || host.executorRegistry == nil {
		return nil
	}
	return host.executorRegistry.Snapshots()
}

func (host *Host) RefreshDelegationExecutorProbes(ctx context.Context) ([]delegation.ExecutorSnapshot, error) {
	if host == nil || host.executorRegistry == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	items := host.executorRegistry.Snapshots()
	workers := delegationExecutorProbeConcurrency
	if workers > len(items) {
		workers = len(items)
	}
	if workers == 0 {
		return items, ctx.Err()
	}

	jobs := make(chan delegation.ExecutorID)
	var wg sync.WaitGroup
	// safego 兜底：探测实现可能来自外部执行器适配层，panic 不应拖垮进程；
	// wg.Done 在 fn 内 defer，恢复后依然会释放，wg.Wait 不会死锁。
	for range workers {
		wg.Add(1)
		safego.Go("backend:delegation-executor-probe", func() {
			defer wg.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				_, _ = host.executorRegistry.Probe(ctx, id, true)
			}
		})
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return host.executorRegistry.Snapshots(), ctx.Err()
		case jobs <- item.ID:
		}
	}
	close(jobs)
	wg.Wait()
	return host.executorRegistry.Snapshots(), ctx.Err()
}

func (host *Host) ConfigManager() *serverconfig.Manager {
	if host == nil {
		return nil
	}
	return host.configs
}

// HasActiveConversation reports whether a conversation still has a live forwarder stream.
func (host *Host) HasActiveConversation(conversationID, requestID string) bool {
	if host == nil {
		return false
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	if module == nil || module.Service == nil {
		return false
	}
	return module.Service.HasActiveConversation(conversationID, requestID)
}

// GetRecentWorkspaceRoot returns the active forwarder's latest request workspace.
func (host *Host) GetRecentWorkspaceRoot() string {
	if host == nil {
		return ""
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	if module == nil || module.Service == nil {
		return ""
	}
	return module.Service.RecentWorkspaceRoot()
}

// DelegationTaskSnapshots returns the active forwarder's retained worker state.
func (host *Host) DelegationTaskSnapshots() []forwarder.DelegationTaskSnapshot {
	if host == nil {
		return nil
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	if module == nil || module.Service == nil {
		return nil
	}
	return module.Service.DelegationTaskSnapshots()
}

// ResetUsageMetrics 清空活动 forwarder 持有的用量统计；没有活动 writer 时直接重置文件。
func (host *Host) ResetUsageMetrics() error {
	if host == nil {
		return historymetrics.ResetUsageFile(appdata.UsageFilePath())
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	if module == nil || module.Service == nil {
		return historymetrics.ResetUsageFile(appdata.UsageFilePath())
	}
	return module.Service.ResetUsageMetrics()
}

// CancelDelegationTask cancels one delegated worker by its stable task ID.
func (host *Host) CancelDelegationTask(taskID string) bool {
	if host == nil {
		return false
	}
	host.runMu.RLock()
	module := host.agentModule
	host.runMu.RUnlock()
	if module == nil || module.Service == nil {
		return false
	}
	return module.Service.CancelDelegationTask(taskID)
}

func (host *Host) GetDelegationConfig(ctx context.Context) (serverconfig.DelegationConfig, error) {
	if host == nil || host.configs == nil {
		return serverconfig.DefaultConfig().Delegation, nil
	}
	return host.configs.GetDelegationConfig(ctx)
}

func (host *Host) SaveDelegationConfig(ctx context.Context, cfg serverconfig.DelegationConfig) (serverconfig.DelegationConfig, error) {
	if host == nil || host.configs == nil {
		return serverconfig.DelegationConfig{}, fmt.Errorf("backend config manager is not initialized")
	}
	return host.configs.SaveDelegationConfig(ctx, cfg)
}

func (host *Host) LoadConfig(ctx context.Context) (serverconfig.Config, error) {
	if host == nil || host.configs == nil {
		return serverconfig.DefaultConfig(), nil
	}
	return host.configs.Load(ctx)
}

func (host *Host) RoutingDecisionHistory(query routing.DecisionQuery) (routing.DecisionPage, error) {
	if host == nil || host.configs == nil {
		return routing.DecisionPage{Items: []routing.DecisionRecord{}}, nil
	}
	history := host.configs.RoutingHistory()
	if history == nil {
		return routing.DecisionPage{Items: []routing.DecisionRecord{}}, nil
	}
	return history.List(query)
}

func (host *Host) SetRoutingMetricsSnapshot(snapshot *routing.MetricsSnapshot) {
	if host == nil || host.configs == nil {
		return
	}
	host.configs.SetRoutingMetricsSnapshot(snapshot)
}

func (host *Host) BuildRoutingCandidates(modelID string) []routing.CandidateInput {
	if host == nil || host.configs == nil {
		return nil
	}
	return host.configs.BuildRoutingCandidates(modelID)
}

func (host *Host) SaveConfig(ctx context.Context, cfg serverconfig.Config) (serverconfig.Config, error) {
	if host == nil || host.configs == nil {
		return serverconfig.Config{}, fmt.Errorf("backend config manager is not initialized")
	}
	normalized, err := host.configs.Save(ctx, cfg)
	if err != nil {
		return serverconfig.Config{}, err
	}
	if host.httpServer == nil {
		if rebuildErr := host.rebuild(normalized); rebuildErr != nil {
			return serverconfig.Config{}, rebuildErr
		}
	}
	return normalized, nil
}

func (host *Host) ListenAddr() string {
	if host == nil {
		return ""
	}
	host.runMu.RLock()
	defer host.runMu.RUnlock()
	return host.listenAddr
}

func (host *Host) BaseURL() string {
	listenAddr := strings.TrimSpace(host.ListenAddr())
	if listenAddr == "" {
		return ""
	}
	return "http://" + listenAddr
}

func (host *Host) IsRunning() bool {
	if host == nil {
		return false
	}
	host.runMu.RLock()
	defer host.runMu.RUnlock()
	return host.httpServer != nil
}

func (host *Host) LastRunError() error {
	if host == nil {
		return nil
	}
	host.runMu.RLock()
	defer host.runMu.RUnlock()
	return host.lastRunErr
}

func (host *Host) Start() error {
	if host == nil {
		return fmt.Errorf("backend host is nil")
	}
	cfg := host.configs.Current()

	host.runMu.Lock()
	defer host.runMu.Unlock()
	if host.httpServer != nil {
		return fmt.Errorf("backend is already running")
	}
	if err := host.rebuildLocked(cfg); err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              host.listenAddr,
		Handler:           host.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", host.listenAddr)
	if err != nil {
		host.lastRunErr = fmt.Errorf("监听内置后端 %s 失败: %w", host.listenAddr, err)
		return host.lastRunErr
	}
	host.listenAddr = listener.Addr().String()
	host.httpServer = httpServer
	host.lastRunErr = nil
	logger.Infof("内置后端监听成功 listen_addr=%s", host.listenAddr)

	servingServer, servingListener := httpServer, listener
	safego.Go("backend:http-serve", func() {
		logger.Infof("内置后端开始提供服务 listen_addr=%s", servingListener.Addr().String())
		if err := servingServer.Serve(servingListener); err != nil && err != http.ErrServerClosed {
			runErr := fmt.Errorf("内置后端在 %s 上异常退出: %w", servingListener.Addr().String(), err)
			host.runMu.Lock()
			if host.httpServer == servingServer {
				host.httpServer = nil
			}
			host.lastRunErr = runErr
			host.runMu.Unlock()
			logger.Errorf("%v", runErr)
		}
	})
	return nil
}

func (host *Host) Stop(ctx context.Context) error {
	if host == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 先取消活动 agent 流，让 RunSSE 有机会发出 TurnEnded/canceled，
	// 再进入 HTTP Shutdown；否则长连接会一直拖到 deadline 并让 Cursor 看到 Canceled。
	host.runMu.RLock()
	agentModule := host.agentModule
	host.runMu.RUnlock()
	if agentModule != nil && agentModule.Service != nil {
		if err := agentModule.Service.Shutdown(ctx); err != nil {
			logger.Errorf("forwarder shutdown before backend stop failed err=%v", err)
		}
	}

	host.runMu.Lock()
	serverInstance := host.httpServer
	host.httpServer = nil
	host.runMu.Unlock()
	if serverInstance == nil {
		return nil
	}
	return serverInstance.Shutdown(ctx)
}

func (host *Host) HealthCheck(ctx context.Context) error {
	if host == nil {
		return fmt.Errorf("backend host is nil")
	}
	if runErr := host.LastRunError(); runErr != nil {
		return runErr
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, host.BaseURL()+healthPath, nil)
	if err != nil {
		return err
	}
	client := host.healthHTTP
	if client == nil {
		client = newLoopbackHTTPClient()
	}
	response, err := client.Do(request)
	if err != nil {
		inProcessErr := host.InProcessHealthCheck()
		if inProcessErr == nil {
			return fmt.Errorf("内置后端进程内健康检查成功，但本机 loopback 访问失败: %w", err)
		}
		if runErr := host.LastRunError(); runErr != nil {
			return runErr
		}
		return fmt.Errorf("内置后端 loopback 与进程内健康检查均失败: %w (in-process: %v)", err, inProcessErr)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("内置后端健康检查返回状态码 %d", response.StatusCode)
	}
	return nil
}

func (host *Host) InProcessHealthCheck() error {
	if host == nil {
		return fmt.Errorf("backend host is nil")
	}
	if host.mux == nil {
		return fmt.Errorf("backend handler is nil")
	}
	request := httptest.NewRequest(http.MethodGet, "http://inprocess"+healthPath, nil)
	recorder := httptest.NewRecorder()
	host.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		return fmt.Errorf("in-process health status %d", recorder.Code)
	}
	body := strings.TrimSpace(recorder.Body.String())
	if body != "ok" {
		return fmt.Errorf("in-process health body %q", body)
	}
	logger.Infof("内置后端进程内健康检查成功")
	return nil
}

// loopbackHealthTimeout 是本机健康检查的端到端上限。只打本进程的 /health，
// 正常在毫秒级返回；给出总超时避免后端半死时把检查调用永久挂住。
const loopbackHealthTimeout = 5 * time.Second

func newLoopbackHTTPClient() *http.Client {
	return &http.Client{
		Timeout: loopbackHealthTimeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:   false,
			MaxIdleConns:        1,
			MaxIdleConnsPerHost: 1,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

func (host *Host) rebuild(cfg serverconfig.Config) error {
	host.runMu.Lock()
	defer host.runMu.Unlock()
	return host.rebuildLocked(cfg)
}

func (host *Host) rebuildLocked(cfg serverconfig.Config) error {
	host.listenAddr = cfg.BackendListenAddr
	agentModule := forwarder.NewModuleWithExecutorRegistry(appdata.HistoryRootPath(), host.configs, host.executorRegistry)
	host.agentModule = agentModule
	legacyBidiAppendProcedure := "/aiserver.v1.BidiService/BidiAppend"
	legacyRunSSEProcedure := "/agent.v1.AgentService/RunSSE"
	routeDeps := upstream.Dependencies{
		SystemSettingService: &serverSystemSettings{configs: host.configs},
		HTTPClient:           netproxy.NewHTTPClient(30000 * time.Second),
	}

	host.mux = server.New(
		server.Use(
			server.Recover(),
			server.ServerContext(),
			server.ErrorEncoder(),
		),
		server.Mount(ads.RoutePrefix, ads.NewHTTPHandler(appdata.AdsRootPath())),
		server.GET(healthPath,
			server.Name("healthz"),
			server.HTTP(),
			server.Local(server.Health()),
		),
		server.POST(legacyBidiAppendProcedure,
			server.Name("bidi_append"),
			server.ConnectUnary(),
			server.Local(server.HTTPHandlerAction(agentModule.LocalBidiHandler)),
		),
		server.POST(legacyRunSSEProcedure,
			server.Name("run_sse"),
			server.ConnectStream(),
			server.Local(server.HTTPHandlerAction(agentModule.LocalRunSSE)),
		),
		server.POST("/aiserver.v1.AiService/ServerTime",
			server.Name("server_time"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "server_time",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.ServerTimeResponse",
				MockBuilder:   upstream.ServerTimeMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AiService/GetServerConfig",
			server.Name("server_config"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "server_config",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetServerConfigResponse",
				MockBuilder:   upstream.ServerConfigMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.ServerConfigService/GetServerConfig",
			server.Name("server_config_service_get_server_config"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "server_config_service_get_server_config",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetServerConfigResponse",
				MockBuilder:   upstream.ServerConfigMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AiService/AvailableModels",
			server.Name("available_models"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "available_models",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.AvailableModelsResponse",
				MockBuilder:   upstream.AvailableModelsMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AiService/GetUsableModels",
			server.Name("usable_models"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "usable_models",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetUsableModelsResponse",
				MockBuilder:   upstream.UsableModelsMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AiService/GetDefaultModelForCli",
			server.Name("default_model_for_cli"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "default_model_for_cli",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetDefaultModelForCliResponse",
				MockBuilder:   upstream.DefaultModelForCliMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AiService/GetDefaultModel",
			server.Name("default_model"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "default_model",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetDefaultModelResponse",
				MockBuilder:   upstream.DefaultModelMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AiService/GetDefaultModelNudgeData",
			server.Name("default_model_nudge"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "default_model_nudge",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetDefaultModelNudgeDataResponse",
				MockBuilder:   upstream.DefaultModelNudgeMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AnalyticsService/BootstrapStatsig",
			server.Name("bootstrap_statsig"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "bootstrap_statsig",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.BootstrapStatsigResponse",
				MockBuilder:   upstream.BootstrapStatsigMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AnalyticsService/GetFirstWindowStatsigDecision",
			server.Name("first_window_statsig_decision"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "first_window_statsig_decision",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetFirstWindowStatsigDecisionResponse",
				MockBuilder:   upstream.FirstWindowStatsigDecisionMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AnalyticsService/SubmitLogs",
			server.Name("analytics_submit_logs"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "analytics_submit_logs",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.SubmitLogsResponse",
				MockBuilder:   upstream.SubmitLogsMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.AnalyticsService/TrackEvents",
			server.Name("analytics_track_events"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "analytics_track_events",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.TrackEventsResponse",
				MockBuilder:   upstream.EmptyMockBuilder,
			})),
		),
		server.POST("/v1/traces",
			server.Name("otlp_traces"),
			server.HTTP(),
			server.Local(upstream.FixedStatusAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "otlp_traces",
				StatusCode: http.StatusOK,
			})),
		),
		server.POST("/oauth/token",
			server.Name("oauth_token"),
			server.HTTP(),
			server.Local(upstream.MockOAuthAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "oauth_token",
				StatusCode: http.StatusOK,
			})),
		),
		server.POST("/aiserver.v1.AuthService/GetEmail",
			server.Name("auth_service_get_email"),
			server.ConnectUnary(),
			server.Local(upstream.MockAuthEmailAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "auth_service_get_email",
				StatusCode: http.StatusOK,
			})),
		),
		tabServerProcedure("/aiserver.v1.AiService/StreamCpp", "ai_stream_cpp", server.ConnectStream(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/StreamNextCursorPrediction", "ai_stream_next_cursor_prediction", server.ConnectStream(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/GetCppEditClassification", "ai_get_cpp_edit_classification", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/RefreshTabContext", "ai_refresh_tab_context", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/CppConfig", "ai_cpp_config", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/CppEditHistoryStatus", "ai_cpp_edit_history_status", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/CppAppend", "ai_cpp_append", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/CppEditHistoryAppend", "ai_cpp_edit_history_append", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.AiService/ReportAiCodeChangeMetrics", "ai_report_ai_code_change_metrics", server.ConnectUnary(), routeDeps),
		writeGitCommitMessageDispatchProcedure(
			"/aiserver.v1.AiService/WriteGitCommitMessage",
			"ai_write_git_commit_message",
			server.ConnectUnary(),
			agentModule.AiHandler,
			func() string {
				if agentModule == nil || agentModule.Service == nil {
					return ""
				}
				if injection := agentModule.Service.PromptInjection(); injection != nil {
					return injection.CommitMessageSource()
				}
				return ""
			},
			host.controlPlaneAuth,
			routeDeps,
		),
		tabServerProcedure("/aiserver.v1.AiService/WriteGitBranchName", "ai_write_git_branch_name", server.ConnectUnary(), routeDeps),
		repositoryServiceProcedure(forwarder.RepositoryServiceFastRepoInitHandshakeV2Procedure, "repository_fast_repo_init_handshake_v2", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceFastRepoInitHandshakeProcedure, "repository_fast_repo_init_handshake", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceFastRepoSyncCompleteProcedure, "repository_fast_repo_sync_complete", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceSyncMerkleSubtreeV2Procedure, "repository_sync_merkle_subtree_v2", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceSyncMerkleSubtreeProcedure, "repository_sync_merkle_subtree", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceFastUpdateFileV2Procedure, "repository_fast_update_file_v2", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceFastUpdateFileProcedure, "repository_fast_update_file", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceEnsureIndexCreatedProcedure, "repository_ensure_index_created", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceGetCopyStatusProcedure, "repository_get_copy_status", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceGetUploadLimitsProcedure, "repository_get_upload_limits", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceGetNumFilesToSendProcedure, "repository_get_num_files_to_send", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceGetAvailableChunkingStrategiesProcedure, "repository_get_available_chunking_strategies", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceGetHighLevelFolderDescriptionProcedure, "repository_get_high_level_folder_description", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceRepositoryStatusProcedure, "repository_status", server.ConnectUnary(), agentModule),
		repositoryServiceProcedure(forwarder.RepositoryServiceBatchRepositoryStatusProcedure, "repository_batch_status", server.ConnectUnary(), agentModule),
		uploadServiceProcedure(forwarder.UploadServiceUploadDocumentationProcedure, "upload_documentation", server.ConnectUnary(), agentModule),
		uploadServiceProcedure(forwarder.UploadServiceGetDocProcedure, "upload_get_doc", server.ConnectUnary(), agentModule),
		uploadServiceProcedure(forwarder.UploadServiceGetPagesProcedure, "upload_get_pages", server.ConnectUnary(), agentModule),
		uploadServiceProcedure(forwarder.UploadServiceUploadedStatusProcedure, "upload_uploaded_status", server.ConnectUnary(), agentModule),
		server.Any("/aiserver.v1.AiService/*",
			server.Name("ai_service"),
			server.HTTP(),
			server.Local(server.HTTPHandlerAction(agentModule.AiHandler)),
		),
		tabServerProcedure("/aiserver.v1.CppService/AvailableModels", "cpp_available_models", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.CppService/RecordCppFate", "cpp_record_cpp_fate", server.ConnectUnary(), routeDeps),
		server.Any("/aiserver.v1.CppService/*",
			server.Name("cpp_service"),
			server.HTTP(),
			server.Local(func(ctx *server.Context) error {
				http.NotFound(ctx.Writer, ctx.Request)
				return nil
			}),
		),
		tabServerProcedure("/aiserver.v1.FileSyncService/FSSyncFile", "file_sync_sync_file", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.FileSyncService/FSIsEnabledForUser", "file_sync_is_enabled_for_user", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.FileSyncService/FSConfig", "file_sync_config", server.ConnectUnary(), routeDeps),
		tabServerProcedure("/aiserver.v1.FileSyncService/FSUploadFile", "file_sync_upload_file", server.ConnectUnary(), routeDeps),
		server.Any("/aiserver.v1.FileSyncService/*",
			server.Name("file_sync"),
			server.HTTP(),
			server.Local(func(ctx *server.Context) error {
				http.NotFound(ctx.Writer, ctx.Request)
				return nil
			}),
		),
		server.POST("/aiserver.v1.DashboardService/GetTokenUsage",
			server.Name("dashboard_token_usage"),
			server.HTTP(),
			server.Local(server.HTTPHandlerAction(agentModule.AiHandler)),
		),
		server.POST("/aiserver.v1.DashboardService/GetGlassEarlyPreviewEnrollment",
			server.Name("dashboard_glass_early_preview_enrollment"),
			server.ConnectUnary(),
			server.Local(server.HTTPHandlerAction(agentModule.AiHandler)),
		),
		server.POST("/aiserver.v1.DashboardService/GetCurrentPeriodUsage",
			server.Name("dashboard_current_period_usage"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_current_period_usage",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetCurrentPeriodUsageResponse",
				MockBuilder:   upstream.DashboardCurrentPeriodUsageMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetTeams",
			server.Name("dashboard_get_teams"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_get_teams",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetTeamsResponse",
				MockBuilder:   upstream.DashboardTeamsMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetManagedSkills",
			server.Name("dashboard_get_managed_skills"),
			server.ConnectUnary(),
			server.Local(cursorControlPlaneAction(
				host.controlPlaneAuth,
				routeDeps,
				"dashboard_get_managed_skills",
				upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
					Name:          "dashboard_get_managed_skills",
					StatusCode:    http.StatusOK,
					MockProtoType: "aiserver.v1.GetManagedSkillsResponse",
					MockBuilder:   upstream.DashboardManagedSkillsMockBuilder,
				}),
			)),
		),
		server.POST("/aiserver.v1.DashboardService/GetTeamAdminSettingsOrEmptyIfNotInTeam",
			server.Name("dashboard_get_team_admin_settings_or_empty"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_get_team_admin_settings_or_empty",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetTeamAdminSettingsResponse",
				MockBuilder:   upstream.EmptyMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetTeamReposOrEmptyIfNotInTeam",
			server.Name("dashboard_get_team_repos_or_empty"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_get_team_repos_or_empty",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetTeamReposResponse",
				MockBuilder:   upstream.EmptyMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetGlobalCommands",
			server.Name("dashboard_get_global_commands"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_get_global_commands",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetGlobalCommandsResponse",
				MockBuilder:   upstream.EmptyMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetCliDownloadUrl",
			server.Name("dashboard_get_cli_download_url"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_get_cli_download_url",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetCliDownloadUrlResponse",
				MockBuilder:   upstream.EmptyMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetMe",
			server.Name("dashboard_get_me"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_get_me",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetMeResponse",
				MockBuilder:   upstream.DashboardGetMeMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetUserPrivacyMode",
			server.Name("dashboard_user_privacy_mode"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_user_privacy_mode",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetUserPrivacyModeResponse",
				MockBuilder:   upstream.DashboardUserPrivacyModeMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetPlanInfo",
			server.Name("dashboard_plan_info"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_plan_info",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetPlanInfoResponse",
				MockBuilder:   upstream.DashboardPlanInfoMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/GetUsageLimitStatusAndActiveGrants",
			server.Name("dashboard_usage_limit_status"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_usage_limit_status",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.GetUsageLimitStatusAndActiveGrantsResponse",
				MockBuilder:   upstream.DashboardUsageLimitStatusAndActiveGrantsMockBuilder,
			})),
		),
		server.POST("/aiserver.v1.DashboardService/IsOnNewPricing",
			server.Name("dashboard_is_on_new_pricing"),
			server.ConnectUnary(),
			server.Local(upstream.MockProtoAction(routeDeps, upstream.CompatRouteConfig{
				Name:          "dashboard_is_on_new_pricing",
				StatusCode:    http.StatusOK,
				MockProtoType: "aiserver.v1.IsOnNewPricingResponse",
				MockBuilder:   upstream.DashboardIsOnNewPricingMockBuilder,
			})),
		),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/AddMarketplace", "dashboard_add_marketplace", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/AddMcpServersFromPlugin", "dashboard_add_mcp_servers_from_plugin", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/BatchGetPluginMcpConfig", "dashboard_batch_get_plugin_mcp_config", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/GetAvailableMcpServers", "dashboard_get_available_mcp_servers", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/GetEffectiveUserPlugins", "dashboard_get_effective_user_plugins", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/GetPlugin", "dashboard_get_plugin", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/GetPluginMcpConfig", "dashboard_get_plugin_mcp_config", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/InstallUserPlugin", "dashboard_install_user_plugin", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/ListMarketplacePlugins", "dashboard_list_marketplace_plugins", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/ListMarketplaces", "dashboard_list_marketplaces", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/ListUserPluginInstalls", "dashboard_list_user_plugin_installs", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/RefreshMarketplace", "dashboard_refresh_marketplace", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/RegisterMarketplaceAndPlugins", "dashboard_register_marketplace_and_plugins", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/RemoveMarketplace", "dashboard_remove_marketplace", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/ResolvePluginsByRef", "dashboard_resolve_plugins_by_ref", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/UninstallUserPlugin", "dashboard_uninstall_user_plugin", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.DashboardService/UpdateUserPluginInstall", "dashboard_update_user_plugin_install", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		cursorControlPlaneProcedure("/aiserver.v1.MCPRegistryService/GetKnownServers", "mcp_registry_get_known_servers", server.ConnectUnary(), host.controlPlaneAuth, routeDeps),
		server.Any("/aiserver.v1.DashboardService/*",
			server.Name("dashboard"),
			server.HTTP(),
			server.Local(func(ctx *server.Context) error {
				http.NotFound(ctx.Writer, ctx.Request)
				return nil
			}),
		),
		server.Any("/aiserver.v1.NetworkService/*",
			server.Name("network_service"),
			server.HTTP(),
			server.Local(func(ctx *server.Context) error {
				http.NotFound(ctx.Writer, ctx.Request)
				return nil
			}),
		),
		server.Any("/aiserver.v1.InAppAdService/*",
			server.Name("in_app_ad"),
			server.HTTP(),
			server.Local(func(ctx *server.Context) error {
				http.NotFound(ctx.Writer, ctx.Request)
				return nil
			}),
		),
		server.GET("/auth/full_stripe_profile",
			server.Name("auth_full_stripe_profile"),
			server.HTTP(),
			server.Local(upstream.MockAuthFullStripeProfileAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "auth_full_stripe_profile",
				StatusCode: http.StatusOK,
			})),
		),
		server.GET("/auth/stripe_profile",
			server.Name("auth_stripe_profile"),
			server.HTTP(),
			server.Local(upstream.MockAuthStripeProfileAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "auth_stripe_profile",
				StatusCode: http.StatusOK,
			})),
		),
		server.GET("/auth/has_valid_payment_method",
			server.Name("auth_has_valid_payment_method"),
			server.HTTP(),
			server.Local(upstream.MockJSONAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "auth_has_valid_payment_method",
				StatusCode: http.StatusOK,
				JSONBody: map[string]any{
					"hasValidPaymentMethod": true,
				},
			})),
		),
		server.Any("/auth/poll",
			server.Name("auth_poll"),
			server.HTTP(),
			server.Local(upstream.MockAuthPollAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "auth_poll",
				StatusCode: http.StatusOK,
			})),
		),
		server.POST("/auth/logout",
			server.Name("auth_logout"),
			server.HTTP(),
			server.Local(upstream.FixedStatusAction(routeDeps, upstream.CompatRouteConfig{
				Name:       "auth_logout",
				StatusCode: http.StatusNoContent,
			})),
		),
		server.Any("/auth/*",
			server.Name("auth_proxy"),
			server.HTTP(),
			server.Local(func(ctx *server.Context) error {
				http.NotFound(ctx.Writer, ctx.Request)
				return nil
			}),
		),
	)

	return nil
}

func repositoryServiceProcedure(pattern string, name string, protocol server.RouteOption, module *forwarder.Module) server.Option {
	localAction := server.HTTPHandlerAction(module.RepositoryServiceHandler)
	return server.POST(pattern,
		server.Name(name),
		protocol,
		server.Local(localAction),
	)
}

func uploadServiceProcedure(pattern string, name string, protocol server.RouteOption, module *forwarder.Module) server.Option {
	localAction := server.HTTPHandlerAction(module.UploadServiceHandler)
	return server.POST(pattern,
		server.Name(name),
		protocol,
		server.Local(localAction),
	)
}

func tabServerProcedure(pattern string, name string, protocol server.RouteOption, deps upstream.Dependencies) server.Option {
	forward := upstream.ForwardAction(deps, upstream.CompatRouteConfig{Name: name})
	action := func(ctx *server.Context) error {
		if ctx != nil && ctx.Request != nil && ctx.Request.URL != nil {
			baseURL, err := url.Parse(tabServerBaseURL)
			if err != nil {
				return fmt.Errorf("解析 tab server 地址失败: %w", err)
			}
			targetURL := *ctx.Request.URL
			targetURL.Scheme = baseURL.Scheme
			targetURL.Host = baseURL.Host
			ctx.UpstreamURL = &targetURL
		}
		return forward(ctx)
	}
	return server.POST(pattern,
		server.Name(name),
		protocol,
		server.Local(action),
	)
}

// writeGitCommitMessageDispatchProcedure 把 WriteGitCommitMessage 按用户配置的
// CommitMessageSource 分发到三种来源之一。配置每次请求时动态读取（Manager 自带
// 磁盘 reload），因此改完来源立即生效，无需重建路由表。
//
//   - local  → localHandler（forwarder agentModule.AiHandler）：本地 BYOK provider
//     生成，语言硬约束（commitLanguageHardPrompts）在此生效，跟随界面语言。
//   - leokun → 转发 https://tab.leokun.cn（原作者自建补全服务）。
//   - cursor → 走 Cursor 官方 api2.cursor.sh（需登录 Cursor 账号，未登录回退 leokun）。
func writeGitCommitMessageDispatchProcedure(
	pattern string,
	name string,
	protocol server.RouteOption,
	localHandler http.Handler,
	source func() string,
	authorizationProvider upstream.AuthorizationProvider,
	deps upstream.Dependencies,
) server.Option {
	leokunForward := tabServerForwardAction(name, deps)
	cursorForward := cursorControlPlaneAction(authorizationProvider, deps, name, leokunForward)
	local := server.HTTPHandlerAction(localHandler)
	action := func(ctx *server.Context) error {
		switch promptinject.NormalizeCommitMessageSource(source()) {
		case promptinject.CommitSourceCursor:
			logger.Infof("WriteGitCommitMessage dispatch source=cursor")
			return cursorForward(ctx)
		case promptinject.CommitSourceLeokun:
			logger.Infof("WriteGitCommitMessage dispatch source=leokun")
			return leokunForward(ctx)
		default:
			logger.Infof("WriteGitCommitMessage dispatch source=local")
			return local(ctx)
		}
	}
	return server.POST(pattern,
		server.Name(name),
		protocol,
		server.Local(action),
	)
}

// tabServerForwardAction 转发到 tabServerBaseURL（tab.leokun.cn）。
// 抽取自 tabServerProcedure 内部闭包，供分发 handler 与 cursor 未登录回退共用。
func tabServerForwardAction(name string, deps upstream.Dependencies) server.HandlerFunc {
	forward := upstream.ForwardAction(deps, upstream.CompatRouteConfig{Name: name})
	return func(ctx *server.Context) error {
		if ctx != nil && ctx.Request != nil && ctx.Request.URL != nil {
			baseURL, err := url.Parse(tabServerBaseURL)
			if err != nil {
				return fmt.Errorf("解析 tab server 地址失败: %w", err)
			}
			targetURL := *ctx.Request.URL
			targetURL.Scheme = baseURL.Scheme
			targetURL.Host = baseURL.Host
			ctx.UpstreamURL = &targetURL
		}
		return forward(ctx)
	}
}

func cursorControlPlaneProcedure(
	pattern string,
	name string,
	protocol server.RouteOption,
	authorizationProvider upstream.AuthorizationProvider,
	deps upstream.Dependencies,
) server.Option {
	notFound := func(ctx *server.Context) error {
		http.NotFound(ctx.Writer, ctx.Request)
		return nil
	}
	return server.POST(pattern,
		server.Name(name),
		protocol,
		server.Local(cursorControlPlaneAction(authorizationProvider, deps, name, notFound)),
	)
}

func cursorControlPlaneAction(
	authorizationProvider upstream.AuthorizationProvider,
	deps upstream.Dependencies,
	name string,
	fallback server.HandlerFunc,
) server.HandlerFunc {
	forward := upstream.AuthenticatedForwardAction(deps, upstream.CompatRouteConfig{Name: name}, authorizationProvider)
	return func(ctx *server.Context) error {
		if authorizationProvider == nil || !authorizationProvider.SignedIn() {
			return fallback(ctx)
		}
		if ctx == nil || ctx.Request == nil || ctx.Request.URL == nil {
			return fmt.Errorf("Cursor 控制面请求上下文无效")
		}
		targetURL := *ctx.Request.URL
		targetURL.Scheme = "https"
		targetURL.Host = "api2.cursor.sh:443"
		ctx.UpstreamURL = &targetURL
		return forward(ctx)
	}
}

type serverSystemSettings struct {
	configs *serverconfig.Manager
}

func (settings *serverSystemSettings) ResolveModelAdapters(ctx context.Context) ([]legacyruntime.ModelAdapterConfig, error) {
	snapshot, err := settings.configs.LegacyRuntimeSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.ModelAdapters, nil
}
