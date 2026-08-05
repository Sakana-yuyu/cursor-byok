package client

import (
	"context"
	"fmt"

	"cursor/internal/appdata"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/cursor"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// UserConfig 定义了当前模块中的 UserConfig 类型。
type UserConfig = serverconfig.Config

// LoadUserConfig 用于处理与 LoadUserConfig 相关的逻辑。
func (s *ProxyService) LoadUserConfig() (UserConfig, error) {
	if s == nil {
		return serverconfig.DefaultConfig(), nil
	}
	app := application.Get()
	ctx := context.Background()
	if app != nil {
		ctx = app.Context()
	}
	if s.backendHost != nil {
		return s.backendHost.LoadConfig(ctx)
	}
	if s.store == nil {
		return serverconfig.DefaultConfig(), nil
	}
	return s.store.Load(ctx)
}

// SaveUserConfig 用于处理与 SaveUserConfig 相关的逻辑。
func (s *ProxyService) SaveUserConfig(cfg UserConfig) error {
	if s == nil {
		return nil
	}
	app := application.Get()
	ctx := context.Background()
	if app != nil {
		ctx = app.Context()
	}
	oldConfig, _ := s.LoadUserConfig()
	var (
		normalized UserConfig
		err        error
	)
	if s.backendHost != nil {
		normalized, err = s.backendHost.SaveConfig(ctx, cfg)
	} else if s.store != nil {
		normalized, err = s.store.Save(ctx, cfg)
	} else {
		return nil
	}
	if err != nil {
		return err
	}
	// 直连模式（routingMode=upstream）：Cursor 直连官方，必须恢复官方登录态，
	// 否则 state.vscdb 中的模拟账号残留会导致官方连接 401/账号异常。
	// 恢复失败向上传播：配置虽已保存，但账号态未恢复，不能让调用方误以为一切正常。
	// 旧配置读取失败（oldErr != nil）时 oldConfig 为零值，Mode 非 upstream，
	// 同样走恢复兜底：状态未知时恢复官方态是更安全的选择。
	if normalized.Routing.Mode == "upstream" && oldConfig.Routing.Mode != "upstream" {
		if err := cursor.RestoreCursorUserInfo(); err != nil {
			return fmt.Errorf("restore cursor user info: %w", err)
		}
	}
	s.emitUserConfigChanged(normalized)
	return nil
}

func (s *ProxyService) emitUserConfigChanged(cfg UserConfig) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit("user-config:changed", cfg)
}

// resolveUserConfigPath 用于处理与 resolveUserConfigPath 相关的逻辑。
func resolveUserConfigPath() string {
	return appdata.ConfigFilePath()
}

// resolveLogsRootPath 用于处理与 resolveLogsRootPath 相关的逻辑。
func resolveLogsRootPath() string {
	return appdata.LogsRootPath()
}

// ResolveLogsRootPath 用于处理与 ResolveLogsRootPath 相关的逻辑。
func ResolveLogsRootPath() string {
	return resolveLogsRootPath()
}

// ResolveSettingsRootPath 用于处理与 ResolveSettingsRootPath 相关的逻辑。
func ResolveSettingsRootPath() string {
	return appdata.RootDir()
}
