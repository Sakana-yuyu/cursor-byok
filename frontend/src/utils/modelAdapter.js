// modelAdapter.js 承载模型适配器元数据与归一化纯函数，全部从 appState.js 逐字搬移（零行为变化）。
import { asArray, asBoolean, asNumber, asPositiveInteger, asString } from "./valueCast";
import {
  ANTHROPIC_THINKING_EFFORT_DEFAULT,
  OPENAI_ENDPOINT_RESPONSES,
  OPENAI_REQUEST_GROUP_RESPONSES,
  PROTOCOL_MODE_AUTO,
  SUPPORTED_PROTOCOL_MODES,
  isValidOpenAIEndpoint,
  isValidOpenAIRequestGroup,
  normalizeOpenAIEndpoint,
  normalizeOpenAIRequestGroup,
  normalizeProtocolGroup,
  normalizeProtocolMode,
} from "./protocolMeta";
import {
  BALANCE_QUERY_HEADERS_DEFAULT_JSON,
  balanceQueryHeadersToJSON,
  hasBalanceQueryHeadersJSON,
  normalizePricing,
  parseBalanceQueryHeaders,
  validateAnthropicExtraParamsJSON,
  validateBalanceQueryHeadersJSON,
  validateHeadersJSON,
  validateJSONObject,
  validateOpenAIExtraParamsJSON,
} from "./configValidators";
import { contextWindowTokensForModel } from "./modelContext";
import { normalizeModelAdapterTestResult } from "./modelAdapterTestResult.js";

export {
  formatDuration,
  formatModelAdapterTestSummary,
  normalizeModelAdapterTestResult,
} from "./modelAdapterTestResult.js";

const SUPPORTED_MODEL_ADAPTER_TYPES = new Set(["openai", "anthropic", "gemini"]);
const SUPPORTED_REASONING_EFFORTS = new Set(["low", "medium", "high", "xhigh", "max"]);
const SUPPORTED_ANTHROPIC_THINKING_EFFORTS = new Set(["low", "medium", "high", "xhigh", "max"]);
export const ANTHROPIC_AUTH_MODE_LEGACY_DUAL = "legacy_dual";
export const ANTHROPIC_AUTH_MODE_AUTO = "auto";
export const ANTHROPIC_AUTH_MODE_X_API_KEY = "x_api_key";
export const ANTHROPIC_AUTH_MODE_BEARER = "bearer";
export const SUPPORTED_ANTHROPIC_AUTH_MODES = new Set([
  ANTHROPIC_AUTH_MODE_LEGACY_DUAL,
  ANTHROPIC_AUTH_MODE_AUTO,
  ANTHROPIC_AUTH_MODE_X_API_KEY,
  ANTHROPIC_AUTH_MODE_BEARER,
]);

export function normalizeAnthropicAuthMode(value, { legacy = true } = {}) {
  const normalized = asString(value).trim().toLowerCase();
  if (!normalized) return legacy ? ANTHROPIC_AUTH_MODE_LEGACY_DUAL : ANTHROPIC_AUTH_MODE_AUTO;
  return SUPPORTED_ANTHROPIC_AUTH_MODES.has(normalized) ? normalized : "";
}
export const OPENAI_EXTRA_PARAMS_DEFAULT_JSON = `{
}`;
export const EXTRA_PARAMS_DEFAULT_JSON = `{
}`;
export const CUSTOM_HEADERS_DEFAULT_JSON = `{
}`;
export function normalizeBaseURL(value) {
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

export function buildModelAdapterIdentityKey(adapter) {
  return [
    asString(adapter.type).toLowerCase(),
    normalizeBaseURL(adapter.baseURL),
    asString(adapter.modelID).toLowerCase(),
    asString(adapter.apiKey),
    adapter.type === "openai" ? normalizeOpenAIEndpoint(adapter.openAIEndpoint) : "",
    adapter.type === "anthropic" ? asString(adapter.anthropicAuthMode) : "",
    String(Boolean(adapter.customHeadersEnabled)),
    adapter.customHeadersEnabled ? asString(adapter.customHeadersJSON) : "",
    asString(adapter.groupName).trim(),
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
    adapter.type === "anthropic" ? asString(adapter.anthropicAuthMode) : "",
    String(asPositiveInteger(adapter.contextWindowTokens)),
    String(asPositiveInteger(adapter.maxCompletionTokens)),
    String(asPositiveInteger(adapter.anthropicMaxTokens)),
    adapter.type === "anthropic" ? asString(adapter.anthropicThinkingEffort || ANTHROPIC_THINKING_EFFORT_DEFAULT) : "",
  ].join("\n"));
}

export function normalizeModelAdapterTestResults(source) {
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
    anthropicAuthMode: ANTHROPIC_AUTH_MODE_AUTO,
    contextWindowTokens: 0,
    maxCompletionTokens: 0,
    anthropicMaxTokens: 0,
    anthropicThinkingEffort: ANTHROPIC_THINKING_EFFORT_DEFAULT,
    thinkingBudgetTokens: 0,
    pricing: null,
    fastMode: false,
    openAIServiceTier: "",
    modelCatalogURL: "",
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
  const anthropicAuthMode = normalizeAnthropicAuthMode(raw.anthropicAuthMode ?? raw.anthropic_auth_mode);
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
  const balanceProfile = ["auto", "none", "general", "newapi", "token_plan", "custom", "official"].includes(balanceProfileRaw)
    ? balanceProfileRaw
    : "auto";
  const balanceAccessToken = asString(raw.balanceAccessToken ?? raw.balance_access_token).trim();
  const balanceUserID = asString(raw.balanceUserID ?? raw.balance_user_id).trim();
  const balanceCodingPlanProvider = asString(
    raw.balanceCodingPlanProvider ?? raw.balance_coding_plan_provider,
  ).trim().toLowerCase();
  const modelCatalogURL = asString(raw.modelCatalogURL ?? raw.model_catalog_url).trim();
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
    anthropicAuthMode,
    thinkingBudgetTokens: asPositiveInteger(
      raw.thinkingBudgetTokens ?? raw.thinking_budget_tokens,
    ),
    pricing: normalizePricing(raw.pricing),
    fastMode: normalizedType === "openai" ? asBoolean(raw.fastMode ?? raw.fast_mode) : false,
    openAIServiceTier: normalizedType === "openai" ? asString(raw.openAIServiceTier ?? raw.openai_service_tier) : "",
    modelCatalogURL,
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
  if (["none", "general", "newapi", "token_plan", "custom", "official"].includes(raw)) {
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

export function mergeDuplicateModelAdapter(existing, incoming) {
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
    modelCatalogURL: existing.modelCatalogURL || incoming.modelCatalogURL,
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
    // 备注（tooltipData）在编辑器中标注为可选，不应阻止保存。
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
    if (!SUPPORTED_ANTHROPIC_AUTH_MODES.has(adapter.anthropicAuthMode)) {
      return `${prefix} 的 Anthropic 鉴权模式仅支持 legacy_dual、auto、x_api_key、bearer`;
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
