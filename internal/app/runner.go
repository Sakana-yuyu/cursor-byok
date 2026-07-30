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
	"cursor/internal/cursor"
	"cursor/internal/historymetrics"
	"cursor/internal/i18n"
	"cursor/internal/modelcontext"
	"cursor/internal/skills"

	"github.com/leaanthony/u"

	bridge "cursor/internal/bridge"
	"cursor/internal/certs"
	"cursor/internal/logger"
	"cursor/internal/mitm"
	"cursor/internal/netproxy"
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

	embeddedCACertPEM := certs.EmbeddedCACertPEM()
	logEmbeddedCAInfo(embeddedCACertPEM)

	certManager, err := certs.NewEmbeddedManager()
	if err != nil {
		return err
	}

	defaultBackendBaseURL := "http://" + serverconfig.DefaultBackendListenAddr
	proxyServer, err := mitm.NewProxyServer(serverconfig.DefaultProxyListenAddr, defaultBackendBaseURL, "", "", certManager)
	if err != nil {
		return err
	}
	proxyService := bridge.NewProxyService(proxyServer, certManager, embeddedCACertPEM)
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
		return priceRatesFromAdapters(cfg.ModelAdapters)
	})
	windowService := bridge.NewWindowService()
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
		go func() {
			refreshAd(context.Background())
		}()
	}
	startAdRefreshLoop := func(ctx context.Context) {
		go func() {
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
		}()
	}

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

	window := mainWindow
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
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
	toggleStatsOverlay := func() {
		// 点击托盘打开统计浮窗（屏幕中心）
		windowService.OpenStatsOverlayWindow(0, 0)
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
		go func() {
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
			// 启动时自动配对上下文窗口（受 autoMatchContextWindow 开关控制）：
			// 目录命中则覆盖为真实窗口，目录未命中则探测 provider /models 回填。
			// 失败仅记日志、不阻断启动。
			autoMatchOnce.Do(func() {
				if matchResult, matchErr := proxyService.AutoMatchContextWindows(context.Background()); matchErr != nil {
					logger.Errorf("自动配对上下文窗口失败: %v", matchErr)
				} else if matchResult.Enabled {
					logger.Infof("自动配对上下文窗口完成: total=%d from_catalog=%d from_probe=%d unchanged=%d changed=%t",
						matchResult.Total, matchResult.FromCatalog, matchResult.FromProbe, matchResult.Unchanged, matchResult.Changed)
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
			}
		}()
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
	systray.OnClick(toggleStatsOverlay).SetMenu(menu)
	refreshTray()

	// 启动托盘统计轮换显示
	startTrayStatsRotation(app, systray, metricsService, currentLocale)

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

// priceRatesFromAdapters 将当前配置的模型渠道价格映射为 historymetrics 价格条目快照。
// 价格优先级：手动配价 / catalog 探测价（adapter.Pricing）> 内置官方价（modelcontext.BuiltinPricingFor）。
// 内置价格仅运行时注入，不写入 config.yaml。
func priceRatesFromAdapters(adapters []serverconfig.ModelAdapterConfig) []historymetrics.PriceRate {
	rates := make([]historymetrics.PriceRate, 0, len(adapters))
	for _, adapter := range adapters {
		pricing := adapter.Pricing
		if pricing != nil {
			rates = append(rates, historymetrics.PriceRate{
				Model:      adapter.ModelID,
				Provider:   adapter.Type,
				BaseURL:    adapter.BaseURL,
				Input:      pricing.Input,
				Output:     pricing.Output,
				CacheRead:  pricing.CacheRead,
				CacheWrite: pricing.CacheWrite,
				Currency:   pricing.Currency,
				Known:      pricing.Known,
			})
			continue
		}
		// adapter 未配置价格 → 尝试内置官方价兜底。
		builtin := modelcontext.BuiltinPricingFor(adapter.ModelID)
		if builtin == nil {
			continue
		}
		rates = append(rates, historymetrics.PriceRate{
			Model:     adapter.ModelID,
			Provider:  adapter.Type,
			BaseURL:   adapter.BaseURL,
			Input:     builtin.Input,
			Output:    builtin.Output,
			CacheRead: builtin.CacheRead,
			CacheWrite: builtin.CacheWrite,
			Currency:  "USD",
			Known:     true, // 内置价视为已知，让花费估算能显示数值
		})
	}
	return rates
}

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

// startTrayStatsRotation 启动托盘统计信息轮换显示
func startTrayStatsRotation(app *application.App, systray *application.SystemTray, metricsService *bridge.MetricsService, locale string) {
	go func() {
		displayIndex := 0
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		updateTrayStats := func() {
			summary, err := metricsService.GetHomeMetricsSummary()
			if err != nil {
				return
			}

			// 计算金额（简化版，假设 $0.01/1M tokens）
			estimatedCost := float64(summary.RequestTokensTotal) / 1000000.0 * 0.01

			var label string
			switch displayIndex % 4 {
			case 0:
				// 显示金额
				label = formatMoney(estimatedCost)
			case 1:
				// 显示缓存命中率
				if summary.CacheHitRate != nil {
					label = formatPercent(*summary.CacheHitRate)
				} else {
					label = "📊 --"
				}
			case 2:
				// 显示 Token 消耗
				label = formatTokens(summary.RequestTokensTotal)
			case 3:
				// 显示对话轮次
				label = formatTurns(summary.TurnsTotal)
			}

			// 更新 Tooltip 显示所有详情
			tooltip := formatTooltip(summary, estimatedCost, locale)
			
			app.DispatchSync(func() {
				systray.SetLabel(label)
				systray.SetTooltip(tooltip)
			})

			displayIndex++
		}

		// 立即更新一次
		updateTrayStats()

		// 定时轮换
		for range ticker.C {
			updateTrayStats()
		}
	}()
}

func formatMoney(cost float64) string {
	return formatValue("💰", cost, "$%.2f")
}

func formatPercent(rate float64) string {
	return formatValue("📊", rate*100, "%.1f%%")
}

func formatTokens(tokens int64) string {
	if tokens >= 1000000 {
		return formatValue("🔢", float64(tokens)/1000000.0, "%.1fM")
	}
	if tokens >= 1000 {
		return formatValue("🔢", float64(tokens)/1000.0, "%.1fK")
	}
	return formatValue("🔢", float64(tokens), "%.0f")
}

func formatTurns(turns int) string {
	return formatValue("🔄", float64(turns), "%.0f")
}

func formatValue(emoji string, value float64, format string) string {
	formatted := ""
	switch format {
	case "$%.2f":
		formatted = "$" + formatFloat(value, 2)
	case "%.1f%%":
		formatted = formatFloat(value, 1) + "%"
	case "%.1fM":
		formatted = formatFloat(value, 1) + "M"
	case "%.1fK":
		formatted = formatFloat(value, 1) + "K"
	case "%.0f":
		formatted = formatFloat(value, 0)
	}
	return emoji + " " + formatted
}

func formatFloat(value float64, precision int) string {
	var format string
	switch precision {
	case 0:
		format = "%.0f"
	case 1:
		format = "%.1f"
	case 2:
		format = "%.2f"
	default:
		format = "%.2f"
	}
	result := u.Sprintf(format, value)
	// 移除尾随的零和小数点
	if precision > 0 && strings.Contains(result, ".") {
		result = strings.TrimRight(result, "0")
		result = strings.TrimRight(result, ".")
	}
	return result
}

func formatTooltip(summary bridge.HomeMetricsSummary, cost float64, locale string) string {
	hitRate := "--"
	if summary.CacheHitRate != nil {
		hitRate = formatFloat(*summary.CacheHitRate*100, 1) + "%"
	}

	tokensStr := formatFloat(float64(summary.RequestTokensTotal)/1000000.0, 1) + "M"
	if summary.RequestTokensTotal < 1000000 {
		tokensStr = formatFloat(float64(summary.RequestTokensTotal)/1000.0, 1) + "K"
	}

	if locale == "en-US" {
		return u.Sprintf(
			"═════════════════\n💰 Cost: $%.2f\n📊 Cache Hit: %s\n🔢 Tokens: %s\n🔄 Turns: %d\n═════════════════\nClick to view details",
			cost, hitRate, tokensStr, summary.TurnsTotal,
		)
	}
	if locale == "ja-JP" {
		return u.Sprintf(
			"═════════════════\n💰 コスト: $%.2f\n📊 キャッシュ: %s\n🔢 トークン: %s\n🔄 ターン: %d\n═════════════════\nクリックして詳細を表示",
			cost, hitRate, tokensStr, summary.TurnsTotal,
		)
	}
	return u.Sprintf(
		"═════════════════\n💰 消费: $%.2f\n📊 缓存命中: %s\n🔢 Token: %s\n🔄 轮次: %d\n═════════════════\n点击查看详情",
		cost, hitRate, tokensStr, summary.TurnsTotal,
	)
}
