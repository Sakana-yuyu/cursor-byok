package bridge

import (
	"archive/zip"
	"cursor/internal/buildinfo"
	"cursor/internal/client"
	"cursor/internal/i18n"
	"cursor/internal/logger"
	"cursor/internal/updater"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leaanthony/u"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// modelEditorContext 保存当前模型编辑器窗口的初始化上下文。
type modelEditorContext struct {
	Index       int    `json:"index"`
	AdapterJSON string `json:"adapterJSON"`
}

// WindowService 定义了当前模块中的 WindowService 类型。
type WindowService struct {
	app                   *application.App
	updater               *updater.Manager
	mainWindow            *application.WebviewWindow
	modelConfigWindow     *application.WebviewWindow
	modelEditorWindow     *application.WebviewWindow
	metricsDetailWindow   *application.WebviewWindow
	requestMetricsWindow  *application.WebviewWindow
	statsOverlayWindow    *application.WebviewWindow
	editorCtx             *modelEditorContext
	locale                string
	mainWindowCloseAction string
	mu                    sync.RWMutex
}

// NewWindowService 用于处理与 NewWindowService 相关的逻辑。
func NewWindowService() *WindowService {
	return &WindowService{locale: i18n.DefaultLocale, mainWindowCloseAction: "tray"}
}

// SetLocale updates the locale used for subsequently created native windows.
func (s *WindowService) SetLocale(locale string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locale = i18n.Normalize(locale)
}

// SetApp 用于处理与 SetApp 相关的逻辑。
func (s *WindowService) SetApp(app *application.App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.app = app
}

// SetMainWindow 关联主窗口，供浮窗关闭按钮按关闭策略隐藏主窗口或退出应用。
func (s *WindowService) SetMainWindow(window *application.WebviewWindow) {
	s.mu.Lock()
	s.mainWindow = window
	s.mu.Unlock()
}

// SetMainWindowCloseAction 设置主窗口关闭时隐藏到托盘或直接退出。
func (s *WindowService) SetMainWindowCloseAction(action string) {
	if action != "quit" {
		action = "tray"
	}
	s.mu.Lock()
	s.mainWindowCloseAction = action
	s.mu.Unlock()
}

// GetMainWindowCloseAction 返回主窗口当前的关闭策略。
func (s *WindowService) GetMainWindowCloseAction() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mainWindowCloseAction == "quit" {
		return "quit"
	}
	return "tray"
}

// CloseApplication 请求应用退出，由应用 OnShutdown 统一清理代理和更新服务。
func (s *WindowService) CloseApplication() {
	s.mu.RLock()
	app := s.app
	mainWindow := s.mainWindow
	action := s.mainWindowCloseAction
	s.mu.RUnlock()
	if action != "quit" {
		if mainWindow != nil {
			mainWindow.Hide()
		}
		return
	}
	if app != nil {
		app.Quit()
	}
}

// SetUpdater 关联更新管理器，供前端手动触发检查更新。
func (s *WindowService) SetUpdater(manager *updater.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updater = manager
}

// GetAppVersion 返回当前应用版本号。
func (s *WindowService) GetAppVersion() string {
	return buildinfo.CurrentVersion()
}

// CheckForUpdates 触发一次手动检查更新。
func (s *WindowService) CheckForUpdates() {
	s.mu.RLock()
	manager := s.updater
	s.mu.RUnlock()
	if manager == nil {
		return
	}
	manager.CheckNow(true)
}

// InstallReadyUpdate 安装当前已下载完成的更新。
func (s *WindowService) InstallReadyUpdate() error {
	s.mu.RLock()
	manager := s.updater
	locale := s.locale
	s.mu.RUnlock()
	if manager == nil {
		return fmt.Errorf("%s", i18n.T(locale, "error.update_manager_uninitialized"))
	}
	return manager.InstallReadyUpdate()
}

// OpenConfigWindow 打开本地设置目录。
func (s *WindowService) OpenConfigWindow() {
	_ = os.MkdirAll(client.ResolveSettingsRootPath(), 0o755)
	openDirectory(client.ResolveSettingsRootPath())
}

// OpenModelConfigWindow 打开模型配置独立窗口。如果窗口已存在则聚焦。
func (s *WindowService) OpenModelConfigWindow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.app == nil {
		return
	}

	if s.modelConfigWindow != nil {
		s.modelConfigWindow.Show()
		s.modelConfigWindow.Focus()
		return
	}

	win := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:               i18n.T(s.locale, "window.model_config"),
		Width:               980,
		Height:              700,
		MinWidth:            820,
		MinHeight:           560,
		DisableResize:       false,
		Frameless:           goruntime.GOOS == "windows",
		URL:                 "/#/model-config",
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

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.modelConfigWindow = nil
	})

	s.modelConfigWindow = win
}

// OpenMetricsDetailWindow 打开会话分析独立窗口。如果窗口已存在则聚焦。
func (s *WindowService) OpenMetricsDetailWindow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.app == nil {
		return
	}

	if s.metricsDetailWindow != nil {
		s.metricsDetailWindow.Show()
		s.metricsDetailWindow.Focus()
		return
	}

	win := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:               "会话分析",
		Width:               1100,
		Height:              760,
		MinWidth:            900,
		MinHeight:           600,
		DisableResize:       false,
		Frameless:           goruntime.GOOS == "windows",
		URL:                 "/#/metrics-detail",
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

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.metricsDetailWindow = nil
	})

	s.metricsDetailWindow = win
}

// OpenRequestMetricsWindow 打开请求明细独立窗口。如果窗口已存在则聚焦。
func (s *WindowService) OpenRequestMetricsWindow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.app == nil {
		return
	}

	if s.requestMetricsWindow != nil {
		s.requestMetricsWindow.Show()
		s.requestMetricsWindow.Focus()
		return
	}

	win := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:               "请求明细",
		Width:               1100,
		Height:              760,
		MinWidth:            900,
		MinHeight:           600,
		DisableResize:       false,
		Frameless:           goruntime.GOOS == "windows",
		URL:                 "/#/request-metrics",
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

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requestMetricsWindow = nil
	})

	s.requestMetricsWindow = win
}

const (
	statsOverlayStyleCard   = "card"
	statsOverlayStyleEngine = "engine"
	statsOverlayStyleOrb    = "orb"
	statsOverlayDockSize    = 44
	statsOverlayDockWidth   = 112
)

type statsOverlayLayout struct {
	style                     string
	edge                      string
	collapsed                 bool
	x, y                      int
	screenLeft, screenTop     int
	screenWidth, screenHeight int
}

// parseStatsOverlayLayout 解析前端发送的内部布局 DSL：
// layout|collapsed/expanded|edge|style|x|y|screenLeft|screenTop|screenWidth|screenHeight。
func parseStatsOverlayLayout(value string) (statsOverlayLayout, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 10 || parts[0] != "layout" {
		return statsOverlayLayout{}, false
	}
	parseInt := func(raw string) (int, bool) {
		parsed, err := strconv.Atoi(raw)
		return parsed, err == nil
	}
	x, okX := parseInt(parts[4])
	y, okY := parseInt(parts[5])
	screenLeft, okLeft := parseInt(parts[6])
	screenTop, okTop := parseInt(parts[7])
	screenWidth, okWidth := parseInt(parts[8])
	screenHeight, okHeight := parseInt(parts[9])
	if !okX || !okY || !okLeft || !okTop || !okWidth || !okHeight || screenWidth <= 0 || screenHeight <= 0 {
		return statsOverlayLayout{}, false
	}
	return statsOverlayLayout{
		style: parts[3], edge: parts[2], collapsed: parts[1] == "collapsed",
		x: x, y: y, screenLeft: screenLeft, screenTop: screenTop,
		screenWidth: screenWidth, screenHeight: screenHeight,
	}, true
}

func statsOverlayWindowSize(style string) (width, height int) {
	switch style {
	case statsOverlayStyleEngine:
		return 240, 124
	case statsOverlayStyleOrb:
		return 176, 176
	default:
		return 240, 144
	}
}

func clampStatsOverlayCoordinate(value, minimum, maximum int) int {
	if maximum < minimum {
		return minimum
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (s *WindowService) validateStatsOverlayPosition(x, y, width, height int) (int, int, bool) {
	if s.app == nil || s.app.Screen == nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	screen := s.app.Screen.ScreenNearestDipRect(application.Rect{X: x, Y: y, Width: width, Height: height})
	if screen == nil || screen.WorkArea.Width <= 0 || screen.WorkArea.Height <= 0 {
		return 0, 0, false
	}
	workArea := screen.WorkArea
	maxX := workArea.X + workArea.Width - width
	maxY := workArea.Y + workArea.Height - height
	return clampStatsOverlayCoordinate(x, workArea.X, maxX), clampStatsOverlayCoordinate(y, workArea.Y, maxY), true
}

// OpenStatsOverlayWindow 打开统计浮窗（置顶、无边框、小尺寸）。
// 用于在任意应用上方常驻显示缓存命中率、Token 消耗、对话轮次、价值估算。
// x, y 为窗口位置；hasPosition=false 时由系统决定初始位置。
// 如果窗口已存在则确保它处于显示状态。
func (s *WindowService) OpenStatsOverlayWindow(x, y int, hasPosition bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.app == nil {
		return
	}
	width, height := statsOverlayWindowSize(statsOverlayStyleCard)

	// 已存在 -> 确保显示，不执行 toggle，避免设置开关与窗口状态竞争。
	if s.statsOverlayWindow != nil {
		if hasPosition {
			currentWidth, currentHeight := s.statsOverlayWindow.Size()
			if currentWidth > 0 && currentHeight > 0 {
				width, height = currentWidth, currentHeight
			}
			x, y, hasPosition = s.validateStatsOverlayPosition(x, y, width, height)
		}
		if hasPosition {
			s.statsOverlayWindow.SetPosition(x, y)
		}
		if !s.statsOverlayWindow.IsVisible() {
			s.statsOverlayWindow.Show()
		}
		s.statsOverlayWindow.Focus()
		return
	}

	if hasPosition {
		x, y, hasPosition = s.validateStatsOverlayPosition(x, y, width, height)
	}
	opts := application.WebviewWindowOptions{
		Title:               "统计浮窗",
		Width:               width,
		Height:              height,
		DisableResize:       true,
		Frameless:           true,
		AlwaysOnTop:         true,
		BackgroundType:      application.BackgroundTypeTransparent,
		URL:                 "/#/stats-overlay",
		Hidden:              false,
		HideOnEscape:        false,
		MinimiseButtonState: application.ButtonDisabled,
		MaximiseButtonState: application.ButtonDisabled,
		CloseButtonState:    application.ButtonDisabled,
		BackgroundColour:    application.RGBA{Alpha: 0},
		Mac: application.MacWindow{
			Backdrop:      application.MacBackdropTransparent,
			DisableShadow: true,
			TitleBar: application.MacTitleBar{
				AppearsTransparent: true,
				Hide:               true,
				HideTitle:          true,
				FullSizeContent:    true,
				UseToolbar:         false,
			},
			WebviewPreferences: application.MacWebviewPreferences{
				FullscreenEnabled:                   u.True,
				TextInteractionEnabled:              u.True,
				AllowsBackForwardNavigationGestures: u.False,
			},
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
			// 显式指定扩展窗口样式，覆盖 Wails 默认为 HiddenOnTaskbar 设置的
			// WS_EX_NOACTIVATE(0x08000000)。NOACTIVATE 会使浮窗永远无法成为前台窗口，
			// 导致主窗口隐藏后线程失去前台、浮窗无法接管 -> startDrag 的 capture 握手失败，
			// 运行时 drag.js 的 dragging 标志卡死并吞掉所有点击，界面整体不可点击
			// （需 Alt-Tab 强制重分配前台才能恢复）。
			// 改用 WS_EX_TOOLWINDOW(0x00000080) 实现任务栏隐藏但允许激活；保留
			// WS_EX_TOPMOST(0x00000008) 置顶、WS_EX_LAYERED(0x00080000) 透明分层、
			// WS_EX_CONTROLPARENT(0x00010000) 与 Wails 默认一致；不含 WS_EX_TRANSPARENT
			// 以确保点击命中浮窗本身而非穿透。
			ExStyle: 0x00000080 | 0x00000008 | 0x00080000 | 0x00010000,
		},
	}
	win := s.app.Window.NewWithOptions(opts)

	// 如果提供了保存的位置，在窗口创建后恢复
	if hasPosition {
		win.SetPosition(x, y)
	}

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.statsOverlayWindow = nil
	})

	s.statsOverlayWindow = win
}

// UpdateStatsOverlayWindow 更新现有统计浮窗的样式尺寸和贴边布局。
// style 既可传普通样式，也可传 parseStatsOverlayLayout 支持的内部布局 DSL。
func (s *WindowService) UpdateStatsOverlayWindow(style string, alwaysOnTop bool) {
	layout, hasLayout := parseStatsOverlayLayout(style)
	if hasLayout {
		style = layout.style
	}
	width, height := statsOverlayWindowSize(style)
	if hasLayout && layout.collapsed {
		// 贴左/右时只收缩宽度，保留恢复态的完整高度；贴上/下时只收缩高度，
		// 保留恢复态的完整宽度。这样展开/收缩只沿远离贴边的一侧进行，
		// 鼠标所在的贴边坐标不会因原生窗口换尺寸而漂移。
		if layout.edge == "left" || layout.edge == "right" {
			width = statsOverlayDockSize
		} else if layout.edge == "top" || layout.edge == "bottom" {
			height = statsOverlayDockSize - 8
		} else {
			// 锁定但尚未吸附到具体边缘时保留紧凑横向胶囊。
			width, height = statsOverlayDockWidth, statsOverlayDockSize-8
		}
	}

	s.mu.RLock()
	win := s.statsOverlayWindow
	s.mu.RUnlock()
	if win == nil {
		return
	}

	win.SetSize(width, height)
	if hasLayout {
		// 胶囊不再使用 CSS 贴边位移，因此原生窗口直接贴屏幕边缘；
		// 窗口内的 flex 居中会自然保留胶囊自身的内边距。
		const snapInset = 0
		x, y := layout.x, layout.y
		screenRight := layout.screenLeft + layout.screenWidth
		screenBottom := layout.screenTop + layout.screenHeight
		switch layout.edge {
		case "left":
			x = layout.screenLeft + snapInset
		case "right":
			x = screenRight - width - snapInset
		case "top":
			y = layout.screenTop + snapInset
		case "bottom":
			y = screenBottom - height - snapInset
		}
		if x < layout.screenLeft {
			x = layout.screenLeft
		}
		if y < layout.screenTop {
			y = layout.screenTop
		}
		if x+width > screenRight {
			x = screenRight - width
		}
		if y+height > screenBottom {
			y = screenBottom - height
		}
		win.SetPosition(x, y)
	}
	// 保留参数以兼容现有绑定；置顶状态由 SetStatsOverlayAlwaysOnTop 独立维护。
	_ = alwaysOnTop
}

// SetStatsOverlayAlwaysOnTop 独立更新浮窗层级，避免尺寸/morph 调用携带旧偏好覆盖最新设置。
func (s *WindowService) SetStatsOverlayAlwaysOnTop(alwaysOnTop bool) {
	s.mu.RLock()
	win := s.statsOverlayWindow
	s.mu.RUnlock()
	if win != nil {
		win.SetAlwaysOnTop(alwaysOnTop)
	}
}

// CloseStatsOverlayWindow 关闭统计浮窗。
func (s *WindowService) CloseStatsOverlayWindow() {
	s.mu.Lock()
	win := s.statsOverlayWindow
	s.statsOverlayWindow = nil
	s.mu.Unlock()
	if win != nil {
		win.Close()
	}
}

// OpenModelEditorWindow 打开模型编辑器独立窗口。
// index < 0 表示新增，>= 0 表示编辑对应索引的适配器。
// index < 0 表示新增，>= 0 表示编辑对应索引的适配器。
// adapterJSON 为编辑器初始数据的 JSON 字符串。
func (s *WindowService) OpenModelEditorWindow(index int, adapterJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.app == nil {
		return
	}

	s.editorCtx = &modelEditorContext{
		Index:       index,
		AdapterJSON: adapterJSON,
	}

	if s.modelEditorWindow != nil {
		s.modelEditorWindow.Show()
		s.modelEditorWindow.Focus()
		return
	}

	title := i18n.T(s.locale, "window.model_add")
	if index >= 0 {
		title = i18n.T(s.locale, "window.model_edit")
	}

	win := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:               title,
		Width:               840,
		Height:              680,
		MinWidth:            740,
		MinHeight:           600,
		DisableResize:       false,
		Frameless:           goruntime.GOOS == "windows",
		URL:                 fmt.Sprintf("/#/model-editor?index=%d", index),
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
				FullscreenEnabled:                   u.False,
				TextInteractionEnabled:              u.True,
				AllowsBackForwardNavigationGestures: u.False,
			},
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: false,
		},
	})

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.modelEditorWindow = nil
		s.editorCtx = nil
	})

	s.modelEditorWindow = win
}

// GetModelEditorContext 返回当前编辑器窗口的初始化上下文。
func (s *WindowService) GetModelEditorContext() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.editorCtx == nil {
		return map[string]any{
			"index":       -1,
			"adapterJSON": "{}",
		}
	}
	return map[string]any{
		"index":       s.editorCtx.Index,
		"adapterJSON": s.editorCtx.AdapterJSON,
	}
}

// OpenHistoryWindow 用于处理与 OpenHistoryWindow 相关的逻辑。
func (s *WindowService) OpenHistoryWindow() {
	_ = os.MkdirAll(client.ResolveLogsRootPath(), 0o755)
	openDirectory(client.ResolveLogsRootPath())
}

// ExportLogs 将日志目录打包为 zip 文件，返回 zip 文件路径。
// zip 文件保存在日志目录内，命名为 export-YYYYMMDD-HHMMSS.zip。
func (s *WindowService) ExportLogs() (string, error) {
	logsDir := client.ResolveLogsRootPath()
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return "", fmt.Errorf("创建日志目录失败: %w", err)
	}

	zipName := fmt.Sprintf("export-%s.zip", time.Now().Format("20060102-150405"))
	zipPath := filepath.Join(logsDir, zipName)

	zipFile, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("创建导出文件失败: %w", err)
	}
	defer func() { _ = zipFile.Close() }()

	zipWriter := zip.NewWriter(zipFile)

	walkErr := filepath.Walk(logsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || path == zipPath {
			return nil
		}
		relPath, relErr := filepath.Rel(logsDir, path)
		if relErr != nil {
			return relErr
		}
		writer, createErr := zipWriter.Create(relPath)
		if createErr != nil {
			return createErr
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = file.Close() }()
		_, copyErr := io.Copy(writer, file)
		return copyErr
	})

	closeErr := zipWriter.Close()
	if walkErr != nil {
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("打包日志失败: %w", walkErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("完成打包失败: %w", closeErr)
	}

	return zipPath, nil
}

// openDirectory 用于处理与 openDirectory 相关的逻辑。
func openDirectory(path string) {
	if path == "" {
		return
	}
	switch goruntime.GOOS {
	case "darwin":
		_ = exec.Command("open", path).Start()
	case "windows":
		_ = exec.Command("explorer", path).Start()
	default:
		_ = exec.Command("xdg-open", path).Start()
	}
}

func validExecutablePath(rawPath string) string {
	path := strings.TrimSpace(strings.Trim(rawPath, `"`))
	path = strings.TrimSuffix(path, ",0")
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			path = filepath.Join(path, "Cursor.exe")
		}
		if info, err = os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func cursorRegistryCandidates() []string {
	if goruntime.GOOS != "windows" {
		return nil
	}
	keys := []string{
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall`,
		`HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall`,
		`HKLM\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
	}
	var candidates []string
	for _, key := range keys {
		output, err := exec.Command("reg", "query", key, "/s", "/v", "InstallLocation").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(output), "\n") {
			index := strings.Index(line, "REG_SZ")
			if index < 0 {
				continue
			}
			location := strings.TrimSpace(line[index+len("REG_SZ"):])
			if path := validExecutablePath(location); path != "" {
				candidates = append(candidates, path)
				continue
			}
			if path := validExecutablePath(filepath.Join(location, "Cursor.exe")); path != "" {
				candidates = append(candidates, path)
			}
		}
	}
	return candidates
}

// DetectCursorPath 检测 Cursor 编辑器的安装路径，manualPath 非空时优先使用。
// 返回可执行文件的完整路径，如果未检测到则返回空字符串。
func (s *WindowService) DetectCursorPath(manualPath string) string {
	if strings.TrimSpace(manualPath) != "" {
		return validExecutablePath(manualPath)
	}

	var candidates []string
	switch goruntime.GOOS {
	case "windows":
		// 优先解析 PATH 中 cursor 启动脚本指向的真实 Cursor.exe（跳过 .cmd/.bat 本身，
		// 直接执行会弹 cmd 窗口）。解析不到再回退常规安装路径。
		if path := findCursorExecutableOnPath(); path != "" {
			candidates = append(candidates, path)
		}
		candidates = append(candidates,
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Cursor", "Cursor.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "cursor", "Cursor.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Cursor", "Cursor.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES"), "Cursor", "Cursor.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Cursor", "Cursor.exe"),
			filepath.Join(os.Getenv("ProgramW6432"), "Cursor", "Cursor.exe"),
		)
		candidates = append(candidates, cursorRegistryCandidates()...)
	case "darwin":
		candidates = append(candidates, "/Applications/Cursor.app/Contents/MacOS/Cursor")
	case "linux":
		if path, err := exec.LookPath("cursor"); err == nil {
			candidates = append(candidates, path)
		}
		candidates = append(candidates,
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "cursor"),
			"/usr/bin/cursor",
			"/usr/local/bin/cursor",
		)
	}
	for _, candidate := range candidates {
		if path := validExecutablePath(candidate); path != "" {
			return path
		}
	}
	return ""
}

// LaunchCursor 启动 Cursor 编辑器。如果提供了 workspaceDir，则在该目录中打开。
func (s *WindowService) LaunchCursor(workspaceDir, manualPath string) error {
	cursorPath := s.DetectCursorPath(manualPath)
	if cursorPath == "" {
		if strings.TrimSpace(manualPath) != "" {
			return fmt.Errorf("指定的 Cursor 路径无效：%s", strings.TrimSpace(manualPath))
		}
		return fmt.Errorf("未检测到 Cursor 安装路径，请在设置中指定 Cursor.exe")
	}

	logger.Infof("launch cursor: path=%s workspace=%q", cursorPath, workspaceDir)
	if err := launchCursorProcess(cursorPath, workspaceDir); err != nil {
		logger.Errorf("launch cursor failed: path=%s err=%v", cursorPath, err)
		return fmt.Errorf("启动 Cursor 失败: %w", err)
	}
	logger.Infof("launch cursor ok: path=%s", cursorPath)
	return nil
}
