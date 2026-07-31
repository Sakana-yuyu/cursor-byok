import { computed, reactive, watchSyncEffect } from "vue";
import { runtimeEvents } from "@/services/runtimeAdapter";
import dayjs from "dayjs";
import { getLocale } from "@/i18n/runtime";
import { contextWindowTokensForModel } from "@/utils/modelContext";
import { adapterMatchesSupplierIdentity } from "@/utils/supplierGrouping";
import {
  checkForUpdates,
  getAppVersion,
  getHomeMetricsSummary,
  getModelAdapterTestResults,
  installReadyUpdate,
  getProxyState,
  openConfigWindow as openConfig,
  loadUserConfig,
  openLogsDirectory,
  exportLogs,
  openModelConfig,
  openModelEditor,
  openMetricsDetailWindow as openMetricsDetail,
  openRequestMetricsWindow as openRequestMetrics,
  openStatsOverlayWindow,
  setStatsOverlayAlwaysOnTop,
  closeStatsOverlayWindow,
  setMainWindowCloseAction,
  closeApplication as closeApplicationNative,
  saveUserConfig,
  startProxyService,
  stopProxyService,
  testModelAdapter,
} from "@/services/clientApi";

const APP_STATE_STORAGE_KEY = "cursor-client:runtime-state:v2";
const GENERIC_SERVICE_ERROR = "服务错误";
const SUPPORTED_MODEL_ADAPTER_TYPES = new Set(["openai", "anthropic", "gemini"]);
const SUPPORTED_REASONING_EFFORTS = new Set(["low", "medium", "high", "xhigh", "max"]);
const SUPPORTED_ANTHROPIC_THINKING_EFFORTS = new Set(["low", "medium", "high", "xhigh", "max"]);
export const ANTHROPIC_THINKING_EFFORT_DEFAULT = "xhigh";
export const OPENAI_ENDPOINT_RESPONSES = "/v1/responses";
export const OPENAI_ENDPOINT_CHAT_COMPLETIONS = "/v1/chat/completions";
export const OPENAI_ENDPOINT_CUSTOM = "/custom";
export const OPENAI_REQUEST_GROUP_RESPONSES = "responses";
export const OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS = "chat_completions";
export const OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT = "chat_completions_compat";
export const PROTOCOL_MODE_AUTO = "auto";
export const PROTOCOL_MODE_FIXED = "fixed";
export const PROTOCOL_GROUP_ANTHROPIC_MESSAGES = "messages";
export const PROTOCOL_GROUP_GEMINI_NATIVE = "gemini_native";
export const OPENAI_EXTRA_PARAMS_DEFAULT_JSON = `{
}`;
export const EXTRA_PARAMS_DEFAULT_JSON = `{
}`;
export const CUSTOM_HEADERS_DEFAULT_JSON = `{
}`;
export const BALANCE_QUERY_HEADERS_DEFAULT_JSON = `{
}`;
const SUPPORTED_OPENAI_ENDPOINTS = new Set([OPENAI_ENDPOINT_RESPONSES, OPENAI_ENDPOINT_CHAT_COMPLETIONS, OPENAI_ENDPOINT_CUSTOM]);
const SUPPORTED_OPENAI_REQUEST_GROUPS = new Set([
  OPENAI_REQUEST_GROUP_RESPONSES,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT,
]);
const SUPPORTED_PROTOCOL_MODES = new Set([PROTOCOL_MODE_AUTO, PROTOCOL_MODE_FIXED]);
const SUPPORTED_ROUTE_MODES = new Set(["local", "upstream"]);
const PROXY_STATE_EVENT = "proxy:state";
const USER_CONFIG_CHANGED_EVENT = "user-config:changed";
const UPDATE_STATE_EVENT = "update:state";
const UPDATE_PROGRESS_EVENT = "update:progress";
const UPDATE_READY_EVENT = "update:ready";
const UPDATE_ERROR_EVENT = "update:error";
const MODEL_ADAPTER_TEST_UPDATED_EVENT = "model-adapter-test:updated";
const SUPPORTED_MODEL_ADAPTER_TEST_STATUSES = new Set(["idle", "running", "success", "error"]);
const HOME_METRICS_MIN_LOADING_MS = 600;

export const ROUTE_MODE_OPTIONS = [
  { label: "本地服务模式", value: "local" },
  { label: "直连 Cursor 模式", value: "upstream" },
];

function asString(value) {
  if (typeof value === "string") {
    return value.trim();
  }
  if (value instanceof String) {
    return value.toString().trim();
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return "";
}

function asBoolean(value, fallback = false) {
  if (typeof value === "boolean") {
    return value;
  }
  if (typeof value === "number") {
    return value !== 0;
  }
  const normalized = asString(value).toLowerCase();
  if (!normalized) {
    return fallback;
  }
  return normalized === "true" || normalized === "1" || normalized === "yes";
}

function asArray(value) {
  return Array.isArray(value) ? value : [];
}

function asPositiveIntegerString(value) {
  const text = asString(value);
  if (!text) {
    return "";
  }
  if (!/^\d+$/.test(text)) {
    return "";
  }
  return Number(text) > 0 ? text : "";
}

function asPositiveInteger(value) {
  const text = asPositiveIntegerString(value);
  if (!text) {
    return 0;
  }
  return Number(text);
}

function asNumber(value, fallback = 0) {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  const text = asString(value);
  if (!text) {
    return fallback;
  }
  const parsed = Number(text);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function formatReleaseDate(value) {
  const text = asString(value);
  if (!text) {
    return "未知";
  }
  const parsed = dayjs(text);
  if (!parsed.isValid()) {
    return text;
  }
  return parsed.format("YYYY-MM-DD HH:mm");
}

function normalizeRouteMode(value, fallback = "local") {
  const text = asString(value).toLowerCase();
  if (SUPPORTED_ROUTE_MODES.has(text)) {
    return text;
  }
  return fallback;
}

function normalizeBaseURL(value) {
  const text = asString(value);
  if (!text) {
    return "";
  }
  try {
    const parsed = new URL(text);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return "";
    }
    parsed.protocol = parsed.protocol.toLowerCase();
    parsed.hostname = parsed.hostname.toLowerCase();
    const normalized = parsed.toString().replace(/\/+$/, "");
    return normalized || parsed.toString();
  } catch (_error) {
    return text;
  }
}

function buildModelAdapterIdentityKey(adapter) {
  return [
    asString(adapter.type).toLowerCase(),
    normalizeBaseURL(adapter.baseURL),
    asString(adapter.modelID).toLowerCase(),
    asString(adapter.apiKey),
    adapter.type === "openai" ? normalizeOpenAIEndpoint(adapter.openAIEndpoint) : "",
  ].join("\n");
}

function hashStringFNV32a(value) {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(16).padStart(8, "0");
}

export function buildModelAdapterTestRequestHash(source) {
  const adapter = normalizeModelAdapter(source);
  return hashStringFNV32a([
    asString(adapter.type),
    normalizeBaseURL(adapter.baseURL),
    asString(adapter.apiKey),
    asString(adapter.modelID),
    asString(adapter.protocolMode),
    asString(adapter.protocolGroup),
    adapter.type === "openai" || adapter.type === "gemini" ? asString(adapter.reasoningEffort || "medium") : "",
    adapter.type === "openai" ? normalizeOpenAIEndpoint(adapter.openAIEndpoint) : "",
    adapter.type === "openai" ? String(Boolean(adapter.openAIExtraParamsEnabled)) : "false",
    adapter.type === "openai" && adapter.openAIExtraParamsEnabled ? asString(adapter.openAIExtraParamsJSON) : "",
    String(Boolean(adapter.customHeadersEnabled)),
    adapter.customHeadersEnabled ? asString(adapter.customHeadersJSON) : "",
    adapter.type === "anthropic" ? String(Boolean(adapter.anthropicExtraParamsEnabled)) : "false",
    adapter.type === "anthropic" && adapter.anthropicExtraParamsEnabled ? asString(adapter.anthropicExtraParamsJSON) : "",
    String(asPositiveInteger(adapter.contextWindowTokens)),
    String(asPositiveInteger(adapter.maxCompletionTokens)),
    String(asPositiveInteger(adapter.anthropicMaxTokens)),
    adapter.type === "anthropic" ? asString(adapter.anthropicThinkingEffort || ANTHROPIC_THINKING_EFFORT_DEFAULT) : "",
  ].join("\n"));
}

export function formatDuration(value) {
  const durationMS = Math.max(0, Math.round(asNumber(value)));
  if (durationMS < 1000) {
    return `${durationMS} ms`;
  }
  return `${(durationMS / 1000).toFixed(1)} s`;
}

function normalizeModelAdapterTestStatus(value) {
  const text = asString(value).toLowerCase();
  return SUPPORTED_MODEL_ADAPTER_TEST_STATUSES.has(text) ? text : "idle";
}

export function formatModelAdapterTestSummary(source) {
  const result = source && typeof source === "object" ? source : {};
  const status = normalizeModelAdapterTestStatus(result.status);
  if (status === "running") {
    return "测试中...";
  }
  if (status === "error") {
    return asString(result.error) || "模型测试失败";
  }
  if (status !== "success") {
    return "";
  }
  const roundedTPS = Math.max(0, Math.round(asNumber(result.tokensPerSecond)));
  return `${roundedTPS} t/s | 首字 ${formatDuration(result.firstTextTokenMS)}`;
}

function normalizeModelAdapterTestResult(source) {
  const raw = source && typeof source === "object" ? source : {};
  const status = normalizeModelAdapterTestStatus(raw.status);
  const normalized = {
    adapterID: asString(raw.adapterID),
    requestHash: asString(raw.requestHash),
    status,
    tokensPerSecond: Math.max(0, asNumber(raw.tokensPerSecond)),
    firstTextTokenMS: Math.max(0, Math.round(asNumber(raw.firstTextTokenMS))),
    totalDurationMS: Math.max(0, Math.round(asNumber(raw.totalDurationMS))),
    outputTokens: Math.max(0, Math.round(asNumber(raw.outputTokens))),
    tokensEstimated: asBoolean(raw.tokensEstimated),
    summaryText: asString(raw.summaryText),
    error: asString(raw.error),
    rawResponse: asString(raw.rawResponse),
    testedAt: asString(raw.testedAt),
  };
  if (!normalized.summaryText) {
    normalized.summaryText = formatModelAdapterTestSummary(normalized);
  }
  if (status === "error" && !normalized.summaryText) {
    normalized.summaryText = normalized.error || "模型测试失败";
  }
  return normalized;
}

function normalizeModelAdapterTestResults(source) {
  const raw = source && typeof source === "object" && !Array.isArray(source)
    ? source.results
    : source;
  return asArray(raw)
    .map((item) => normalizeModelAdapterTestResult(item))
    .filter((item) => item.adapterID);
}

export function createEmptyModelAdapter() {
  return {
    id: "",
    displayName: "",
    groupName: "",
    type: "openai",
    supplierID: "custom",
    protocolMode: PROTOCOL_MODE_AUTO,
    protocolGroup: OPENAI_REQUEST_GROUP_RESPONSES,
    baseURL: "",
    apiKey: "",
    tooltipData: "备注",
    modelID: "",
    reasoningEffort: "medium",
    openAIEndpoint: OPENAI_ENDPOINT_RESPONSES,
    openAIRequestGroup: OPENAI_REQUEST_GROUP_RESPONSES,
    openAIExtraParamsEnabled: false,
    openAIExtraParamsJSON: OPENAI_EXTRA_PARAMS_DEFAULT_JSON,
    customHeadersEnabled: false,
    customHeadersJSON: CUSTOM_HEADERS_DEFAULT_JSON,
    anthropicExtraParamsEnabled: false,
    anthropicExtraParamsJSON: EXTRA_PARAMS_DEFAULT_JSON,
    contextWindowTokens: 0,
    maxCompletionTokens: 0,
    anthropicMaxTokens: 0,
    anthropicThinkingEffort: ANTHROPIC_THINKING_EFFORT_DEFAULT,
    thinkingBudgetTokens: 0,
    pricing: null,
    fastMode: false,
    openAIServiceTier: "",
    balanceQueryURL: "",
    balanceQueryField: "",
    balanceQueryHeaders: {},
    balanceQueryHeadersJSON: BALANCE_QUERY_HEADERS_DEFAULT_JSON,
    balanceProfile: "general",
    balanceAccessToken: "",
    balanceUserID: "",
    balanceCodingPlanProvider: "",
  };
}

// normalizeOpenAIEndpoint 归一化 endpoint 路径。
// 支持三个预设值：/v1/responses、/v1/chat/completions、/custom（自定义路径）。
// 选 /custom 时，用户需在接口地址栏填写完整请求 URL。
function normalizeOpenAIEndpoint(value) {
  const text = asString(value).toLowerCase();
  if (!text) {
    return OPENAI_ENDPOINT_RESPONSES;
  }
  return SUPPORTED_OPENAI_ENDPOINTS.has(text) ? text : "";
}

function isValidOpenAIEndpoint(value) {
  return normalizeOpenAIEndpoint(value) !== "";
}

// normalizeOpenAIRequestGroup 归一化 OpenAI 请求分组/协议形态。
// 与后端 modelchannel.NormalizeOpenAIRequestGroup 行为一致：
// - 非 openai 类型返回空串；
// - group 为空时按 endpoint 推导默认值（responses 端点 → responses，其余 → chat_completions）；
// - group 命中三选一返回原值；其余返回空串（视为非法）。
function normalizeOpenAIRequestGroup(type, endpoint, group) {
  if (asString(type).toLowerCase() !== "openai") {
    return "";
  }
  const normalized = asString(group).trim();
  if (!normalized) {
    return normalizeOpenAIEndpoint(endpoint) === OPENAI_ENDPOINT_RESPONSES
      ? OPENAI_REQUEST_GROUP_RESPONSES
      : OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  }
  return SUPPORTED_OPENAI_REQUEST_GROUPS.has(normalized) ? normalized : "";
}

function isValidOpenAIRequestGroup(type, endpoint, group) {
  return normalizeOpenAIRequestGroup(type, endpoint, group) !== "";
}

export function normalizeProtocolMode(value) {
  const normalized = asString(value).toLowerCase();
  if (!normalized) return PROTOCOL_MODE_AUTO;
  return SUPPORTED_PROTOCOL_MODES.has(normalized) ? normalized : "";
}

// inferProviderType 根据模型名推断最合适的 provider 协议族，避免把本应走原生协议的模型
// （claude、gemini）错误套用渠道级 openai 协议（导致 claude 缓存失效）。
// 规则：claude-* → anthropic、gemini-* → gemini、其余 → fallback（通常为渠道当前 type）。
// 与后端 modelchannel.InferProviderType 镜像，务必保持一致。
export function inferProviderType(modelID, fallback = "openai") {
  const model = asString(modelID).toLowerCase().trim();
  if (model.startsWith("claude")) return "anthropic";
  if (model.startsWith("gemini")) return "gemini";
  const fb = asString(fallback).toLowerCase().trim();
  if (fb === "anthropic" || fb === "gemini" || fb === "openai") return fb;
  return "openai";
}

export function classifyModelProtocol(type, modelID, baseURL, endpoint, configuredGroup = "") {
  const provider = asString(type).toLowerCase();
  if (provider === "anthropic") return PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  if (provider === "gemini") return PROTOCOL_GROUP_GEMINI_NATIVE;
  if (provider !== "openai") return "";
  const configured = asString(configuredGroup).toLowerCase();
  if (SUPPORTED_OPENAI_REQUEST_GROUPS.has(configured)) return configured;
  const normalizedEndpoint = normalizeOpenAIEndpoint(endpoint);
  const normalizedBaseURL = asString(baseURL).toLowerCase();
  const model = asString(modelID).toLowerCase();
  if (normalizedBaseURL.endsWith("/responses")) {
    return OPENAI_REQUEST_GROUP_RESPONSES;
  }
  if (normalizedBaseURL.endsWith("/chat/completions")) {
    return OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  }
  if (normalizedBaseURL.includes("api.openai.com") || /^(gpt-|o1|o3|o4)/.test(model)) {
    return OPENAI_REQUEST_GROUP_RESPONSES;
  }
  if (model) return OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  return normalizedEndpoint === OPENAI_ENDPOINT_RESPONSES
    ? OPENAI_REQUEST_GROUP_RESPONSES
    : OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
}

function normalizeProtocolGroup(mode, type, modelID, baseURL, endpoint, configuredGroup) {
  const provider = asString(type).toLowerCase();
  const normalizedMode = normalizeProtocolMode(mode);
  const configured = asString(configuredGroup).toLowerCase();
  if (!normalizedMode) return "";
  if (provider === "anthropic") return configured && configured !== PROTOCOL_GROUP_ANTHROPIC_MESSAGES
    ? ""
    : PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  if (provider === "gemini") return configured && configured !== PROTOCOL_GROUP_GEMINI_NATIVE
    ? ""
    : PROTOCOL_GROUP_GEMINI_NATIVE;
  if (provider !== "openai") return "";
  if (normalizedMode === PROTOCOL_MODE_FIXED) {
    return SUPPORTED_OPENAI_REQUEST_GROUPS.has(configured) ? configured : "";
  }
  return classifyModelProtocol(provider, modelID, baseURL, endpoint, configured);
}

function validateJSONObject(value, label) {
  const text = asString(value);
  if (!text) {
    return `${label}不能为空`;
  }
  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return `${label}必须是 JSON 对象`;
    }
  } catch (_error) {
    return `${label}必须是合法 JSON 对象`;
  }
  return "";
}

function validateHeadersJSON(value) {
  const objectError = validateJSONObject(value, "自定义请求头 JSON");
  if (objectError) {
    return objectError;
  }
  const parsed = JSON.parse(asString(value));
  for (const [key, item] of Object.entries(parsed)) {
    if (!asString(key)) {
      return "自定义请求头名称不能为空";
    }
    if (typeof item !== "string") {
      return `自定义请求头 ${key} 的值必须是字符串`;
    }
  }
  return "";
}

// parseBalanceQueryHeaders 把 map 或 JSON 字符串收成 map[string]string（对齐后端 BalanceQueryHeaders）。
function parseBalanceQueryHeaders(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      const name = asString(key).trim();
      if (!name) continue;
      out[name] = typeof item === "string" ? item : String(item ?? "");
    }
    return out;
  }
  const text = asString(value).trim();
  if (!text) {
    return {};
  }
  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    const out = {};
    for (const [key, item] of Object.entries(parsed)) {
      const name = asString(key).trim();
      if (!name) continue;
      out[name] = typeof item === "string" ? item : String(item ?? "");
    }
    return out;
  } catch (_error) {
    return {};
  }
}

function balanceQueryHeadersToJSON(headers) {
  if (!headers || typeof headers !== "object" || Array.isArray(headers) || Object.keys(headers).length === 0) {
    return BALANCE_QUERY_HEADERS_DEFAULT_JSON;
  }
  try {
    return JSON.stringify(headers, null, 2);
  } catch (_error) {
    return BALANCE_QUERY_HEADERS_DEFAULT_JSON;
  }
}

function validateBalanceQueryHeadersJSON(value) {
  const text = asString(value).trim();
  if (!text) {
    return "";
  }
  const objectError = validateJSONObject(text, "余额查询请求头 JSON");
  if (objectError) {
    return objectError;
  }
  const parsed = JSON.parse(text);
  for (const [key, item] of Object.entries(parsed)) {
    if (!asString(key)) {
      return "余额查询请求头名称不能为空";
    }
    if (typeof item !== "string") {
      return `余额查询请求头 ${key} 的值必须是字符串`;
    }
  }
  return "";
}

function hasBalanceQueryHeadersJSON(value) {
  return asString(value).trim() !== "";
}

function validateOpenAIExtraParamsJSON(value) {
  return validateJSONObject(value, "额外参数 JSON");
}

function validateAnthropicExtraParamsJSON(value) {
  return validateJSONObject(value, "Anthropic 额外参数 JSON");
}

function normalizePricing(value) {
  if (!value || typeof value !== "object") return null;
  const number = (input) => {
    const parsed = Number(input);
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
  };
  const pricing = {
    input: number(value.input ?? value.inputPrice ?? value.input_price),
    output: number(value.output ?? value.outputPrice ?? value.output_price),
    cacheRead: number(value.cacheRead ?? value.cache_read ?? value.cache_read_price),
    cacheWrite: number(value.cacheWrite ?? value.cache_write ?? value.cache_write_price),
    currency: asString(value.currency) || "USD",
    known: Boolean(value.known),
    source: asString(value.source),
  };
  pricing.known = pricing.known || [pricing.input, pricing.output, pricing.cacheRead, pricing.cacheWrite].some((item) => item != null);
  return pricing.known ? pricing : null;
}

export function normalizeModelAdapter(source) {
  const raw = source && typeof source === "object" ? source : {};
  const normalizedType = asString(raw.type).toLowerCase();
  const normalizedReasoningEffort = asString(raw.reasoningEffort || raw.reasoning_effort).toLowerCase();
  const normalizedAnthropicThinkingEffort = asString(
    raw.anthropicThinkingEffort
      ?? raw.anthropic_thinking_effort
      ?? raw.outputConfigEffort
      ?? raw.output_config_effort,
  ).toLowerCase();
  const normalizedOpenAIEndpoint = normalizeOpenAIEndpoint(
    raw.openAIEndpoint ?? raw.openaiEndpoint ?? raw.open_ai_endpoint ?? raw.endpoint,
  );
  const normalizedOpenAIRequestGroup = normalizeOpenAIRequestGroup(
    normalizedType,
    normalizedOpenAIEndpoint,
    raw.openAIRequestGroup ?? raw.openaiRequestGroup ?? raw.open_ai_request_group,
  );
  const protocolMode = normalizeProtocolMode(raw.protocolMode ?? raw.protocol_mode);
  const protocolGroup = normalizeProtocolGroup(
    protocolMode,
    normalizedType,
    raw.modelID,
    raw.baseURL || raw.url,
    normalizedOpenAIEndpoint,
    raw.protocolGroup ?? raw.protocol_group ?? normalizedOpenAIRequestGroup,
  );
  const openAIExtraParamsEnabled = normalizedType === "openai"
    ? asBoolean(raw.openAIExtraParamsEnabled ?? raw.openaiExtraParamsEnabled ?? raw.open_ai_extra_params_enabled)
    : false;
  const openAIExtraParamsJSON = normalizedType === "openai"
    ? asString(raw.openAIExtraParamsJSON ?? raw.openaiExtraParamsJSON ?? raw.open_ai_extra_params_json) || OPENAI_EXTRA_PARAMS_DEFAULT_JSON
    : "";
  const customHeadersEnabled = asBoolean(raw.customHeadersEnabled ?? raw.custom_headers_enabled);
  const customHeadersJSON = asString(raw.customHeadersJSON ?? raw.custom_headers_json) || CUSTOM_HEADERS_DEFAULT_JSON;
  const anthropicExtraParamsEnabled = normalizedType === "anthropic"
    ? asBoolean(raw.anthropicExtraParamsEnabled ?? raw.anthropic_extra_params_enabled)
    : false;
  const anthropicExtraParamsJSON = normalizedType === "anthropic"
    ? asString(raw.anthropicExtraParamsJSON ?? raw.anthropic_extra_params_json) || EXTRA_PARAMS_DEFAULT_JSON
    : "";
  const balanceQueryURL = asString(raw.balanceQueryURL ?? raw.balance_query_url).trim();
  const balanceQueryField = asString(raw.balanceQueryField ?? raw.balance_query_field).trim();
  const balanceQueryHeadersJSONRaw = asString(
    raw.balanceQueryHeadersJSON ?? raw.balance_query_headers_json,
  );
  // 编辑态优先保留用户输入的 JSON 字符串；落盘/后端只有 map 时再反序列化成字符串。
  const balanceQueryHeaders = balanceQueryHeadersJSONRaw.trim()
    ? parseBalanceQueryHeaders(balanceQueryHeadersJSONRaw)
    : parseBalanceQueryHeaders(raw.balanceQueryHeaders ?? raw.balance_query_headers);
  const balanceQueryHeadersJSON = balanceQueryHeadersJSONRaw.trim()
    ? balanceQueryHeadersJSONRaw
    : balanceQueryHeadersToJSON(balanceQueryHeaders);
  const balanceProfileRaw = asString(raw.balanceProfile ?? raw.balance_profile).trim().toLowerCase();
  const balanceProfile = ["auto", "general", "newapi", "token_plan", "custom", "official"].includes(balanceProfileRaw)
    ? balanceProfileRaw
    : "auto";
  const balanceAccessToken = asString(raw.balanceAccessToken ?? raw.balance_access_token).trim();
  const balanceUserID = asString(raw.balanceUserID ?? raw.balance_user_id).trim();
  const balanceCodingPlanProvider = asString(
    raw.balanceCodingPlanProvider ?? raw.balance_coding_plan_provider,
  ).trim().toLowerCase();
  return {
    id: asString(raw.id),
    displayName: asString(raw.displayName || raw.name),
    groupName: asString(raw.groupName || raw.group_name),
    type: SUPPORTED_MODEL_ADAPTER_TYPES.has(normalizedType) ? normalizedType : "",
    supplierID: asString(raw.supplierID ?? raw.supplierId ?? raw.vendor).trim() || "custom",
    protocolMode,
    protocolGroup,
    baseURL: normalizeBaseURL(raw.baseURL || raw.url),
    apiKey: asString(raw.apiKey || raw.key),
    tooltipData: asString(raw.tooltipData),
    modelID: asString(raw.modelID),
    reasoningEffort: SUPPORTED_REASONING_EFFORTS.has(normalizedReasoningEffort)
      ? normalizedReasoningEffort
      : "medium",
    openAIEndpoint: normalizedType === "openai" ? normalizedOpenAIEndpoint : "",
    openAIRequestGroup: normalizedType === "openai" ? protocolGroup : "",
    openAIExtraParamsEnabled,
    openAIExtraParamsJSON,
    customHeadersEnabled,
    customHeadersJSON,
    anthropicExtraParamsEnabled,
    anthropicExtraParamsJSON,
    contextWindowTokens: contextWindowTokensForModel(
      raw.modelID,
      raw.contextWindowTokens ?? raw.context_window_tokens ?? raw.maxInputTokens ?? raw.max_input_tokens,
    ),
    maxCompletionTokens: asPositiveInteger(
      raw.maxCompletionTokens ?? raw.max_completion_tokens ?? raw.max_tokens ?? raw.max_token,
    ),
    anthropicMaxTokens: asPositiveInteger(
      raw.anthropicMaxTokens ?? raw.anthropic_max_tokens ?? raw.max_tokens,
    ),
    anthropicThinkingEffort: normalizedType === "anthropic"
      ? (SUPPORTED_ANTHROPIC_THINKING_EFFORTS.has(normalizedAnthropicThinkingEffort)
        ? normalizedAnthropicThinkingEffort
        : ANTHROPIC_THINKING_EFFORT_DEFAULT)
      : "",
    thinkingBudgetTokens: asPositiveInteger(
      raw.thinkingBudgetTokens ?? raw.thinking_budget_tokens,
    ),
    pricing: normalizePricing(raw.pricing),
    fastMode: normalizedType === "openai" ? asBoolean(raw.fastMode ?? raw.fast_mode) : false,
    openAIServiceTier: normalizedType === "openai" ? asString(raw.openAIServiceTier ?? raw.openai_service_tier) : "",
    balanceQueryURL,
    balanceQueryField,
    balanceQueryHeaders,
    balanceQueryHeadersJSON,
    balanceProfile,
    balanceAccessToken,
    balanceUserID,
    balanceCodingPlanProvider,
  };
}

export function resolveBalanceProfileForAdapter(source) {
  const adapter = source && typeof source === "object" ? source : {};
  const raw = asString(adapter.balanceProfile).toLowerCase();
  if (["general", "newapi", "token_plan", "custom", "official"].includes(raw)) {
    return raw;
  }
  if (asString(adapter.balanceAccessToken) && asString(adapter.balanceUserID)) {
    return "newapi";
  }
  const identity = `${asString(adapter.supplierID)} ${asString(adapter.baseURL)}`.toLowerCase();
  if (/deepseek|stepfun|siliconflow|openrouter|novita|moonshot/.test(identity)) {
    return "official";
  }
  if (/api\.kimi\.com\/coding|bigmodel\.cn|api\.z\.ai|minimaxi|minimax\.io|zenmux|volces\.com\/api\/coding/.test(identity)) {
    return "token_plan";
  }
  if (asString(adapter.balanceQueryURL) && asString(adapter.balanceQueryField)) {
    return "custom";
  }
  return "general";
}

export function normalizeModelAdapters(source) {
  return asArray(source).map((item) => normalizeModelAdapter(item));
}

function mergeDuplicateModelAdapter(existing, incoming) {
  const existingHasBalanceHeaders = Object.keys(existing.balanceQueryHeaders || {}).length > 0;
  const incomingBalanceHeadersJSON = incoming.balanceQueryHeadersJSON || balanceQueryHeadersToJSON(incoming.balanceQueryHeaders);
  return {
    ...existing,
    displayName: existing.displayName || incoming.displayName,
    supplierID: existing.supplierID || incoming.supplierID,
    groupName: existing.groupName || incoming.groupName,
    tooltipData: existing.tooltipData || incoming.tooltipData,
    contextWindowTokens: existing.contextWindowTokens > 0
      ? existing.contextWindowTokens
      : incoming.contextWindowTokens,
    pricing: existing.pricing || incoming.pricing,
    balanceQueryURL: existing.balanceQueryURL || incoming.balanceQueryURL,
    balanceQueryField: existing.balanceQueryField || incoming.balanceQueryField,
    balanceQueryHeaders: existingHasBalanceHeaders ? existing.balanceQueryHeaders : incoming.balanceQueryHeaders,
    balanceQueryHeadersJSON: existingHasBalanceHeaders ? existing.balanceQueryHeadersJSON : incomingBalanceHeadersJSON,
    balanceProfile: existing.balanceProfile && existing.balanceProfile !== "auto"
      ? existing.balanceProfile
      : (incoming.balanceProfile || existing.balanceProfile || "auto"),
    balanceAccessToken: existing.balanceAccessToken || incoming.balanceAccessToken,
    balanceUserID: existing.balanceUserID || incoming.balanceUserID,
    balanceCodingPlanProvider: existing.balanceCodingPlanProvider || incoming.balanceCodingPlanProvider,
  };
}

export function dedupeModelAdapters(source) {
  const result = [];
  const indexByIdentity = new Map();
  for (const adapter of normalizeModelAdapters(source)) {
    const identity = buildModelAdapterIdentityKey(adapter);
    const existingIndex = indexByIdentity.get(identity);
    if (existingIndex == null) {
      indexByIdentity.set(identity, result.length);
      result.push(adapter);
      continue;
    }
    result[existingIndex] = mergeDuplicateModelAdapter(result[existingIndex], adapter);
  }
  return result;
}

export function validateModelAdapters(source) {
  const adapters = dedupeModelAdapters(source);
  for (const [index, adapter] of adapters.entries()) {
    const prefix = `模型 ${index + 1}`;
    if (!adapter.displayName) {
      return `${prefix} 的显示名称不能为空`;
    }
    if (!SUPPORTED_MODEL_ADAPTER_TYPES.has(adapter.type)) {
      return `${prefix} 的类型仅支持 OpenAI、Anthropic 或 Gemini`;
    }
    if (!adapter.baseURL) {
      return `${prefix} 的接口地址不能为空`;
    }
    if (!adapter.apiKey) {
      return `${prefix} 的访问密钥不能为空`;
    }
    if (!adapter.tooltipData) {
      return `${prefix} 的悬停提示不能为空`;
    }
    if (!adapter.modelID) {
      return `${prefix} 的模型标识不能为空`;
    }
    if (!SUPPORTED_PROTOCOL_MODES.has(adapter.protocolMode)) {
      return `${prefix} 的协议模式仅支持 auto 或 fixed`;
    }
    if (!adapter.protocolGroup) {
      return `${prefix} 的协议分组与模型类型不匹配`;
    }
    if ((adapter.type === "openai" || adapter.type === "gemini") && !SUPPORTED_REASONING_EFFORTS.has(adapter.reasoningEffort)) {
      return `${prefix} 的推理强度仅支持 low、medium、high、xhigh、max`;
    }
    if (adapter.type === "openai" && !isValidOpenAIEndpoint(adapter.openAIEndpoint)) {
      return `${prefix} 的 OpenAI 端点仅支持 /v1/responses、/v1/chat/completions 或以 / 开头的自定义路径`;
    }
    if (adapter.type === "openai" && !isValidOpenAIRequestGroup(adapter.type, adapter.openAIEndpoint, adapter.openAIRequestGroup)) {
      return `${prefix} 的请求分组仅支持 responses、chat_completions、chat_completions_compat`;
    }
    if (adapter.type === "openai" && adapter.openAIExtraParamsEnabled) {
      const extraParamsError = validateOpenAIExtraParamsJSON(adapter.openAIExtraParamsJSON);
      if (extraParamsError) {
        return `${prefix} 的 ${extraParamsError}`;
      }
    }
    if (adapter.customHeadersEnabled) {
      const customHeadersError = validateHeadersJSON(adapter.customHeadersJSON);
      if (customHeadersError) {
        return `${prefix} 的 ${customHeadersError}`;
      }
    }
    if (hasBalanceQueryHeadersJSON(adapter.balanceQueryHeadersJSON)) {
      const balanceHeadersError = validateBalanceQueryHeadersJSON(adapter.balanceQueryHeadersJSON);
      if (balanceHeadersError) {
        return `${prefix} 的 ${balanceHeadersError}`;
      }
    }
    if (adapter.type === "anthropic" && adapter.anthropicExtraParamsEnabled) {
      const extraParamsError = validateAnthropicExtraParamsJSON(adapter.anthropicExtraParamsJSON);
      if (extraParamsError) {
        return `${prefix} 的 ${extraParamsError}`;
      }
    }
    if (adapter.type === "anthropic" && !SUPPORTED_ANTHROPIC_THINKING_EFFORTS.has(adapter.anthropicThinkingEffort)) {
      return `${prefix} 的 Anthropic 思考强度仅支持 low、medium、high、xhigh、max`;
    }
    if (adapter.contextWindowTokens && (!Number.isInteger(adapter.contextWindowTokens) || adapter.contextWindowTokens <= 0)) {
      return `${prefix} 的上下文窗口必须为正整数`;
    }
    if (adapter.maxCompletionTokens && (!Number.isInteger(adapter.maxCompletionTokens) || adapter.maxCompletionTokens <= 0)) {
      return `${prefix} 的最大输出 Token 必须为正整数`;
    }
    if (adapter.anthropicMaxTokens && (!Number.isInteger(adapter.anthropicMaxTokens) || adapter.anthropicMaxTokens <= 0)) {
      return `${prefix} 的最大输出 Token 必须为正整数`;
    }
    if (adapter.thinkingBudgetTokens && (!Number.isInteger(adapter.thinkingBudgetTokens) || adapter.thinkingBudgetTokens <= 0)) {
      return `${prefix} 的思考预算 Token 必须为正整数`;
    }
  }
  return "";
}

function validateConfigPayload(payload) {
  if (!SUPPORTED_ROUTE_MODES.has(normalizeRouteMode(payload?.routing?.mode, ""))) {
    return "运行模式仅支持 local 或 upstream";
  }
  return "";
}

function canUseLocalStorage() {
  return typeof window !== "undefined" && typeof window.localStorage !== "undefined";
}

function delay(ms) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, Math.max(0, ms));
  });
}

function createEmptyHomeMetrics() {
  return {
    turnsTotal: 0,
    validTurnsTotal: 0,
    invalidTurnsTotal: 0,
    requestTokensTotal: 0,
    promptTokensTotal: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    cacheHitRate: null,
  };
}

function loadCachedState() {
  if (!canUseLocalStorage()) {
    return {};
  }

  try {
    const raw = window.localStorage.getItem(APP_STATE_STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    return parsed;
  } catch (_error) {
    return {};
  }
}

function normalizeLocalResponseCache(source) {
  const raw = source && typeof source === "object" ? source : {};
  return {
    enabled: asBoolean(raw.enabled),
    ttlSeconds: asPositiveInteger(raw.ttlSeconds),
    maxEntries: asPositiveInteger(raw.maxEntries),
  };
}

function normalizeDelegation(source) {
  const raw = source && typeof source === "object" ? source : {};
  const groups = asArray(raw.groups).map((item, index) => {
    const group = item && typeof item === "object" ? item : {};
    const modelIDs = [...new Set(asArray(group.modelIDs || group.modelIds).map((value) => asString(value)).filter(Boolean))];
    const defaultModelID = asString(group.defaultModelID || group.defaultModelId);
    return {
      id: asString(group.id) || `delegation-group-${index + 1}`,
      name: asString(group.name) || `委派模型组 ${index + 1}`,
      enabled: asBoolean(group.enabled, true),
      modelIDs,
      defaultModelID: modelIDs.includes(defaultModelID) ? defaultModelID : (modelIDs[0] || ""),
      executionMode: ["auto", "cursor", "local"].includes(asString(group.executionMode).toLowerCase())
        ? asString(group.executionMode).toLowerCase()
        : "auto",
      toolPermissions: group.toolPermissions && typeof group.toolPermissions === "object" ? { ...group.toolPermissions } : {},
    };
  });
  const maxConcurrency = asPositiveInteger(raw.maxConcurrency);
  return {
    enabled: asBoolean(raw.enabled, true),
    maxConcurrency: maxConcurrency > 0 ? maxConcurrency : 4,
    groups,
  };
}

function normalizeDelegationForAdapters(source, adapters) {
  const delegation = normalizeDelegation(source);
  const availableModelIDs = new Set(
    asArray(adapters).map((adapter) => asString(adapter?.id)).filter(Boolean),
  );
  return {
    ...delegation,
    groups: delegation.groups.map((group) => {
      const modelIDs = group.modelIDs.filter((modelID) => availableModelIDs.has(modelID));
      const defaultModelID = modelIDs.includes(group.defaultModelID)
        ? group.defaultModelID
        : (modelIDs[0] || "");
      return {
        ...group,
        enabled: modelIDs.length > 0 && group.enabled,
        modelIDs,
        defaultModelID,
      };
    }),
  };
}

function normalizeConfig(source) {
  const raw = source && typeof source === "object" ? source : {};
  const routing = raw.routing && typeof raw.routing === "object" ? raw.routing : {};
  const homeMetrics = raw.homeMetrics && typeof raw.homeMetrics === "object" ? raw.homeMetrics : {};
  return {
    log: asBoolean(raw.log),
    providerStreamIdleTimeout: asPositiveInteger(raw.providerStreamIdleTimeout),
    turnStaleTimeout: asPositiveInteger(raw.turnStaleTimeout),
    autoMatchContextWindow: asBoolean(raw.autoMatchContextWindow),
    backendListenAddr: asString(raw.configBackendListenAddr) || asString(raw.backendListenAddr),
    proxyListenAddr: asString(raw.configProxyListenAddr) || asString(raw.proxyListenAddr),
    modelAdapters: dedupeModelAdapters(raw.modelAdapters),
    routing: {
      mode: normalizeRouteMode(routing.mode),
    },
    homeMetrics: {
      includeCacheWriteInHitRate: asBoolean(homeMetrics.includeCacheWriteInHitRate),
    },
    // 本地响应缓存配置：保留在归一化白名单中，避免任何一次配置保存把它清空回默认值
    localResponseCache: normalizeLocalResponseCache(raw.localResponseCache),
    delegation: normalizeDelegation(raw.delegation),
    lastAgentModelHash: asString(raw.lastAgentModelHash),
  };
}

function asNullableRate(value) {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  return null;
}

function normalizeHomeMetrics(source) {
  const raw = source && typeof source === "object" ? source : {};
  const turnsTotal = asPositiveInteger(raw.turnsTotal);
  const validTurnsTotal = asPositiveInteger(raw.validTurnsTotal);
  const invalidTurnsTotal = asPositiveInteger(raw.invalidTurnsTotal);
  return {
    turnsTotal,
    validTurnsTotal,
    invalidTurnsTotal,
    requestTokensTotal: asPositiveInteger(raw.requestTokensTotal),
    promptTokensTotal: asPositiveInteger(raw.promptTokensTotal),
    cacheReadTokens: asPositiveInteger(raw.cacheReadTokens),
    cacheWriteTokens: asPositiveInteger(raw.cacheWriteTokens),
    cacheHitRate: asNullableRate(raw.cacheHitRate),
  };
}

function applyHomeMetrics(raw) {
  appState.homeMetrics = normalizeHomeMetrics(raw);
  appState.homeMetricsError = "";
}

function buildConfigPayload(source = appState) {
  const normalized = normalizeConfig(source);
  const delegation = normalizeDelegationForAdapters(normalized.delegation, normalized.modelAdapters);
  return {
    log: normalized.log,
    providerStreamIdleTimeout: normalized.providerStreamIdleTimeout,
    turnStaleTimeout: normalized.turnStaleTimeout,
    autoMatchContextWindow: normalized.autoMatchContextWindow,
    backendListenAddr: normalized.backendListenAddr,
    proxyListenAddr: normalized.proxyListenAddr,
    // balanceQueryHeadersJSON 仅前端编辑态使用，落盘只保留 map 形态的 balanceQueryHeaders。
    modelAdapters: normalized.modelAdapters.map(({ balanceQueryHeadersJSON, ...adapter }) => adapter),
    routing: normalized.routing,
    homeMetrics: normalized.homeMetrics,
    localResponseCache: normalized.localResponseCache,
    delegation,
    lastAgentModelHash: normalized.lastAgentModelHash,
  };
}

function applyConfigToState(config, { modelAdaptersOnly = false } = {}) {
  const normalized = normalizeConfig(config);
  if (modelAdaptersOnly) {
    appState.modelAdapters = normalized.modelAdapters;
    return normalized;
  }
  appState.modelAdapters = normalized.modelAdapters;
  appState.configBackendListenAddr = normalized.backendListenAddr;
  appState.configProxyListenAddr = normalized.proxyListenAddr;
  appState.routingMode = normalized.routing.mode;
  appState.includeCacheWriteInHitRate = normalized.homeMetrics.includeCacheWriteInHitRate;
  appState.localResponseCache = normalized.localResponseCache;
  appState.delegation = normalized.delegation;
  appState.turnStaleTimeout = normalized.turnStaleTimeout;
  appState.autoMatchContextWindow = normalized.autoMatchContextWindow;
  return normalized;
}

async function loadPersistedUserConfig() {
  return normalizeConfig(await loadUserConfig());
}

async function persistConfigPayload(config, { modelAdaptersOnly = false } = {}) {
  const normalizedForValidation = normalizeConfig(config);
  const prePayloadValidationError = validateModelAdapters(normalizedForValidation.modelAdapters);
  if (prePayloadValidationError) {
    return { ok: false, error: prePayloadValidationError };
  }
  const payload = buildConfigPayload(normalizedForValidation);
  const configValidationError = validateConfigPayload(payload);
  if (configValidationError) {
    return {
      ok: false,
      error: configValidationError,
    };
  }
  const validationError = validateModelAdapters(payload.modelAdapters);
  if (validationError) {
    return {
      ok: false,
      error: validationError,
    };
  }

  appState.configSaving = true;
  try {
    await saveUserConfig(payload);
    const persisted = await loadPersistedUserConfig();
    applyConfigToState(persisted, { modelAdaptersOnly });
    return {
      ok: true,
      error: "",
    };
  } catch (error) {
    return {
      ok: false,
      error: toUserError(error),
    };
  } finally {
    appState.configSaving = false;
  }
}

function applyProxyState(raw) {
  const state = raw && typeof raw === "object" ? raw : {};
  appState.backendRunning = asBoolean(state.backendRunning);
  appState.proxyRunning = asBoolean(state.proxyRunning ?? state.running);
  appState.serviceRunning = appState.proxyRunning;
  appState.serviceLastError = asString(state.lastError);
  appState.backendListenAddr = asString(state.backendListenAddr);
  appState.proxyListenAddr = asString(state.proxyListenAddr || state.listenAddr);
  appState.serviceListenAddr = appState.proxyListenAddr;
  appState.cursorSettingsApplied = asBoolean(state.cursorSettingsApplied);
  appState.netProxySource = asString(state.netProxySource);
  appState.netProxyActive = asBoolean(state.netProxyActive);
  appState.netProxyUsingSystem = asBoolean(state.netProxyUsingSystem);
  appState.netProxyUsingEnv = asBoolean(state.netProxyUsingEnv);
  appState.netProxyHttp = asString(state.netProxyHttp);
  appState.netProxyHttps = asString(state.netProxyHttps);
  appState.netProxyPacIgnored = asBoolean(state.netProxyPacIgnored);
  appState.netProxyDescription = asString(state.netProxyDescription);
}

function handleProxyStateEvent(event) {
  if (event?.data && typeof event.data === "object") {
    applyProxyState(event.data);
    return;
  }
  void syncServiceState().catch(() => {});
}

function handleUserConfigChangedEvent(event) {
  if (event?.data && typeof event.data === "object") {
    applyConfigToState(event.data);
    return;
  }
  void reloadUserConfig().catch(() => {});
}

function applyModelAdapterTestResults(source) {
  const next = {};
  for (const result of normalizeModelAdapterTestResults(source)) {
    next[result.adapterID] = result;
  }
  appState.modelAdapterTestResults = next;
  return next;
}

function handleModelAdapterTestUpdatedEvent(event) {
  if (event?.data) {
    applyModelAdapterTestResults(event.data);
    return;
  }
  void refreshModelAdapterTestResults().catch(() => {});
}

function normalizeUpdateState(value) {
  const text = asString(value).toLowerCase();
  if (["idle", "checking", "downloading", "ready", "installing", "error"].includes(text)) {
    return text;
  }
  return "idle";
}

function applyUpdateSnapshot(raw) {
  const data = raw && typeof raw === "object" ? raw : {};
  const nextState = normalizeUpdateState(data.state ?? appState.updateState);
  appState.updateState = nextState;

  const version = asString(data.version);
  if (version) {
    appState.updateVersion = version;
  } else if (nextState === "idle") {
    appState.updateVersion = "";
  }

  const releaseDate = asString(data.releaseDate);
  if (releaseDate) {
    appState.updateReleaseDate = releaseDate;
  } else if (nextState === "idle") {
    appState.updateReleaseDate = "";
  }

  if (typeof data.releaseNotes === "string") {
    appState.updateReleaseNotes = data.releaseNotes.replace(/\r\n/g, "\n");
  } else if (nextState === "idle") {
    appState.updateReleaseNotes = "";
  }

  if (typeof data.error === "string") {
    appState.updateError = data.error.trim();
  } else if (nextState !== "error") {
    appState.updateError = "";
  }

  if (typeof data.message === "string") {
    appState.updateMessage = data.message.trim();
  } else if (!data.prompt) {
    appState.updateMessage = "";
  }

  if (typeof data.downloaded === "number") {
    appState.updateProgressDownloaded = data.downloaded;
  } else if (nextState !== "downloading") {
    appState.updateProgressDownloaded = 0;
  }

  if (typeof data.total === "number") {
    appState.updateProgressTotal = data.total;
  } else if (nextState !== "downloading") {
    appState.updateProgressTotal = 0;
  }

  if (typeof data.percentage === "number") {
    appState.updateProgressPercent = Math.max(0, Math.min(100, data.percentage));
  } else if (nextState !== "downloading") {
    appState.updateProgressPercent = 0;
  }
}

function openUpdatePrompt(kind, payload = {}) {
  appState.updatePromptKind = asString(kind) || "idle";
  appState.updatePromptVisible = true;
  appState.updatePromptBusy = false;
  if (typeof payload.message === "string") {
    appState.updateMessage = payload.message.trim();
  }
  if (typeof payload.error === "string") {
    appState.updateError = payload.error.trim();
  }
}

function handleUpdateStateEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot(data);
  if (asBoolean(data.prompt)) {
    openUpdatePrompt(asString(data.promptKind) || "idle", data);
  }
}

function handleUpdateProgressEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot({
    ...data,
    state: "downloading",
  });
}

function handleUpdateReadyEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot({
    ...data,
    state: "ready",
  });
  if (data.prompt !== false) {
    openUpdatePrompt("ready", data);
  }
}

function handleUpdateErrorEvent(event) {
  const data = event?.data && typeof event.data === "object" ? event.data : {};
  applyUpdateSnapshot({
    ...data,
    state: "error",
  });
  if (asBoolean(data.prompt)) {
    openUpdatePrompt("error", data);
  }
}

function extractErrorMessage(error) {
  if (typeof error === "string") {
    return error.trim();
  }
  if (error && typeof error === "object") {
    return asString(error.message) || asString(error.error);
  }
  return "";
}

const cachedState = loadCachedState();
const cachedConfig = normalizeConfig(cachedState);

export const appState = reactive({
  appVersion: "",
  modelAdapters: cachedConfig.modelAdapters,
  modelAdapterTestResults: {},
  configBackendListenAddr: cachedConfig.backendListenAddr,
  configProxyListenAddr: cachedConfig.proxyListenAddr,
  routingMode: cachedConfig.routing.mode,
  includeCacheWriteInHitRate: cachedConfig.homeMetrics.includeCacheWriteInHitRate,
  localResponseCache: cachedConfig.localResponseCache,
  delegation: cachedConfig.delegation,
  // 浮窗偏好是纯前端 UX 状态：localStorage 持久化 + 跨窗口 storage 事件广播。
  // 不进后端 config（后端 config 不含 overlay 字段）。初始给默认值，真实值由
  // getStatsOverlayPreferences() 在首次读取时从 localStorage 填充，避免模块求值顺序依赖。
  statsOverlayPreferences: { style: "card", alwaysOnTop: true, visible: false, snapCollapse: true, dockLocked: false, closeAction: "tray" },
  turnStaleTimeout: cachedConfig.turnStaleTimeout,
  autoMatchContextWindow: cachedConfig.autoMatchContextWindow,

  serviceRunning: asBoolean(cachedState.serviceRunning),
  backendRunning: asBoolean(cachedState.backendRunning),
  proxyRunning: asBoolean(cachedState.proxyRunning),
  serviceBusy: false,
  serviceLastError: asString(cachedState.serviceLastError),
  serviceListenAddr: asString(cachedState.serviceListenAddr),
  backendListenAddr: asString(cachedState.backendListenAddr),
  proxyListenAddr: asString(cachedState.proxyListenAddr),
  cursorSettingsApplied: asBoolean(cachedState.cursorSettingsApplied),
  netProxySource: asString(cachedState.netProxySource),
  netProxyActive: asBoolean(cachedState.netProxyActive),
  netProxyUsingSystem: asBoolean(cachedState.netProxyUsingSystem),
  netProxyUsingEnv: asBoolean(cachedState.netProxyUsingEnv),
  netProxyHttp: asString(cachedState.netProxyHttp),
  netProxyHttps: asString(cachedState.netProxyHttps),
  netProxyPacIgnored: asBoolean(cachedState.netProxyPacIgnored),
  netProxyDescription: asString(cachedState.netProxyDescription),

  configSaving: false,
  homeMetrics: createEmptyHomeMetrics(),
  homeMetricsLoading: false,
  homeMetricsError: "",

  updateState: "idle",
  updateVersion: "",
  updateReleaseDate: "",
  updateReleaseNotes: "",
  updateProgressDownloaded: 0,
  updateProgressTotal: 0,
  updateProgressPercent: 0,
  updateError: "",
  updateMessage: "",
  updatePromptVisible: false,
  updatePromptKind: "idle",
  updatePromptBusy: false,
});

watchSyncEffect(() => {
  if (!canUseLocalStorage()) {
    return;
  }
  try {
    window.localStorage.setItem(
      APP_STATE_STORAGE_KEY,
      JSON.stringify({
        ...buildConfigPayload(),
        serviceRunning: appState.serviceRunning,
        backendRunning: appState.backendRunning,
        proxyRunning: appState.proxyRunning,
        serviceLastError: appState.serviceLastError,
        serviceListenAddr: appState.serviceListenAddr,
        configBackendListenAddr: appState.configBackendListenAddr,
        configProxyListenAddr: appState.configProxyListenAddr,
        backendListenAddr: appState.backendListenAddr,
        proxyListenAddr: appState.proxyListenAddr,
        cursorSettingsApplied: appState.cursorSettingsApplied,
        netProxySource: appState.netProxySource,
        netProxyActive: appState.netProxyActive,
        netProxyUsingSystem: appState.netProxyUsingSystem,
        netProxyUsingEnv: appState.netProxyUsingEnv,
        netProxyHttp: appState.netProxyHttp,
        netProxyHttps: appState.netProxyHttps,
        netProxyPacIgnored: appState.netProxyPacIgnored,
        netProxyDescription: appState.netProxyDescription,
      }),
    );
  } catch (_error) {
    // ignore local persistence failures
  }
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(PROXY_STATE_EVENT, handleProxyStateEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(USER_CONFIG_CHANGED_EVENT, handleUserConfigChangedEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(MODEL_ADAPTER_TEST_UPDATED_EVENT, handleModelAdapterTestUpdatedEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_STATE_EVENT, handleUpdateStateEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_PROGRESS_EVENT, handleUpdateProgressEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_READY_EVENT, handleUpdateReadyEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

watchSyncEffect((onCleanup) => {
  if (typeof window === "undefined") {
    return;
  }
  const unsubscribe = runtimeEvents.On(UPDATE_ERROR_EVENT, handleUpdateErrorEvent);
  onCleanup(() => {
    unsubscribe();
  });
});

export const appViewState = reactive({
  serviceStatusText: computed(() => {
    if (appState.proxyRunning && appState.backendRunning) {
      return "服务运行中";
    }
    if (appState.backendRunning) {
      return "后端已启动，代理未启动";
    }
    return "服务未启动";
  }),
  serviceStatusClass: computed(() =>
    appState.serviceRunning ? "text-[#22c55e]" : "text-[#f59e0b]",
  ),
  serviceButtonText: computed(() => {
    if (appState.serviceBusy) {
      return appState.serviceRunning ? "关闭中..." : "启动中...";
    }
    return appState.serviceRunning ? "关闭服务" : "启动服务";
  }),
});

function localizeUpdateMessage(msg) {
  if (!msg) return "";
  if (msg.includes("当前已是最新版本")) {
    const match = msg.match(/v?([0-9]+\.[0-9]+\.[0-9]+)/);
    const version = match ? match[1] : appState.appVersion || "...";
    const locale = getLocale ? getLocale() : "zh-CN";
    if (locale === "en-US") {
      return `You are already on the latest version (v${version}).`;
    }
    if (locale === "ja-JP") {
      return `すでに最新バージョン（v${version}）です。`;
    }
    return `当前已是最新版本（v${version}）。`;
  }
  return msg;
}

function localizeReadyContent() {
  const locale = getLocale ? getLocale() : "zh-CN";
  const version = appState.updateVersion || appState.appVersion || "...";
  const date = formatReleaseDate(appState.updateReleaseDate);
  const notes = appState.updateReleaseNotes || "";

  if (locale === "en-US") {
    return [
      `Version: v${version}`,
      `Release Date: ${date}`,
      "",
      notes || "No release notes",
    ].join("\n");
  }
  if (locale === "ja-JP") {
    return [
      `バージョン: v${version}`,
      `リリース日: ${date}`,
      "",
      notes || "リリースノートはありません",
    ].join("\n");
  }
  return [
    `版本：v${version}`,
    `发布时间：${date}`,
    "",
    notes || "无更新说明",
  ].join("\n");
}

export const updateViewState = reactive({
  footerDownloading: computed(() => appState.updateState === "downloading"),
  footerBusy: computed(() => ["checking", "installing"].includes(appState.updateState)),
  footerVersionLabel: computed(() => `v${appState.appVersion || "..."}`),
  footerProgressText: computed(() => `${Math.round(appState.updateProgressPercent || 0)}%`),
  footerProgressStyle: computed(() => ({
    width: `${Math.max(0, Math.min(100, appState.updateProgressPercent || 0))}%`,
  })),
  promptTitle: computed(() => {
    const locale = getLocale ? getLocale() : "zh-CN";
    switch (appState.updatePromptKind) {
      case "ready":
        if (locale === "en-US") return "New Version Available";
        if (locale === "ja-JP") return "新しいバージョンがあります";
        return "发现新版本";
      case "error":
        if (locale === "en-US") return "Update Failed";
        if (locale === "ja-JP") return "アップデートに失敗しました";
        return "更新失败";
      default:
        if (locale === "en-US") return "Check for Updates";
        if (locale === "ja-JP") return "アップデートを確認";
        return "检查更新";
    }
  }),
  promptContent: computed(() => {
    switch (appState.updatePromptKind) {
      case "ready":
        return localizeReadyContent();
      case "error":
        return appState.updateError || localizeUpdateMessage(appState.updateMessage) || GENERIC_SERVICE_ERROR;
      default:
        return localizeUpdateMessage(appState.updateMessage) || localizeUpdateMessage(`当前已是最新版本（v${appState.appVersion || "..."}）。`);
    }
  }),
  promptConfirmText: computed(() => {
    const locale = getLocale ? getLocale() : "zh-CN";
    if (appState.updatePromptKind === "ready") {
      if (locale === "en-US") return "Restart to Update";
      if (locale === "ja-JP") return "再起動して更新";
      return "立即重启更新";
    }
    if (locale === "en-US") return "OK";
    if (locale === "ja-JP") return "OK";
    return "确定";
  }),
  promptCancelText: computed(() => {
    const locale = getLocale ? getLocale() : "zh-CN";
    if (appState.updatePromptKind === "ready") {
      if (locale === "en-US") return "Later";
      if (locale === "ja-JP") return "後で";
      return "稍后";
    }
    if (locale === "en-US") return "Cancel";
    if (locale === "ja-JP") return "キャンセル";
    return "取消";
  }),
  promptShowCancel: computed(() => appState.updatePromptKind === "ready"),
});

export function getModelAdapterTestResultByID(adapterID) {
  const id = asString(adapterID);
  if (!id) {
    return null;
  }
  return appState.modelAdapterTestResults[id] ?? null;
}

export function getModelAdapterTestResult(adapter) {
  const normalized = normalizeModelAdapter(adapter);
  if (normalized.id && appState.modelAdapterTestResults[normalized.id]) {
    return appState.modelAdapterTestResults[normalized.id];
  }
  const requestHash = buildModelAdapterTestRequestHash(normalized);
  return Object.values(appState.modelAdapterTestResults).find((result) => result.requestHash === requestHash) ?? null;
}

export function isModelAdapterTestResultRunning(adapter) {
  return getModelAdapterTestResult(adapter)?.status === "running";
}

export function isModelAdapterTestResultStale(adapter, result) {
  if (!result || !result.requestHash) {
    return false;
  }
  return result.requestHash !== buildModelAdapterTestRequestHash(adapter);
}

export async function refreshModelAdapterTestResults() {
  const results = await getModelAdapterTestResults();
  applyModelAdapterTestResults(results);
  return Object.values(appState.modelAdapterTestResults);
}

export function startModelAdapterTest(adapter) {
  const normalized = normalizeModelAdapter(adapter);
  return testModelAdapter(normalized).then((rawResult) => {
    const result = normalizeModelAdapterTestResult(rawResult);
    if (result.adapterID) {
      const existing = appState.modelAdapterTestResults[result.adapterID];
      // "running" 状态由后端事件负责推送；Promise 回调只合并终态结果，
      // 避免去重路径返回的陈旧 "running" 覆盖事件已送达的 success/error。
      if (result.status !== "running" || !existing || existing.status === "running") {
        appState.modelAdapterTestResults = {
          ...appState.modelAdapterTestResults,
          [result.adapterID]: result,
        };
      }
    }
    return result;
  });
}

export async function runModelAdapterTest(adapter) {
  return startModelAdapterTest(adapter);
}

export async function persistUserConfig() {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    modelAdapters: normalizeModelAdapters(appState.modelAdapters),
    routing: {
      mode: appState.routingMode,
    },
    homeMetrics: {
      ...currentConfig.homeMetrics,
      includeCacheWriteInHitRate: appState.includeCacheWriteInHitRate,
    },
    delegation: appState.delegation,
  });
}

export async function saveIncludeCacheWriteInHitRate(value) {
  const currentConfig = await loadPersistedUserConfig();
  const previousValue = appState.includeCacheWriteInHitRate;
  const nextValue = asBoolean(value);
  appState.includeCacheWriteInHitRate = nextValue;
  const result = await persistConfigPayload({
    ...currentConfig,
    homeMetrics: {
      ...currentConfig.homeMetrics,
      includeCacheWriteInHitRate: nextValue,
    },
  });
  if (!result.ok) {
    appState.includeCacheWriteInHitRate = previousValue;
  }
  return result;
}

export async function saveLocalResponseCacheEnabled(enabled) {
  const currentConfig = await loadPersistedUserConfig();
  const nextValue = asBoolean(enabled);
  const previous = appState.localResponseCache;
  appState.localResponseCache = { ...previous, enabled: nextValue };
  const result = await persistConfigPayload({
    ...currentConfig,
    localResponseCache: {
      ...currentConfig.localResponseCache,
      enabled: nextValue,
    },
  });
  if (!result.ok) {
    appState.localResponseCache = previous;
  }
  return result;
}

// ─── 浮窗偏好（stats overlay）──────────────────────────────────────────────
// 偏好对象：{ style: "card"|"engine"|"orb", alwaysOnTop: boolean, visible: boolean }
// 仅前端 localStorage 持久化；浮窗是独立 webview 窗口，靠 storage 事件 + 自定义事件
// 跨窗口同步。后端 WindowService 已提供 open/update/close 三个 binding（window.go）。

const STATS_OVERLAY_PREFERENCES_KEY = "cursor-byok.stats-overlay.preferences";
const STATS_OVERLAY_CHANGED_EVENT = "stats-overlay-preferences-changed";
const STATS_OVERLAY_SHOW_REQUESTED_EVENT = "stats-overlay-show-requested";
const STATS_OVERLAY_STYLES = new Set(["card", "engine", "orb"]);
let statsOverlayPreferenceSyncBound = false;
let statsOverlayShowRequestBound = false;

function normalizeStatsOverlayPreferences(input) {
  const raw = input && typeof input === "object" ? input : {};
  const style = STATS_OVERLAY_STYLES.has(raw.style) ? raw.style : "card";
  const closeAction = raw.closeAction === "quit" ? "quit" : "tray";
  const normalizeCoordinate = (value) => typeof value === "number" && Number.isFinite(value) ? Math.round(value) : null;
  return {
    style,
    alwaysOnTop: asBoolean(raw.alwaysOnTop ?? true),
    visible: asBoolean(raw.visible ?? false),
    x: normalizeCoordinate(raw.x),
    y: normalizeCoordinate(raw.y),
    snapCollapse: asBoolean(raw.snapCollapse ?? true),
    // dockLocked：锁定为收缩胶囊（悬停不展开）且窗口不可拖动。由设置开关与浮窗内锁按钮共同控制。
    dockLocked: asBoolean(raw.dockLocked ?? false),
    closeAction,
  };
}

function loadStatsOverlayPreferences() {
  try {
    const stored = localStorage.getItem(STATS_OVERLAY_PREFERENCES_KEY);
    return normalizeStatsOverlayPreferences(stored ? JSON.parse(stored) : {});
  } catch {
    return normalizeStatsOverlayPreferences({});
  }
}

function syncStatsOverlayPreferencesFromStorage() {
  appState.statsOverlayPreferences = loadStatsOverlayPreferences();
}

function bindStatsOverlayPreferenceSync() {
  if (statsOverlayPreferenceSyncBound || typeof window === "undefined") return;
  window.addEventListener("storage", (event) => {
    if (event.key === STATS_OVERLAY_PREFERENCES_KEY) syncStatsOverlayPreferencesFromStorage();
  });
  window.addEventListener(STATS_OVERLAY_CHANGED_EVENT, syncStatsOverlayPreferencesFromStorage);
  statsOverlayPreferenceSyncBound = true;
}

bindStatsOverlayPreferenceSync();

function persistStatsOverlayPreferences(next) {
  const normalized = normalizeStatsOverlayPreferences(next);
  try {
    localStorage.setItem(STATS_OVERLAY_PREFERENCES_KEY, JSON.stringify(normalized));
  } catch { /* localStorage 不可用时仅内存生效 */ }
  appState.statsOverlayPreferences = normalized;
  // storage 事件只在其它窗口触发；同窗口内派发自定义事件补刀（StatsOverlay 已注册监听）。
  window.dispatchEvent(new Event(STATS_OVERLAY_CHANGED_EVENT));
  return normalized;
}

export function getStatsOverlayPreferences() {
  // 同步读取：调用方（SettingsDrawer/StatsOverlay 的 onMounted）均未 await。
  const stored = loadStatsOverlayPreferences();
  appState.statsOverlayPreferences = stored;
  return stored;
}

export async function setStatsOverlayPreferences(partial) {
  const next = { ...loadStatsOverlayPreferences(), ...(partial || {}) };
  const persisted = persistStatsOverlayPreferences(next);
  // 样式尺寸与窗口层级都由浮窗自己的响应式偏好监听同步，避免多个窗口竞争原生状态。
  if (partial && "closeAction" in partial) {
    await setMainWindowCloseAction(persisted.closeAction);
  }
  // dockLocked 为纯前端 UX 状态（CSS 控制胶囊收缩与拖拽禁用），无需同步后端。
  return persisted;
}

export async function showStatsOverlay(position) {
  const current = loadStatsOverlayPreferences();
  const next = { ...current, visible: true };
  // 如果传入了位置参数，保存到偏好中
  if (position && typeof position === "object") {
    if (typeof position.x === "number") next.x = position.x;
    if (typeof position.y === "number") next.y = position.y;
  }
  const persisted = persistStatsOverlayPreferences(next);
  // 传入位置参数到后端
  const hasPosition = typeof persisted.x === "number" && typeof persisted.y === "number";
  await openStatsOverlayWindow(persisted.x || 0, persisted.y || 0, hasPosition);
  // 尺寸由浮窗挂载后的布局同步；置顶偏好由独立原生调用立即对齐。
  await setStatsOverlayAlwaysOnTop(persisted.alwaysOnTop);
  return loadStatsOverlayPreferences();
}

function bindStatsOverlayShowRequest() {
  if (statsOverlayShowRequestBound || typeof window === "undefined") return;
  window.addEventListener(STATS_OVERLAY_SHOW_REQUESTED_EVENT, () => {
    void showStatsOverlay();
  });
  statsOverlayShowRequestBound = true;
}

bindStatsOverlayShowRequest();

export async function hideStatsOverlay() {
  const persisted = persistStatsOverlayPreferences({ ...loadStatsOverlayPreferences(), visible: false });
  await closeStatsOverlayWindow();
  return persisted;
}

export async function closeApplication() {
  persistStatsOverlayPreferences({ ...loadStatsOverlayPreferences(), visible: false });
  await closeStatsOverlayWindow();
  await closeApplicationNative();
}

const CURSOR_LAUNCH_PREFERENCES_KEY = "cursor-byok.cursor-launch.preferences";

function normalizeCursorManualPath(value) {
  return asString(value).replace(/^"|"$/g, "").trim();
}

export function getCursorManualPath() {
  try {
    const stored = localStorage.getItem(CURSOR_LAUNCH_PREFERENCES_KEY);
    const parsed = stored ? JSON.parse(stored) : {};
    return normalizeCursorManualPath(parsed?.manualPath);
  } catch {
    return "";
  }
}

export function setCursorManualPath(manualPath) {
  const normalized = normalizeCursorManualPath(manualPath);
  try {
    localStorage.setItem(CURSOR_LAUNCH_PREFERENCES_KEY, JSON.stringify({ manualPath: normalized }));
  } catch { /* localStorage 不可用时仅保留当前调用结果 */ }
  return normalized;
}

export async function openMetricsDetailWindow() {
  await openMetricsDetail();
}

export async function openRequestMetricsWindow() {
  await openRequestMetrics();
}

export async function saveRoutingMode(mode) {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload({
    ...currentConfig,
    routing: {
      mode: normalizeRouteMode(mode),
    },
  });
}

export async function reloadUserConfig(options = {}) {
  const config = await loadPersistedUserConfig();
  applyConfigToState(config, options);
  return config;
}

export async function saveModelAdapterAt(index, adapter) {
  const currentConfig = await loadPersistedUserConfig();
  const nextAdapters = dedupeModelAdapters(currentConfig.modelAdapters);
  const nextAdapter = normalizeModelAdapter(adapter);

  if (index >= 0 && index < nextAdapters.length) {
    nextAdapters.splice(index, 1, nextAdapter);
  } else {
    nextAdapters.push(nextAdapter);
  }

  const dedupedAdapters = dedupeModelAdapters(nextAdapters);
  const targetIdentity = buildModelAdapterIdentityKey(nextAdapter);
  const targetIndex = dedupedAdapters.findIndex(
    (item) => buildModelAdapterIdentityKey(item) === targetIdentity,
  );
  const result = await persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: dedupedAdapters,
    },
    { modelAdaptersOnly: true },
  );
  if (!result.ok) {
    return result;
  }
  return {
    ...result,
    index: targetIndex,
    adapter: appState.modelAdapters[targetIndex] ?? null,
  };
}

export async function saveModelAdaptersBatch(adapters) {
  const currentConfig = await loadPersistedUserConfig();
  const existingAdapters = normalizeModelAdapters(currentConfig.modelAdapters);
  const nextAdapters = existingAdapters.slice();
  const indexByIdentity = new Map(nextAdapters.map((adapter, index) => [buildModelAdapterIdentityKey(adapter), index]));
  let added = 0;
  let skipped = 0;
  let updated = 0;

  for (const source of Array.isArray(adapters) ? adapters : []) {
    const adapter = normalizeModelAdapter(source);
    const identity = buildModelAdapterIdentityKey(adapter);
    const existingIndex = indexByIdentity.get(identity);
    if (existingIndex == null) {
      indexByIdentity.set(identity, nextAdapters.length);
      nextAdapters.push(adapter);
      added += 1;
      continue;
    }

    const existing = nextAdapters[existingIndex];
    const merged = mergeDuplicateModelAdapter(existing, adapter);
    if (JSON.stringify(merged) !== JSON.stringify(existing)) {
      nextAdapters[existingIndex] = merged;
      updated += 1;
    } else {
      skipped += 1;
    }
  }

  if (added === 0 && updated === 0) {
    return { ok: true, error: "", added, skipped, updated, total: 0 };
  }
  const result = await persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
  return result.ok ? { ...result, added, skipped, updated, total: added + skipped + updated } : result;
}

/**
 * updateModelAdaptersBySupplier — 批量更新同一供应商下所有模型的共享（供应商级）配置。
 *
 * @param {object} supplierIdentity  — { mode, baseURL?, groupName? }，与路由 query 中的供应商标识一致。
 * @param {object} providerPatch     — 要覆盖的供应商级字段（见 PROVIDER_LEVEL_FIELDS）。
 * @returns {{ ok, error, updated, conflicts }}
 */

// 允许批量覆盖的供应商级字段；模型级字段不在此列表中，永远不会被覆盖。
const PROVIDER_LEVEL_FIELDS = new Set([
  "type", "supplierID", "baseURL", "apiKey", "groupName", "tooltipData",
  "protocolMode", "protocolGroup", "openAIEndpoint", "openAIRequestGroup",
  "customHeadersEnabled", "customHeadersJSON",
  "balanceQueryURL", "balanceQueryField", "balanceQueryHeadersJSON",
  "balanceProfile", "balanceAccessToken", "balanceUserID", "balanceCodingPlanProvider",
  "openAIExtraParamsEnabled", "openAIExtraParamsJSON",
  "anthropicExtraParamsEnabled", "anthropicExtraParamsJSON",
]);

export async function updateModelAdaptersBySupplier(supplierIdentity, providerPatch, options = {}) {
  const currentConfig = await loadPersistedUserConfig();
  const allAdapters = normalizeModelAdapters(currentConfig.modelAdapters);

  // 找出属于本供应商的 adapter 及其下标
  const targetIndices = [];
  for (let i = 0; i < allAdapters.length; i++) {
    if (adapterMatchesSupplierIdentity(allAdapters[i], supplierIdentity)) {
      targetIndices.push(i);
    }
  }
  if (targetIndices.length === 0) {
    return { ok: false, error: "未找到属于该供应商的模型配置", updated: 0, conflicts: [] };
  }

  // 只取允许覆盖的字段
  const patch = {};
  for (const key of Object.keys(providerPatch || {})) {
    if (PROVIDER_LEVEL_FIELDS.has(key)) {
      patch[key] = providerPatch[key];
    }
  }
  if (Object.keys(patch).length === 0) {
    return { ok: true, error: "", updated: 0, conflicts: [] };
  }

  // 应用 patch，保留模型级字段不变
  const nextAdapters = allAdapters.slice();
  for (const idx of targetIndices) {
    nextAdapters[idx] = normalizeModelAdapter({ ...allAdapters[idx], ...patch });
  }

  // 冲突检测：检查 patch 后的 adapter 是否与非目标 adapter 的 identity 碰撞
  const targetSet = new Set(targetIndices);
  const nonTargetIdentities = new Map();
  for (let i = 0; i < nextAdapters.length; i++) {
    if (!targetSet.has(i)) {
      nonTargetIdentities.set(buildModelAdapterIdentityKey(nextAdapters[i]), i);
    }
  }
  const conflicts = [];
  for (const idx of targetIndices) {
    const key = buildModelAdapterIdentityKey(nextAdapters[idx]);
    if (nonTargetIdentities.has(key)) {
      conflicts.push({
        targetIndex: idx,
        conflictIndex: nonTargetIdentities.get(key),
        modelID: nextAdapters[idx].modelID,
      });
    }
  }
  if (conflicts.length > 0 && !options.forceOverwrite) {
    return { ok: false, error: "存在重复模型冲突，请确认后重试", updated: 0, conflicts };
  }

  const result = await persistConfigPayload(
    { ...currentConfig, modelAdapters: nextAdapters },
    { modelAdaptersOnly: true },
  );
  return result.ok
    ? { ...result, updated: targetIndices.length, conflicts }
    : result;
}

// 余额查询可同步的字段（不含连接字段，保留各分组自身的 apiKey/groupName 等）。
const BALANCE_SYNC_FIELDS = new Set([
  "balanceQueryURL",
  "balanceQueryField",
  "balanceQueryHeadersJSON",
  "balanceProfile",
  "balanceAccessToken",
  "balanceUserID",
  "balanceCodingPlanProvider",
]);

/**
 * syncBalanceConfigToSameURL — 把余额查询配置同步到同一 baseURL 下的所有分组。
 *
 * 同一中转站下用户常有多个 key（各对应一个分组），但余额查询配置基本相同。
 * 本函数只覆盖余额字段，保留各分组自身的 apiKey / groupName / type / 连接配置，
 * 避免逐个分组手动配置余额查询。
 *
 * @param {string} baseURL              — 目标中转站 baseURL（按 normalizeBaseURL 归一化匹配）。
 * @param {object} balancePatch         — 余额查询配置草稿（只取 BALANCE_SYNC_FIELDS 中的字段）。
 * @returns {{ ok, error, updated }}
 */
export async function syncBalanceConfigToSameURL(baseURL, balancePatch) {
  const targetBase = normalizeBaseURL(baseURL);
  if (!targetBase) {
    return { ok: false, error: "缺少 baseURL，无法同步余额配置", updated: 0 };
  }
  const patch = {};
  for (const key of Object.keys(balancePatch || {})) {
    if (BALANCE_SYNC_FIELDS.has(key)) patch[key] = balancePatch[key];
  }
  if (Object.keys(patch).length === 0) {
    return { ok: true, error: "", updated: 0 };
  }

  const currentConfig = await loadPersistedUserConfig();
  const allAdapters = normalizeModelAdapters(currentConfig.modelAdapters);
  const targetIndices = [];
  for (let i = 0; i < allAdapters.length; i++) {
    if (normalizeBaseURL(allAdapters[i].baseURL) === targetBase) {
      targetIndices.push(i);
    }
  }
  if (targetIndices.length === 0) {
    return { ok: false, error: "未找到该 URL 下的模型配置", updated: 0 };
  }

  const nextAdapters = allAdapters.slice();
  for (const idx of targetIndices) {
    // 只覆盖余额字段；apiKey / groupName / 连接配置保持各 adapter 原值不变。
    nextAdapters[idx] = normalizeModelAdapter({ ...allAdapters[idx], ...patch });
  }

  const result = await persistConfigPayload(
    { ...currentConfig, modelAdapters: nextAdapters },
    { modelAdaptersOnly: true },
  );
  return result.ok
    ? { ...result, updated: targetIndices.length }
    : result;
}

export async function deleteModelAdapterAt(index) {
  const currentConfig = await loadPersistedUserConfig();
  const nextAdapters = normalizeModelAdapters(currentConfig.modelAdapters);

  if (index < 0 || index >= nextAdapters.length) {
    return {
      ok: false,
      error: "模型配置不存在，无法删除",
    };
  }

  nextAdapters.splice(index, 1);

  return persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
}

// 原子批量删除：一次性过滤掉目标渠道并只保存一次，避免逐个删除时多次落盘、
// 中途失败留下半删状态。targets 为待删除的适配器对象列表。
export async function deleteModelAdaptersBatch(targets) {
  const list = Array.isArray(targets) ? targets : [];
  if (list.length === 0) return { ok: true, error: "", removed: 0 };
  const currentConfig = await loadPersistedUserConfig();
  const existingAdapters = normalizeModelAdapters(currentConfig.modelAdapters);
  const removeKeys = new Set(
    list.map((adapter) => buildModelAdapterIdentityKey(normalizeModelAdapter(adapter))),
  );
  const nextAdapters = existingAdapters.filter(
    (adapter) => !removeKeys.has(buildModelAdapterIdentityKey(adapter)),
  );
  const removed = existingAdapters.length - nextAdapters.length;
  if (removed === 0) return { ok: true, error: "", removed: 0 };
  const result = await persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
  return result.ok ? { ...result, removed } : result;
}

export async function deleteAllModelAdapters() {
  const currentConfig = await loadPersistedUserConfig();
  return persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: [],
    },
    { modelAdaptersOnly: true },
  );
}

/**
 * 按供应商身份删除模型。
 * - 旧签名：deleteModelAdaptersBySupplier(baseURL, groupName) → 等同 legacy（baseURL+分组名）
 * - 新签名：deleteModelAdaptersBySupplier({ mode, baseURL?, groupName? })
 * mode: 'name' | 'connection' | 'legacy'
 */
export async function deleteModelAdaptersBySupplier(baseURLOrIdentity, groupName) {
  const currentConfig = await loadPersistedUserConfig();
  let identity;
  if (baseURLOrIdentity && typeof baseURLOrIdentity === "object" && !Array.isArray(baseURLOrIdentity)) {
    identity = {
      mode: asString(baseURLOrIdentity.mode || "legacy").trim() || "legacy",
      baseURL: baseURLOrIdentity.baseURL,
      groupName: baseURLOrIdentity.groupName,
    };
  } else {
    identity = {
      mode: "legacy",
      baseURL: baseURLOrIdentity,
      groupName,
    };
  }

  const mode = asString(identity.mode).trim().toLowerCase() || "legacy";
  const normalizedBaseURL = normalizeBaseURL(identity.baseURL);
  const normalizedGroupName = asString(identity.groupName).trim();

  const remaining = normalizeModelAdapters(currentConfig.modelAdapters).filter((adapter) => {
    const adapterBase = normalizeBaseURL(adapter.baseURL);
    const adapterGroup = asString(adapter.groupName).trim();
    if (mode === "name") {
      return adapterGroup !== normalizedGroupName;
    }
    if (mode === "connection") {
      return adapterBase !== normalizedBaseURL;
    }
    // legacy: baseURL + groupName
    return !(adapterBase === normalizedBaseURL && adapterGroup === normalizedGroupName);
  });
  return persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: remaining,
    },
    { modelAdaptersOnly: true },
  );
}

function splitDisplayNameSeed(value) {
  const text = asString(value);
  const match = text.match(/^(.*?)(?:\s*[-+](\d+))?$/);
  if (!match) {
    return { base: text || "模型", number: 0 };
  }
  const base = asString(match[1]) || "模型";
  const number = match[2] ? Number(match[2]) : 0;
  return { base, number: Number.isFinite(number) ? number : 0 };
}

function buildNextDisplayName(existingAdapters, sourceName) {
  const { base } = splitDisplayNameSeed(sourceName);
  let next = 1;
  const taken = new Set(
    normalizeModelAdapters(existingAdapters)
      .map((adapter) => adapter.displayName)
      .filter(Boolean),
  );

  while (taken.has(`${base}-${next}`)) {
    next += 1;
  }
  return `${base}-${next}`;
}

export async function duplicateModelAdapterAt(index) {
  const currentConfig = await loadPersistedUserConfig();
  const nextAdapters = normalizeModelAdapters(currentConfig.modelAdapters);

  if (index < 0 || index >= nextAdapters.length) {
    return {
      ok: false,
      error: "模型配置不存在，无法复制",
    };
  }

  const source = normalizeModelAdapter(nextAdapters[index]);
  const duplicate = {
    ...source,
    id: "",
    displayName: buildNextDisplayName(nextAdapters, source.displayName || source.modelID || "模型"),
  };

  nextAdapters.splice(index + 1, 0, duplicate);

  return persistConfigPayload(
    {
      ...currentConfig,
      modelAdapters: nextAdapters,
    },
    { modelAdaptersOnly: true },
  );
}

export async function syncServiceState() {
  const state = await getProxyState();
  applyProxyState(state);
  return state;
}

export async function syncHomeMetrics() {
  const startedAt = Date.now();
  appState.homeMetricsLoading = true;
  try {
    const summary = await getHomeMetricsSummary();
    applyHomeMetrics(summary);
    return {
      ok: true,
      error: "",
    };
  } catch (error) {
    appState.homeMetricsError = toUserError(error);
    return {
      ok: false,
      error: appState.homeMetricsError,
    };
  } finally {
    const elapsed = Date.now() - startedAt;
    if (elapsed < HOME_METRICS_MIN_LOADING_MS) {
      await delay(HOME_METRICS_MIN_LOADING_MS - elapsed);
    }
    appState.homeMetricsLoading = false;
  }
}

export async function startService() {
  if (appState.serviceBusy) {
    return { ok: false, error: "服务状态更新中，请稍后再试" };
  }
  appState.serviceBusy = true;
  try {
    const saved = await persistUserConfig();
    if (!saved.ok) {
      return saved;
    }
    const state = await startProxyService();
    applyProxyState(state);
    return { ok: true, error: "" };
  } catch (error) {
    await syncServiceState().catch(() => {});
    return { ok: false, error: toUserError(error) };
  } finally {
    appState.serviceBusy = false;
  }
}

export async function stopService() {
  if (appState.serviceBusy) {
    return { ok: false, error: "服务状态更新中，请稍后再试" };
  }
  appState.serviceBusy = true;
  try {
    const state = await stopProxyService();
    applyProxyState(state);
    return { ok: true, error: "" };
  } catch (error) {
    await syncServiceState().catch(() => {});
    return { ok: false, error: toUserError(error) };
  } finally {
    appState.serviceBusy = false;
  }
}

export async function toggleService() {
  if (appState.serviceRunning) {
    return stopService();
  }
  return startService();
}

export async function openLocalLogsDirectory() {
  await openLogsDirectory();
}

export async function exportLogsAction() {
  try {
    const path = await exportLogs();
    return { ok: true, path: String(path || ""), error: "" };
  } catch (error) {
    return { ok: false, path: "", error: toUserError(error) };
  }
}

export async function openConfigWindow() {
  await openConfig();
}

export async function openModelConfigWindow() {
  await openModelConfig();
}

export async function openModelEditorWindow(index, adapter) {
  const adapterJSON = JSON.stringify(normalizeModelAdapter(adapter));
  await openModelEditor(index, adapterJSON);
}

export async function checkForAppUpdates() {
  await checkForUpdates();
}

export function dismissUpdatePrompt() {
  appState.updatePromptVisible = false;
  appState.updatePromptBusy = false;
}

export async function confirmUpdatePrompt() {
  if (appState.updatePromptKind !== "ready") {
    dismissUpdatePrompt();
    return;
  }
  if (appState.updatePromptBusy) {
    return;
  }
  appState.updatePromptBusy = true;
  try {
    await installReadyUpdate();
  } catch (error) {
    appState.updatePromptBusy = false;
    const message = toUserError(error);
    appState.updateError = message;
    openUpdatePrompt("error", { error: message });
  }
}

export function toUserError(error) {
  const message = extractErrorMessage(error);
  return message || GENERIC_SERVICE_ERROR;
}

export async function bootstrapAppState() {
  try {
    await reloadUserConfig();
  } catch (_error) {
    // keep cached config if loading fails
  }
  await refreshModelAdapterTestResults().catch(() => {});
  try {
    appState.appVersion = await getAppVersion();
  } catch (_error) {
    appState.appVersion = "";
  }
  await syncServiceState().catch(() => {});
  await syncHomeMetrics().catch(() => {});
  
  // 根据用户偏好自动打开悬浮窗
  const overlayPrefs = getStatsOverlayPreferences();
  await setMainWindowCloseAction(overlayPrefs.closeAction).catch(() => {});
  if (overlayPrefs.visible) {
    // 延迟打开，避免阻塞主窗口启动
    setTimeout(() => {
      showStatsOverlay({ x: overlayPrefs.x, y: overlayPrefs.y }).catch(() => {});
    }, 500);
  }
}
