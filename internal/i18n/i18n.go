package i18n

import (
	"errors"
	"strings"
	"sync/atomic"
)

// DefaultLocale is the safe fallback when a request does not carry locale data.
const DefaultLocale = "zh-CN"

const (
	LocaleZhCN = "zh-CN"
	LocaleEnUS = "en-US"
	LocaleJaJP = "ja-JP"
	LocaleRuRU = "ru-RU"
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
		"hint.grok_multi_agent_no_client_tools": "该模型为 Grok 多代理变体，不支持客户端工具调用（function calling），因此无法在 Cursor 中执行工具类操作。请在模型配置中改用 grok-4.5、grok-4.3 等支持工具调用的模型。",
	},
	LocaleEnUS: {
		"app.name": "Cursor Assistant", "tray.status.not_started": "Status: Not Started", "tray.status.running": "Status: Running", "tray.start": "Start Service", "tray.stop": "Stop Service", "tray.update": "Check for Updates", "tray.show": "Show Main Window", "tray.show_stats": "Show Stats Overlay", "tray.hide": "Hide Window", "tray.quit": "Exit", "window.model_config": "Model Configuration", "window.model_add": "Add Model Configuration", "window.model_edit": "Edit Model Configuration",
		"error.update_manager_uninitialized": "Update manager is not initialized", "error.model_adapter.display_name_required": "Model adapter name is required", "error.model_adapter.type_invalid": "Model adapter type must be openai or anthropic", "error.model_adapter.api_key_required": "Model adapter API key is required", "error.model_adapter.tooltip_required": "Model adapter description is required", "error.model_adapter.model_id_required": "Model adapter model ID is required", "error.model_adapter.reasoning_effort_invalid": "Model adapter reasoning effort is invalid", "error.model_adapter.endpoint_invalid": "OpenAI model adapter endpoint is invalid", "error.model_adapter.json_required": "Model adapter parameters must be a valid JSON object", "error.model_adapter.headers_invalid": "Model adapter custom headers are invalid", "error.model_adapter.thinking_effort_invalid": "Model adapter thinking effort is invalid", "error.model_catalog.request_failed": "Model list request failed", "error.model_catalog.response_invalid": "Model list response is invalid", "error.model_catalog.empty": "No usable models were returned", "error.request.invalid": "Invalid request", "error.backend.failed": "Backend processing failed", "error.proxy.failed": "Proxy processing failed", "error.certificate.failed": "Certificate processing failed", "error.update.failed": "Update failed", "error.provider.failed": "Model service request failed",
		"hint.grok_multi_agent_no_client_tools": "This Grok multi-agent variant does not support client-side tool calling (function calling), so it cannot perform tool actions in Cursor. Switch to a tool-capable model such as grok-4.5 or grok-4.3 in your model configuration.",
	},
	LocaleJaJP: {
		"app.name": "Cursorアシスタント", "tray.status.not_started": "状態：未起動", "tray.status.running": "状態：実行中", "tray.start": "サービス起動", "tray.stop": "サービス停止", "tray.update": "アップデートを確認", "tray.show": "メイン画面を表示", "tray.show_stats": "統計オーバーレイを表示", "tray.hide": "ウィンドウを非表示", "tray.quit": "終了", "window.model_config": "モデル設定", "window.model_add": "モデル設定を追加", "window.model_edit": "モデル設定を編集",
		"error.update_manager_uninitialized": "更新マネージャーが初期化されていません", "error.model_adapter.display_name_required": "モデルアダプター名は必須です", "error.model_adapter.type_invalid": "モデルアダプターの種類は openai または anthropic です", "error.model_adapter.api_key_required": "モデルアダプターの API キーは必須です", "error.model_adapter.tooltip_required": "モデルアダプターの説明は必須です", "error.model_adapter.model_id_required": "モデルアダプターのモデル ID は必須です", "error.model_adapter.reasoning_effort_invalid": "モデルアダプターの推論強度が無効です", "error.model_adapter.endpoint_invalid": "OpenAI モデルアダプターのエンドポイントが無効です", "error.model_adapter.json_required": "モデルアダプターのパラメーターは有効な JSON オブジェクトである必要があります", "error.model_adapter.headers_invalid": "モデルアダプターのカスタムヘッダーが無効です", "error.model_adapter.thinking_effort_invalid": "モデルアダプターの思考強度が無効です", "error.model_catalog.request_failed": "モデル一覧の取得に失敗しました", "error.model_catalog.response_invalid": "モデル一覧の応答が無効です", "error.model_catalog.empty": "使用可能なモデルがありません", "error.request.invalid": "リクエストパラメーターが無効です", "error.backend.failed": "バックエンド処理に失敗しました", "error.proxy.failed": "プロキシ処理に失敗しました", "error.certificate.failed": "証明書処理に失敗しました", "error.update.failed": "更新に失敗しました", "error.provider.failed": "モデルサービスへのリクエストに失敗しました",
		"hint.grok_multi_agent_no_client_tools": "この Grok マルチエージェント系モデルはクライアント側ツール呼び出し（function calling）に対応していないため、Cursor でツール操作を実行できません。モデル設定で grok-4.5 や grok-4.3 などツール対応モデルに切り替えてください。",
	},
	LocaleRuRU: {
		"app.name": "Помощник Cursor", "tray.status.not_started": "Статус: не запущен", "tray.status.running": "Статус: выполняется", "tray.start": "Запустить службу", "tray.stop": "Остановить службу", "tray.update": "Проверить обновления", "tray.show": "Показать главное окно", "tray.show_stats": "Показать панель статистики", "tray.hide": "Скрыть окно", "tray.quit": "Выход", "window.model_config": "Конфигурация моделей", "window.model_add": "Добавить конфигурацию модели", "window.model_edit": "Изменить конфигурацию модели",
		"error.update_manager_uninitialized": "Менеджер обновлений не инициализирован", "error.model_adapter.display_name_required": "Укажите имя адаптера модели", "error.model_adapter.type_invalid": "Тип адаптера модели должен быть openai или anthropic", "error.model_adapter.api_key_required": "Укажите API-ключ адаптера модели", "error.model_adapter.tooltip_required": "Укажите описание адаптера модели", "error.model_adapter.model_id_required": "Укажите идентификатор модели адаптера", "error.model_adapter.reasoning_effort_invalid": "Недопустимое значение силы рассуждений адаптера модели", "error.model_adapter.endpoint_invalid": "Недопустимая конечная точка OpenAI адаптера модели", "error.model_adapter.json_required": "Параметры адаптера модели должны быть корректным объектом JSON", "error.model_adapter.headers_invalid": "Недопустимые пользовательские заголовки адаптера модели", "error.model_adapter.thinking_effort_invalid": "Недопустимое значение силы мышления адаптера модели", "error.model_catalog.request_failed": "Не удалось получить список моделей", "error.model_catalog.response_invalid": "Некорректный ответ списка моделей", "error.model_catalog.empty": "Не возвращено ни одной доступной модели", "error.request.invalid": "Некорректный запрос", "error.backend.failed": "Сбой внутренней обработки", "error.proxy.failed": "Сбой обработки прокси", "error.certificate.failed": "Сбой обработки сертификата", "error.update.failed": "Сбой обновления", "error.provider.failed": "Сбой запроса к службе модели",
		"hint.grok_multi_agent_no_client_tools": "Этот многоагентный вариант Grok не поддерживает вызов клиентских инструментов (function calling) и не может выполнять инструментальные действия в Cursor. Переключитесь на модель с поддержкой инструментов, например grok-4.5 или grok-4.3, в настройках модели.",
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

// Normalize maps browser/system locale variants to the supported locale set.
func Normalize(locale string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(locale, "_", "-"))) {
	case "en", "en-us":
		return LocaleEnUS
	case "ja", "ja-jp":
		return LocaleJaJP
	case "ru", "ru-ru":
		return LocaleRuRU
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

// currentLocaleHolder 保存进程级 UI 语言，供无 locale 上下文的错误提示（如 forwarder
// provider 错误收口）本地化使用。由 WindowService.SetLocale 在收到前端 locale:changed
// 事件时更新；测试可通过 SetCurrentLocale 覆盖。默认 DefaultLocale。
var currentLocaleHolder atomic.Value

func init() {
	currentLocaleHolder.Store(DefaultLocale)
}

// CurrentLocale 返回最近一次由前端设定的 UI 语言；未设定时回退 DefaultLocale。
func CurrentLocale() string {
	if v, ok := currentLocaleHolder.Load().(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return DefaultLocale
}

// SetCurrentLocale 更新进程级 UI 语言；输入会先经 Normalize 归一化到受支持的 locale。
func SetCurrentLocale(locale string) {
	currentLocaleHolder.Store(Normalize(locale))
}
