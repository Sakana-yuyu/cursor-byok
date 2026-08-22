package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io/fs"
	"net"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"cursor/internal/ads"
	"cursor/internal/appdata"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/buildinfo"
	"cursor/internal/client"
	"cursor/internal/cursor"
	"cursor/internal/historymetrics"
	"cursor/internal/i18n"
	"cursor/internal/skills"

	"github.com/leaanthony/u"

	bridge "cursor/internal/bridge"
	"cursor/internal/certs"
	"cursor/internal/logger"
	"cursor/internal/mitm"
	"cursor/internal/netproxy"
	"cursor/internal/safego"
	"cursor/internal/updater"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	// appNameKey 是应用名称的统一翻译键。
	appNameKey = "app.name"
	// adRefreshInterval 表示后台广告拉取间隔。
	adRefreshInterval = 3 * time.Minute
)

// autoMatchOnce 保证「启动时自动配对上下文窗口」在同一进程内只执行一次。
var autoMatchOnce sync.Once

// syncSkillsOnce 保证「启动时释放内置技能」在同一进程内只执行一次。
var syncSkillsOnce sync.Once

// EmbeddedResources 定义了当前模块中的 EmbeddedResources 类型。
type EmbeddedResources struct {
	// Assets 表示当前声明中的 Assets。
	Assets fs.FS
	// AppIcon 表示当前声明中的 AppIcon。
	AppIcon []byte
	// TrayIcon 表示当前声明中的 TrayIcon。
	TrayIcon []byte
}

// init 用于处理与 init 相关的逻辑。
func init() {
	application.RegisterEvent[bridge.ProxyState]("proxy:state")
	application.RegisterEvent[bridge.UserConfig]("user-config:changed")
	application.RegisterEvent[bridge.ModelAdapterTestResultsPayload]("model-adapter-test:updated")
	application.RegisterEvent[client.ProviderBalancesSyncedPayload]("provider-balances-synced")
	application.RegisterEvent[bridge.AdRuntime](ads.EventUpdated)
	application.RegisterEvent[updater.StatePayload](updater.EventState)
	application.RegisterEvent[updater.ProgressPayload](updater.EventProgress)
	application.RegisterEvent[updater.ReadyPayload](updater.EventReady)
	application.RegisterEvent[updater.ErrorPayload](updater.EventError)
}

// Run 用于处理与 Run 相关的逻辑。
func Run(resources EmbeddedResources) error {
	logger.Init()
	netproxy.InstallDefaultTransport()
	appName := i18n.T(i18n.DefaultLocale, appNameKey)
	startupStart := time.Now()
	logStartupPhase := func(name string) {
		logger.Infof("startup phase=%s elapsed=%s", name, time.Since(startupStart).Round(time.Millisecond))
	}

	certManager, certErr := certs.NewPersistentManager(appdata.CACertFilePath(), appdata.CAKeyFilePath())
	if certErr != nil {
		// CA 材料不完整（cert/key 仅存其一，常见于 ca.key 被杀软/同步/误删）：
		// 自动修复（备份残留 + 重新生成 + 标记 RepairedAt），避免降级死锁。
		// 与 cert/key 失配的自动修复行为对齐：重建后启动流程自动重装信任，
		// 首页横幅提示重启 Cursor（非整个应用）。
		if certs.IsIncompleteCA(certErr) {
			logger.Infof("CA material incomplete, auto-repairing at startup: %v", certErr)
			repaired, _, repairErr := certs.RepairAndReloadManager(appdata.CACertFilePath(), appdata.CAKeyFilePath())
			if repairErr != nil {
				// 修复失败仍降级（权限等），保留原 certErr 上报。
				logger.Errorf("CA auto-repair failed, degraded startup (MITM disabled): %v", repairErr)
				certManager = nil
			} else {
				certManager = repaired
				certErr = nil
				logger.Infof("CA auto-repaired at startup, proxy will start with new CA (restart Cursor to take effect)")
			}
		} else {
			// 降级启动：CA 任何初始化失败都不中止应用--GUI 必须能打开，
			// 本地代理停用（MITM disabled）；材料不完整时首页提供「一键修复」入口，
			// 其余错误同样展示在首页，避免「应用打不开、无法自救」的死锁。
			logger.Errorf("CA init failed, degraded startup (MITM disabled): %v", certErr)
			certManager = nil
		}
	}
	var embeddedCACertPEM []byte
	if certManager != nil {
		embeddedCACertPEM = certManager.CACertPEM()
	}
	logEmbeddedCAInfo(embeddedCACertPEM)
	logStartupPhase("certs-ready")

	defaultBackendBaseURL := "http://" + serverconfig.DefaultBackendListenAddr
	// 镜像记录配置使用应用生命周期上下文：Manager 在启动时加载一次，
	// 之后每次读取都会热加载（reloadIfChanged），无需持有可取消的 ctx。
	runCtx := context.Background()
	mirrorStore := serverconfig.NewStore(appdata.ConfigFilePath(), appdata.LogsRootPath())
	mirrorCfg, mirrorCfgErr := serverconfig.NewManager(runCtx, mirrorStore)
	if mirrorCfgErr != nil {
		// 配置管理器初始化失败不影响启动：镜像开关关闭（镜像记录不启用），回落语义不变。
		logger.Warnf("init mirror capture config manager failed: %v", mirrorCfgErr)
	}
	proxyServer, err := mitm.NewProxyServer(serverconfig.DefaultProxyListenAddr, defaultBackendBaseURL, appdata.HistoryRootPath(), mirrorCfg, certManager)
	if err != nil {
		return err
	}
	proxyService := bridge.NewProxyService(proxyServer, certManager, embeddedCACertPEM)
	logStartupPhase("proxy-service")
	if certErr != nil {
		proxyService.MarkCAIncomplete(certErr.Error())
	}
	adAssetBaseURL := defaultBackendBaseURL
	if cfg, err := proxyService.LoadUserConfig(); err == nil {
		adAssetBaseURL = browserReachableLoopbackBaseURL(cfg.BackendListenAddr)
	}
	metricsService := bridge.NewMetricsService(func() bool {
		cfg, err := proxyService.LoadUserConfig()
		if err != nil {
			return false
		}
		return cfg.HomeMetrics.IncludeCacheWriteInHitRate
	}, func() []historymetrics.PriceRate {
		cfg, err := proxyService.LoadUserConfig()
		if err != nil {
			return nil
		}
		return serverconfig.PriceRatesFromAdapters(cfg.ModelAdapters)
	}, proxyService.ResetUsageMetrics)
	windowService := bridge.NewWindowService()
	windowService.SetCursorLaunchPreflight(proxyService.PrepareCursorLaunch)
	bridge.WireCursorRuntime(proxyService, windowService)
	adCore := ads.NewService(ads.Options{
		StoreRoot:    appdata.AdsRootPath(),
		HTTPClient:   netproxy.NewHTTPClient(30 * time.Second),
		AppVersion:   buildinfo.CurrentVersion(),
		AssetBaseURL: adAssetBaseURL + ads.RoutePrefix,
		DeviceID:     cursor.GetDeviceID,
		Metrics: func(context.Context) (ads.MetricsSnapshot, error) {
			if err := appdata.EnsureAssistantHome(); err != nil {
				return ads.MetricsSnapshot{}, err
			}
			summary, err := historymetrics.LoadUsageSummary(appdata.UsageFilePath(), false, nil)
			if err != nil {
				return ads.MetricsSnapshot{}, err
			}
			return ads.MetricsSnapshot{
				TurnsTotal:         summary.TurnsTotal,
				RequestTokensTotal: summary.RequestTokensTotal,
				PromptTokensTotal:  summary.PromptTokensTotal,
				CacheReadTokens:    summary.CacheReadTokens,
				CacheWriteTokens:   summary.CacheWriteTokens,
			}, nil
		},
		ProviderCount: func(context.Context) (int, error) {
			cfg, err := proxyService.LoadUserConfig()
			if err != nil {
				return 0, err
			}
			return len(cfg.ModelAdapters), nil
		},
	})
	adService := bridge.NewAdService(adCore)
	logStartupPhase("services")
	var updateManager *updater.Manager

	var mainWindow *application.WebviewWindow
	adRefreshCtx, stopAdRefresh := context.WithCancel(context.Background())

	app := application.New(application.Options{
		Name:        appName,
		Description: appName,
		Services: []application.Service{
			application.NewService(proxyService),
			application.NewService(metricsService),
			application.NewService(windowService),
			application.NewService(adService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(resources.Assets),
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			stopAdRefresh()
			if updateManager != nil {
				updateManager.Shutdown()
			}
			proxyService.ShutdownForQuit()
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.cursor-assistant.single-instance",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				logger.Infof("检测到实例请求，已忽略")
				// 不激活窗口，避免干扰用户工作
			},
		},
	})

	refreshAdAssetBaseURL := func() bool {
		state := proxyService.GetState()
		backendListenAddr := strings.TrimSpace(state.BackendListenAddr)
		if backendListenAddr == "" {
			backendListenAddr = serverconfig.DefaultBackendListenAddr
		}
		return adCore.SetAssetBaseURL(browserReachableLoopbackBaseURL(backendListenAddr) + ads.RoutePrefix)
	}
	refreshAdRuntime := func() {
		runtimeState, err := adCore.GetRuntime(context.Background())
		if err != nil {
			return
		}
		app.Event.Emit(ads.EventUpdated, runtimeState)
	}
	refreshAd := func(ctx context.Context) {
		if ctx == nil {
			ctx = context.Background()
		}
		runtimeState, changed, err := adCore.Refresh(ctx)
		if err != nil || !changed {
			return
		}
		app.Event.Emit(ads.EventUpdated, runtimeState)
	}
	refreshAdAsync := func() {
		safego.Go("ad:refresh", func() {
			refreshAd(context.Background())
		})
	}
	startAdRefreshLoop := func(ctx context.Context) {
		safego.Go("ad:refresh-loop", func() {
			refreshAd(ctx)
			ticker := time.NewTicker(adRefreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refreshAd(ctx)
				}
			}
		})
	}

	logStartupPhase("application-new")
	updateManager = updater.NewManager(app)

	windowService.SetApp(app)
	windowService.SetUpdater(updateManager)

	mainWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:               appName,
		Width:               1280,
		Height:              860,
		MinWidth:            960,
		MinHeight:           640,
		DisableResize:       false,
		Frameless:           goruntime.GOOS == "windows",
		URL:                 "/#/",
		Hidden:              false,
		HideOnEscape:        false,
		MinimiseButtonState: application.ButtonEnabled,
		MaximiseButtonState: application.ButtonEnabled,
		CloseButtonState:    application.ButtonEnabled,
		BackgroundColour:    application.RGBA{Red: 25, Green: 25, Blue: 25, Alpha: 255},
		Mac: application.MacWindow{
			Backdrop:      application.MacBackdropLiquidGlass,
			DisableShadow: false,
			TitleBar: application.MacTitleBar{
				AppearsTransparent:   true,
				Hide:                 false,
				HideTitle:            true,
				FullSizeContent:      true,
				UseToolbar:           false,
				HideToolbarSeparator: true,
			},
			WebviewPreferences: application.MacWebviewPreferences{
				FullscreenEnabled:                   u.True,
				TextInteractionEnabled:              u.True,
				AllowsBackForwardNavigationGestures: u.False,
			},
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: false,
		},
	})

	logStartupPhase("window-created")
	window := mainWindow
	windowService.SetMainWindow(window)
	quitting := false
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting {
			return
		}
		if windowService.GetMainWindowCloseAction() == "quit" {
			quitting = true
			e.Cancel()
			windowService.CloseApplication()
			return
		}
		window.Hide()
		e.Cancel()
	})
	window.RegisterHook(events.Common.WindowFocus, func(e *application.WindowEvent) {
		refreshAdAsync()
	})

	showMainWindow := func() {
		// 托盘恢复始终回到固定主页，避免复用主窗口当前的子路由。
		window.SetURL("/#/")
		window.Show().Focus()
	}
	toggleMainWindow := func() {
		if window.IsVisible() {
			window.Hide()
			return
		}
		showMainWindow()
	}

	systray := app.SystemTray.New()
	menu := app.Menu.New()
	statusItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.status.not_started")).SetEnabled(false)
	menu.AddSeparator()
	startItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.start"))
	stopItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.stop"))
	updateItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.update")).OnClick(func(ctx *application.Context) {
		updateManager.CheckNow(true)
	})
	menu.AddSeparator()
	showItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.show")).OnClick(func(ctx *application.Context) {
		showMainWindow()
	})
	showStatsItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.show_stats")).OnClick(func(ctx *application.Context) {
		// 由主窗口通过现有偏好状态恢复坐标、样式和置顶设置；无有效坐标时沿用默认定位。
		window.ExecJS(`window.dispatchEvent(new Event("stats-overlay-show-requested"));`)
	})
	hideItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.hide")).OnClick(func(ctx *application.Context) {
		window.Hide()
	})
	menu.AddSeparator()
	quitItem := menu.Add(i18n.T(i18n.DefaultLocale, "tray.quit")).OnClick(func(ctx *application.Context) {
		proxyService.ShutdownForQuit()
		app.Quit()
	})

	var currentLocale = i18n.DefaultLocale

	updateTrayLabels := func(locale string) {
		currentLocale = i18n.Normalize(locale)
		state := proxyService.GetState()
		statusKey := "tray.status.not_started"
		if state.Running {
			statusKey = "tray.status.running"
		}
		statusItem.SetLabel(i18n.T(currentLocale, statusKey))
		startItem.SetLabel(i18n.T(currentLocale, "tray.start"))
		stopItem.SetLabel(i18n.T(currentLocale, "tray.stop"))
		updateItem.SetLabel(i18n.T(currentLocale, "tray.update"))
		showItem.SetLabel(i18n.T(currentLocale, "tray.show"))
		showStatsItem.SetLabel(i18n.T(currentLocale, "tray.show_stats"))
		hideItem.SetLabel(i18n.T(currentLocale, "tray.hide"))
		quitItem.SetLabel(i18n.T(currentLocale, "tray.quit"))
		systray.SetTooltip(i18n.T(currentLocale, appNameKey))
	}

	refreshTray := func() {
		state := proxyService.GetState()
		if state.Running {
			startItem.SetEnabled(false)
			stopItem.SetEnabled(true)
		} else {
			startItem.SetEnabled(true)
			stopItem.SetEnabled(false)
		}
		updateTrayLabels(currentLocale)
		if refreshAdAssetBaseURL() {
			refreshAdRuntime()
		}
	}

	// The frontend may emit its initial locale before this callback can observe it.
	// Keep native UI on the safe Chinese default, then correct it on the next
	// locale:changed event; this preserves the existing Wails event API.
	app.Event.On("locale:changed", func(e *application.CustomEvent) {
		if locale, ok := e.Data.(string); ok {
			windowService.SetLocale(locale)
			updateTrayLabels(locale)
		}
	})
	app.Event.On("proxy:state", func(event *application.CustomEvent) {
		refreshTray()
	})
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(event *application.ApplicationEvent) {
		logger.Infof("应用版本：v%s", buildinfo.CurrentVersion())
		updateManager.Start()
		startAdRefreshLoop(adRefreshCtx)
		safego.Go("app:autostart", func() {
			logger.Infof("application started, begin auto start service in background")
			// 释放内置技能（find-skills + superpowers）到 ~/.cursor/skills/，
			// 让 Cursor 客户端在所有项目的 / 菜单里列出。仅写入缺失/变化的文件，不删用户自加技能。
			// 失败仅记日志、不阻断启动。
			syncSkillsOnce.Do(func() {
				if syncResult, syncErr := skills.SyncToCursorSkillsDir(""); syncErr != nil {
					logger.Errorf("释放内置技能失败: %v", syncErr)
				} else if syncResult.Written > 0 {
					logger.Infof("内置技能已释放: total=%d written=%d skipped=%d failed=%d",
						syncResult.Total, syncResult.Written, syncResult.Skipped, syncResult.Failed)
				}
			})
			if _, err := proxyService.StartProxy(); err != nil {
				logger.Errorf("自动启动服务失败: %v", err)
			} else {
				state := proxyService.GetState()
				if refreshAdAssetBaseURL() {
					refreshAdRuntime()
				}
				logger.Infof("代理已自动启动: %s", state.ProxyListenAddr)
				// 自动配对需要访问供应商模型目录，不能占用本地代理的启动关键路径。
				safego.Go("app:auto-match", func() {
					autoMatchOnce.Do(func() {
						if matchResult, matchErr := proxyService.AutoMatchContextWindows(context.Background(), false); matchErr != nil {
							logger.Errorf("自动配对上下文窗口失败: %v", matchErr)
						} else if matchResult.Enabled {
							logger.Infof("自动配对上下文窗口完成: total=%d from_catalog=%d from_probe=%d unchanged=%d changed=%t",
								matchResult.Total, matchResult.FromCatalog, matchResult.FromProbe, matchResult.Unchanged, matchResult.Changed)
						}
					})
				})
			}
		})
	})

	startItem.OnClick(func(ctx *application.Context) {
		if _, err := proxyService.StartProxy(); err != nil {
			logger.Errorf("启动服务失败: %v", err)
		} else if refreshAdAssetBaseURL() {
			refreshAdRuntime()
		}
		refreshTray()
	})
	stopItem.OnClick(func(ctx *application.Context) {
		if _, err := proxyService.StopProxy(); err != nil {
			logger.Errorf("停止服务失败: %v", err)
		}
		refreshTray()
	})

	if len(resources.AppIcon) > 0 {
		switch goruntime.GOOS {
		case "darwin":
			systray.SetTemplateIcon(resources.TrayIcon)
		case "windows":
			systray.SetIcon(resources.AppIcon)
		default:
			systray.SetIcon(resources.TrayIcon)
		}
	}
	systray.SetTooltip(appName)
	systray.OnClick(toggleMainWindow).SetMenu(menu)
	refreshTray()

	logStartupPhase("before-run")
	return app.Run()
}

func browserReachableLoopbackBaseURL(listenAddr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil || strings.TrimSpace(port) == "" {
		return "http://" + serverconfig.DefaultBackendListenAddr
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// 价格快照构建（手动价/探测价 > 内置官方价 > 均价估算）由
// serverconfig.PriceRatesFromAdapters 提供，goal 循环费用估算与统计页共用同一实现。

// logEmbeddedCAInfo 用于处理与 logEmbeddedCAInfo 相关的逻辑。
func logEmbeddedCAInfo(certPEM []byte) {
	if len(certPEM) == 0 {
		logger.Errorf("embedded CA is empty")
		return
	}
	cert, err := parseEmbeddedCert(certPEM)
	if err != nil {
		logger.Errorf("parse embedded CA failed: %v", err)
		return
	}
	sum := sha256.Sum256(cert.Raw)
	logger.Infof(
		"embedded CA loaded: sha256=%s subject=%s valid=%s~%s",
		strings.ToUpper(hex.EncodeToString(sum[:])),
		cert.Subject.String(),
		cert.NotBefore.Format(time.RFC3339),
		cert.NotAfter.Format(time.RFC3339),
	)
}

// parseEmbeddedCert 用于处理与 parseEmbeddedCert 相关的逻辑。
func parseEmbeddedCert(data []byte) (*x509.Certificate, error) {
	if block, _ := pem.Decode(data); block != nil {
		return x509.ParseCertificate(block.Bytes)
	}
	return x509.ParseCertificate(data)
}
