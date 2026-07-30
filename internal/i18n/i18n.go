package i18n

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DefaultLocale is the safe fallback when a request does not carry locale data.
const DefaultLocale = "zh-CN"

const (
	LocaleZhCN = "zh-CN"
	LocaleEnUS = "en-US"
	LocaleJaJP = "ja-JP"
)

// Stable user-facing error codes. These codes are safe to expose to API clients
// and remain independent from provider diagnostics stored in Detail/Cause.
const (
	CodeInvalidModelAdapter = "model_adapter_invalid"
	CodeModelCatalog        = "model_catalog_failed"
	CodeInvalidRequest      = "request_invalid"
	CodeBackend             = "backend_failed"
	CodeProxy               = "proxy_failed"
	CodeCertificate         = "certificate_failed"
	CodeUpdate              = "update_failed"
	CodeProvider            = "provider_failed"
)

var messages = map[string]map[string]string{
	LocaleZhCN: {
		"app.name": "Cursor助手", "tray.status.not_started": "状态：未启动", "tray.status.running": "状态：运行中", "tray.start": "启动服务", "tray.stop": "停止服务", "tray.update": "检查更新", "tray.show": "显示主界面", "tray.show_stats": "显示统计浮窗", "tray.hide": "隐藏窗口", "tray.quit": "退出", "window.model_config": "模型配置", "window.model_add": "新增模型配置", "window.model_edit": "编辑模型配置",
		"error.update_manager_uninitialized": "更新管理器未初始化", "error.model_adapter.display_name_required": "模型适配器名称不能为空", "error.model_adapter.type_invalid": "模型适配器类型仅支持 openai 或 anthropic", "error.model_adapter.api_key_required": "模型适配器 API 密钥不能为空", "error.model_adapter.tooltip_required": "模型适配器说明不能为空", "error.model_adapter.model_id_required": "模型适配器模型 ID 不能为空", "error.model_adapter.reasoning_effort_invalid": "模型适配器推理强度无效", "error.model_adapter.endpoint_invalid": "模型适配器 OpenAI 端点无效", "error.model_adapter.json_required": "模型适配器参数必须是合法 JSON 对象", "error.model_adapter.headers_invalid": "模型适配器自定义请求头无效", "error.model_adapter.thinking_effort_invalid": "模型适配器思考强度无效", "error.model_catalog.request_failed": "模型列表请求失败", "error.model_catalog.response_invalid": "模型列表响应无效", "error.model_catalog.empty": "模型列表中没有可用模型", "error.request.invalid": "请求参数无效", "error.backend.failed": "后端处理失败", "error.proxy.failed": "代理处理失败", "error.certificate.failed": "证书处理失败", "error.update.failed": "更新失败", "error.provider.failed": "模型服务请求失败",
	},
	LocaleEnUS: {
		"app.name": "Cursor Assistant", "tray.status.not_started": "Status: Not Started", "tray.status.running": "Status: Running", "tray.start": "Start Service", "tray.stop": "Stop Service", "tray.update": "Check for Updates", "tray.show": "Show Main Window", "tray.show_stats": "Show Stats Overlay", "tray.hide": "Hide Window", "tray.quit": "Exit", "window.model_config": "Model Configuration", "window.model_add": "Add Model Configuration", "window.model_edit": "Edit Model Configuration",
		"error.update_manager_uninitialized": "Update manager is not initialized", "error.model_adapter.display_name_required": "Model adapter name is required", "error.model_adapter.type_invalid": "Model adapter type must be openai or anthropic", "error.model_adapter.api_key_required": "Model adapter API key is required", "error.model_adapter.tooltip_required": "Model adapter description is required", "error.model_adapter.model_id_required": "Model adapter model ID is required", "error.model_adapter.reasoning_effort_invalid": "Model adapter reasoning effort is invalid", "error.model_adapter.endpoint_invalid": "OpenAI model adapter endpoint is invalid", "error.model_adapter.json_required": "Model adapter parameters must be a valid JSON object", "error.model_adapter.headers_invalid": "Model adapter custom headers are invalid", "error.model_adapter.thinking_effort_invalid": "Model adapter thinking effort is invalid", "error.model_catalog.request_failed": "Model list request failed", "error.model_catalog.response_invalid": "Model list response is invalid", "error.model_catalog.empty": "No usable models were returned", "error.request.invalid": "Invalid request", "error.backend.failed": "Backend processing failed", "error.proxy.failed": "Proxy processing failed", "error.certificate.failed": "Certificate processing failed", "error.update.failed": "Update failed", "error.provider.failed": "Model service request failed",
	},
	LocaleJaJP: {
		"app.name": "Cursorアシスタント", "tray.status.not_started": "状態：未起動", "tray.status.running": "状態：実行中", "tray.start": "サービス起動", "tray.stop": "サービス停止", "tray.update": "アップデートを確認", "tray.show": "メイン画面を表示", "tray.show_stats": "統計オーバーレイを表示", "tray.hide": "ウィンドウを非表示", "tray.quit": "終了", "window.model_config": "モデル設定", "window.model_add": "モデル設定を追加", "window.model_edit": "モデル設定を編集",
		"error.update_manager_uninitialized": "更新マネージャーが初期化されていません", "error.model_adapter.display_name_required": "モデルアダプター名は必須です", "error.model_adapter.type_invalid": "モデルアダプターの種類は openai または anthropic です", "error.model_adapter.api_key_required": "モデルアダプターの API キーは必須です", "error.model_adapter.tooltip_required": "モデルアダプターの説明は必須です", "error.model_adapter.model_id_required": "モデルアダプターのモデル ID は必須です", "error.model_adapter.reasoning_effort_invalid": "モデルアダプターの推論強度が無効です", "error.model_adapter.endpoint_invalid": "OpenAI モデルアダプターのエンドポイントが無効です", "error.model_adapter.json_required": "モデルアダプターのパラメーターは有効な JSON オブジェクトである必要があります", "error.model_adapter.headers_invalid": "モデルアダプターのカスタムヘッダーが無効です", "error.model_adapter.thinking_effort_invalid": "モデルアダプターの思考強度が無効です", "error.model_catalog.request_failed": "モデル一覧の取得に失敗しました", "error.model_catalog.response_invalid": "モデル一覧の応答が無効です", "error.model_catalog.empty": "使用可能なモデルがありません", "error.request.invalid": "リクエストパラメーターが無効です", "error.backend.failed": "バックエンド処理に失敗しました", "error.proxy.failed": "プロキシ処理に失敗しました", "error.certificate.failed": "証明書処理に失敗しました", "error.update.failed": "更新に失敗しました", "error.provider.failed": "モデルサービスへのリクエストに失敗しました",
	},
}

// UserError carries a stable key/code while retaining diagnostic detail and
// the original cause for logs, errors.Is, and API compatibility.
type UserError struct {
	Key    string
	CodeID string
	Detail string
	Cause  error
}

func NewError(key, code, detail string) *UserError {
	return &UserError{Key: key, CodeID: code, Detail: detail}
}
func WrapError(key, code, detail string, cause error) *UserError {
	return &UserError{Key: key, CodeID: code, Detail: detail, Cause: cause}
}
func (err *UserError) Error() string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Detail) != "" {
		return err.Detail
	}
	return T(DefaultLocale, err.Key)
}
func (err *UserError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
func (err *UserError) Code() string {
	if err == nil {
		return ""
	}
	return err.CodeID
}
func (err *UserError) Message(locale string) string {
	if err == nil {
		return ""
	}
	message := T(locale, err.Key)
	if detail := strings.TrimSpace(err.Detail); detail != "" && detail != message {
		return message + ": " + detail
	}
	return message
}

// Display localizes only errors explicitly marked as user-facing. Unknown
// errors retain their original diagnostic text instead of hiding information.
func Display(locale string, err error) string {
	if err == nil {
		return ""
	}
	var userErr *UserError
	if errors.As(err, &userErr) {
		return userErr.Message(locale)
	}
	return err.Error()
}

type localeContextKey struct{}

func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeContextKey{}, Normalize(locale))
}
func LocaleFromContext(ctx context.Context) string {
	if ctx != nil {
		if locale, ok := ctx.Value(localeContextKey{}).(string); ok {
			return Normalize(locale)
		}
	}
	return DefaultLocale
}

// Normalize maps browser/system locale variants to the supported locale set.
func Normalize(locale string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(locale, "_", "-"))) {
	case "en", "en-us":
		return LocaleEnUS
	case "ja", "ja-jp":
		return LocaleJaJP
	case "zh", "zh-cn", "zh-hans":
		return LocaleZhCN
	default:
		return DefaultLocale
	}
}

func T(locale, key string) string {
	locale = Normalize(locale)
	if catalog := messages[locale]; catalog != nil {
		if value, ok := catalog[key]; ok {
			return value
		}
	}
	if catalog := messages[DefaultLocale]; catalog != nil {
		if value, ok := catalog[key]; ok {
			return value
		}
	}
	return key
}

// FormatError keeps diagnostics available while producing a localized prefix.
func FormatError(locale, key, code, detail string, cause error) error {
	return WrapError(key, code, fmt.Sprint(detail), cause)
}
