<script setup>
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { enableReaderMCP, getModelEditorContext } from "@/services/clientApi";
import { connectMCPRuntimeServer } from "@/services/runtimeControlApi";
import { resolveModelContextWindow, resolveModelCapabilities } from "@/utils/modelContext";
import { providerIcon, providerLabel, providerSelectOptions } from "@/utils/providerMeta";
import { supplierSelectOptions, supplierTemplate } from "@/utils/supplierCatalog";
import {
  SUPPLIER_GROUP_MODE_CONNECTION,
  SUPPLIER_GROUP_MODE_NAME,
  loadSupplierGroupMode,
  saveSupplierGroupMode,
} from "@/utils/supplierGrouping";
import { MODEL_CATALOG_DRAFT_KEY } from "@/utils/modelCatalogDraft";
import {
  ANTHROPIC_THINKING_EFFORT_DEFAULT,
  appState,
  BALANCE_QUERY_HEADERS_DEFAULT_JSON,
  buildModelAdapterTestRequestHash,
  classifyModelProtocol,
  inferProviderType,
  CUSTOM_HEADERS_DEFAULT_JSON,
  EXTRA_PARAMS_DEFAULT_JSON,
  getModelAdapterTestResult,
  getModelAdapterTestResultByID,
  isModelAdapterTestResultStale,
  normalizeModelAdapter,
  OPENAI_ENDPOINT_CHAT_COMPLETIONS,
  OPENAI_ENDPOINT_CUSTOM,
  OPENAI_ENDPOINT_RESPONSES,
  OPENAI_EXTRA_PARAMS_DEFAULT_JSON,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS,
  OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT,
  OPENAI_REQUEST_GROUP_RESPONSES,
  PROTOCOL_GROUP_ANTHROPIC_MESSAGES,
  PROTOCOL_GROUP_GEMINI_NATIVE,
  PROTOCOL_MODE_AUTO,
  PROTOCOL_MODE_FIXED,
  runModelAdapterTest,
  resolveBalanceProfileForAdapter,
  saveModelAdapterAt,
  toUserError,
  validateModelAdapters,
} from "@/state/appState";
import { runtimeWindow } from "@/services/runtimeAdapter";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";

const reasoningEffortOptions = [
  { label: "低", value: "low", icon: "icon-[mdi--head-outline]" },
  { label: "中", value: "medium", icon: "icon-[mdi--head-lightbulb-outline]" },
  { label: "高", value: "high", icon: "icon-[mdi--brain]" },
  { label: "极高", value: "xhigh", icon: "icon-[mdi--head-cog-outline]" },
  { label: "最高", value: "max", icon: "icon-[mdi--brain]" },
];

const anthropicThinkingEffortOptions = [
  { label: "低", value: "low", icon: "icon-[mdi--head-outline]" },
  { label: "中", value: "medium", icon: "icon-[mdi--head-lightbulb-outline]" },
  { label: "高", value: "high", icon: "icon-[mdi--brain]" },
  { label: "极高", value: "xhigh", icon: "icon-[mdi--head-cog-outline]" },
  { label: "Max", value: "max", icon: "icon-[mdi--brain]" },
];

const createEmptyModelAdapter = () => ({
  id: "",
  displayName: "",
  groupName: "",
  type: "openai",
  protocolMode: PROTOCOL_MODE_AUTO,
  protocolGroup: OPENAI_REQUEST_GROUP_RESPONSES,
  baseURL: "",
  apiKey: "",
  tooltipData: "",
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
  supplierID: "custom",
  modelCatalogURL: "",
  balanceQueryURL: "",
  balanceQueryField: "",
  balanceQueryHeaders: {},
  balanceQueryHeadersJSON: BALANCE_QUERY_HEADERS_DEFAULT_JSON,
  balanceProfile: "general",
  balanceAccessToken: "",
  balanceUserID: "",
  balanceCodingPlanProvider: "",
});

const balanceProfileOptions = [
  { label: "暂无自动查询", value: "none", icon: "icon-[mdi--minus-circle-outline]" },
  { label: "自定义", value: "custom", icon: "icon-[mdi--code-json]" },
  { label: "通用模板", value: "general", icon: "icon-[mdi--web]" },
  { label: "New API", value: "newapi", icon: "icon-[mdi--key-variant]" },
  { label: "Token Plan", value: "token_plan", icon: "icon-[mdi--chart-donut]" },
  { label: "官方", value: "official", icon: "icon-[mdi--shield-check-outline]" },
];

const codingPlanProviderOptions = [
  { label: "自动检测（按接口地址）", value: "", icon: "icon-[mdi--auto-fix]" },
  { label: "Kimi For Coding", value: "kimi", icon: "icon-[mdi--moon-waning-crescent]" },
  { label: "Zhipu GLM (智谱)", value: "zhipu", icon: "icon-[mdi--brain]" },
  { label: "Zhipu GLM Team (智谱团队)", value: "zhipu_team", icon: "icon-[mdi--account-group]" },
  { label: "MiniMax", value: "minimax", icon: "icon-[mdi--lightning-bolt]" },
  { label: "ZenMux", value: "zenmux", icon: "icon-[mdi--swap-horizontal]" },
  { label: "火山方舟 (Volcengine)", value: "volcengine", icon: "icon-[mdi--volcano]" },
];

const openAIEndpointOptions = [
  { label: "/v1/responses", value: OPENAI_ENDPOINT_RESPONSES, icon: "icon-[mdi--api]" },
  { label: "/v1/chat/completions", value: OPENAI_ENDPOINT_CHAT_COMPLETIONS, icon: "icon-[mdi--message-text-outline]" },
  { label: "自定义路径(请输入完整请求地址)", value: OPENAI_ENDPOINT_CUSTOM, icon: "icon-[mdi--pencil-outline]" },
];

const protocolModeOptions = [
  { label: "自动识别", value: PROTOCOL_MODE_AUTO, icon: "icon-[mdi--auto-fix]" },
  { label: "固定协议", value: PROTOCOL_MODE_FIXED, icon: "icon-[mdi--lock-outline]" },
];

const openAIRequestGroupOptions = [
  { label: "Responses（responses）", value: OPENAI_REQUEST_GROUP_RESPONSES, icon: "icon-[mdi--api]" },
  { label: "Chat Completions（chat_completions）", value: OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS, icon: "icon-[mdi--message-text-outline]" },
  { label: "Chat Completions 兼容模式（chat_completions_compat）", value: OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS_COMPAT, icon: "icon-[mdi--swap-horizontal]" },
];

const providerTypeOptions = providerSelectOptions();
const supplierOptions = computed(() => supplierSelectOptions(draft.supplierID));
const currentSupplierTemplate = computed(() => supplierTemplate(draft.supplierID));
const currentSupplierUsage = computed(() => currentSupplierTemplate.value.usage || {});
const currentSupplierCatalog = computed(() => currentSupplierTemplate.value.modelCatalog || {});
const supplierModelOptions = computed(() => {
  const template = supplierTemplate(draft.supplierID);
  const models = new Set(template.models || []);
  for (const preset of template.presets || []) {
    if (preset.model) models.add(preset.model);
  }
  return Array.from(models).map((model) => ({ label: model, value: model }));
});
const supplierPresetOptions = computed(() => (supplierTemplate(draft.supplierID).presets || []).map((item) => ({ label: item.label, value: item.model })));
const supplierHasPresets = computed(() => supplierPresetOptions.value.length > 0);

const editorIndex = ref(-1);
const router = useRouter();
const draft = reactive(createEmptyModelAdapter());
const errorMessage = ref("");
// 读图 MCP 设置：一键为不支持图片的模型添加读图 MCP（写入 Cursor 全局 mcp.json）。
const readerState = reactive({
  open: false,
  url: "",
  apiKey: "",
  model: "gpt-5.6-luna",
  busy: false,
  ok: "",
  error: "",
});

function openReaderPanel() {
  readerState.url = String(draft.baseURL || "");
  readerState.apiKey = String(draft.apiKey || "");
  readerState.model = "gpt-5.6-luna";
  readerState.open = true;
  readerState.ok = "";
  readerState.error = "";
}

async function handleEnableReaderMCP() {
  if (readerState.busy) {
    return;
  }
  const url = String(readerState.url || "").trim();
  if (!url) {
    readerState.error = "请填写视觉网关地址（url）";
    return;
  }
  readerState.busy = true;
  readerState.error = "";
  readerState.ok = "";
  try {
    const result = await enableReaderMCP(url, String(readerState.apiKey || "").trim(), String(readerState.model || "").trim());
    const identifier = String(result?.identifier || "vision-reader");
    readerState.ok = `读图 MCP「${identifier}」已添加并启用，可在「设置 → Skills 与 MCP」中查看。`;
    // 自动尝试连接（不阻塞，连接失败仅提示可手动连接）
    void connectReaderMCPAttempt(identifier);
  } catch (error) {
    readerState.error = toUserError(error);
  } finally {
    readerState.busy = false;
  }
}

async function connectReaderMCPAttempt(identifier) {
  try {
    await connectMCPRuntimeServer(identifier, `reader-${Date.now()}`, "");
    readerState.ok += "已尝试连接读图 MCP。";
  } catch (_error) {
    readerState.error = readerState.error
      ? `${readerState.error} 读图 MCP 自动连接失败，请到「设置 → Skills 与 MCP」手动连接。`
      : "读图 MCP 已添加，但自动连接失败，请到「设置 → Skills 与 MCP」手动连接。";
  }
}
const loading = ref(true);
const lastTestAdapterID = ref("");
const localTestFailure = ref("");
const manualAddMode = ref(false);
const catalogError = ref("");
// 快速添加：归类方式（与模型配置页「名称/连接」一致），不再在此页手填 groupName
const quickGroupMode = ref(loadSupplierGroupMode());
const quickGroupModeOptions = [
  { label: "按 URL 归类", value: SUPPLIER_GROUP_MODE_CONNECTION, icon: "icon-[mdi--link-variant]" },
  { label: "按渠道归类", value: SUPPLIER_GROUP_MODE_NAME, icon: "icon-[mdi--tag-outline]" },
];

function createOptionalPositiveIntegerModel(key) {
  return computed({
    get() {
      return draft[key] > 0 ? String(draft[key]) : "";
    },
    set(value) {
      const text = String(value || "").trim();
      draft[key] = /^\d+$/.test(text) && Number(text) > 0 ? Number(text) : 0;
    },
  });
}

const maxCompletionTokensInput = createOptionalPositiveIntegerModel("maxCompletionTokens");
const anthropicMaxTokensInput = createOptionalPositiveIntegerModel("anthropicMaxTokens");
const contextWindowTokensInput = createOptionalPositiveIntegerModel("contextWindowTokens");
const pricingInput = computed({
  get: () => draft.pricing?.input == null ? "" : String(draft.pricing.input),
  set: (value) => updatePricingRate("input", value),
});
const pricingOutput = computed({
  get: () => draft.pricing?.output == null ? "" : String(draft.pricing.output),
  set: (value) => updatePricingRate("output", value),
});
const pricingCacheRead = computed({
  get: () => draft.pricing?.cacheRead == null ? "" : String(draft.pricing.cacheRead),
  set: (value) => updatePricingRate("cacheRead", value),
});
const pricingCacheWrite = computed({
  get: () => draft.pricing?.cacheWrite == null ? "" : String(draft.pricing.cacheWrite),
  set: (value) => updatePricingRate("cacheWrite", value),
});
const pricingCurrency = computed({
  get: () => String(draft.pricing?.currency || "USD").toUpperCase(),
  set: (value) => {
    if (!draft.pricing) draft.pricing = { known: true, source: "manual" };
    draft.pricing = { ...draft.pricing, currency: String(value || "USD").trim().toUpperCase(), known: true, source: "manual" };
  },
});
const pricingKnown = computed(() => Boolean(draft.pricing?.known));

function updatePricingRate(field, value) {
  const text = String(value ?? "").trim();
  const next = { ...(draft.pricing || {}) };
  if (text === "") {
    delete next[field];
  } else if (/^\d+(\.\d+)?$/.test(text) && Number.isFinite(Number(text))) {
    next[field] = Number(text);
  }
  const hasRate = ["input", "output", "cacheRead", "cacheWrite"].some((key) => next[key] != null);
  draft.pricing = hasRate
    ? { ...next, currency: String(next.currency || "USD").trim().toUpperCase(), known: true, source: "manual" }
    : null;
}

function clearPricing() {
  draft.pricing = null;
}

const detectedCapabilities = computed(() => resolveModelCapabilities(draft.modelID));

// 上下文窗口快捷档位
const CONTEXT_TIERS = [
  { label: "128K", tokens: 128_000 },
  { label: "200K", tokens: 200_000 },
  { label: "256K", tokens: 256_000 },
  { label: "500K", tokens: 500_000 },
  { label: "1M", tokens: 1_000_000 },
];
const activeContextTier = computed(() => {
  const current = draft.contextWindowTokens;
  return CONTEXT_TIERS.find(t => t.tokens === current) || null;
});
const recommendedContextTier = computed(() => {
  const cap = detectedCapabilities.value;
  if (!cap) return null;
  return CONTEXT_TIERS.find(t => t.tokens === cap.contextWindowTokens) || null;
});
function selectContextTier(tier) {
  draft.contextWindowTokens = tier.tokens;
}

const interfacePlaceholder = computed(() => {
  if (draft.type === "anthropic") return "例如：https://api.anthropic.com";
  if (draft.type === "gemini") return "例如：https://generativelanguage.googleapis.com/v1beta";
  return "例如：https://api.openai.com/v1";
});
const quickBaseURLLabel = computed(() =>
  draft.type === "openai" ? "接口地址（自动补 /v1）" : "接口地址",
);
const autoProtocolGroup = computed(() => classifyModelProtocol(
  draft.type,
  draft.modelID,
  draft.baseURL,
  draft.openAIEndpoint,
  "",
));
const effectiveProtocolGroup = computed(() => draft.protocolMode === PROTOCOL_MODE_FIXED
  ? draft.protocolGroup
  : autoProtocolGroup.value);
const currentRequestHash = computed(() => buildModelAdapterTestRequestHash(draft));
const directModelTestResult = computed(() => getModelAdapterTestResult(draft));
const rememberedModelTestResult = computed(() =>
  lastTestAdapterID.value ? getModelAdapterTestResultByID(lastTestAdapterID.value) : null,
);
const activeModelTestResult = computed(() => directModelTestResult.value || rememberedModelTestResult.value);
const modelTestResultStale = computed(() =>
  isModelAdapterTestResultStale(draft, activeModelTestResult.value),
);
const isCurrentConfigTesting = computed(() => directModelTestResult.value?.status === "running");
const modelTestSummary = computed(() => {
  if (localTestFailure.value) {
    return localTestFailure.value;
  }
  return activeModelTestResult.value?.summaryText || "尚未测试";
});

const isQuickMode = computed(() => editorIndex.value < 0);
const title = computed(() => {
  if (manualAddMode.value) return "手动添加模型";
  return isQuickMode.value ? "快速添加模型" : "编辑模型配置";
});

// 高级设置分区：默认收起，存量非默认配置自动展开
const advancedExpanded = ref(false);

function hasBalanceQueryHeadersOverride(value) {
  const text = String(value || "").trim();
  if (!text || text === "{}") return false;
  try {
    const parsed = JSON.parse(text);
    return Boolean(parsed && typeof parsed === "object" && !Array.isArray(parsed) && Object.keys(parsed).length > 0);
  } catch (_error) {
    // 非法 JSON 也算覆盖，方便用户看到并修正
    return true;
  }
}

const hasAdvancedOverrides = computed(() => {
  if (draft.protocolMode === PROTOCOL_MODE_FIXED) return true;
  if (draft.openAIExtraParamsEnabled) return true;
  if (draft.anthropicExtraParamsEnabled) return true;
  if (draft.customHeadersEnabled) return true;
  if (String(draft.balanceQueryURL || "").trim()) return true;
  if (String(draft.balanceQueryField || "").trim()) return true;
  if (hasBalanceQueryHeadersOverride(draft.balanceQueryHeadersJSON)) return true;
  if (String(draft.balanceProfile || "general") !== "general") return true;
  if (String(draft.balanceAccessToken || "").trim()) return true;
  if (String(draft.balanceUserID || "").trim()) return true;
  if (String(draft.balanceCodingPlanProvider || "").trim()) return true;
  if (draft.fastMode) return true;
  if (draft.type === "openai") {
    if (String(draft.openAIEndpoint || "") !== OPENAI_ENDPOINT_RESPONSES) return true;
    if (Number(draft.maxCompletionTokens) > 0) return true;
  }
  return false;
});

function ensureOpenAIExtraParamsJSON() {
  if (!String(draft.openAIExtraParamsJSON || "").trim()) {
    draft.openAIExtraParamsJSON = OPENAI_EXTRA_PARAMS_DEFAULT_JSON;
  }
}

function ensureCustomHeadersJSON() {
  if (!String(draft.customHeadersJSON || "").trim()) {
    draft.customHeadersJSON = CUSTOM_HEADERS_DEFAULT_JSON;
  }
}

function ensureAnthropicExtraParamsJSON() {
  if (!String(draft.anthropicExtraParamsJSON || "").trim()) {
    draft.anthropicExtraParamsJSON = EXTRA_PARAMS_DEFAULT_JSON;
  }
}

function ensureAnthropicThinkingEffort() {
  if (!String(draft.anthropicThinkingEffort || "").trim()) {
    draft.anthropicThinkingEffort = ANTHROPIC_THINKING_EFFORT_DEFAULT;
  }
}

function ensureOpenAIRequestGroup() {
  if (!String(draft.openAIRequestGroup || "").trim()) {
    draft.openAIRequestGroup = draft.openAIEndpoint === OPENAI_ENDPOINT_RESPONSES
      ? OPENAI_REQUEST_GROUP_RESPONSES
      : OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
  }
}

const fieldTips = {
  displayName: "仅用于界面展示，便于你区分不同模型。",
  modelID: "请求实际发送给服务端的模型名称，例如 gpt-4.1 或 claude-sonnet。",
  baseURL: "模型服务的 API 根地址，通常为兼容 OpenAI 或 Anthropic 的接口入口。",
  modelCatalogURL: "直接读取模型列表的完整 URL。留空时使用供应商预设地址或兼容协议的自动候选地址。",
  apiKey: "调用该模型服务需要使用的访问密钥。",
  contextWindowTokens: "模型单次可接受的最大上下文 Token 数。留空时使用默认值。",
  reasoningEffort: "推理强度仅对部分支持 reasoning_effort 的模型生效，并不是所有模型都支持。越高通常越稳，但也可能更慢。",
  maxCompletionTokens: "单次回复允许生成的最大 Token 数。留空时使用默认值。",
  openAIEndpoint: "选择接口协议端点。选“自定义路径”时，请在接口地址栏填写完整请求地址（含 /chat/completions 或 /responses 路径后缀），系统会根据末段自动判断协议形态。",
  protocolMode: "自动识别会根据供应商、模型名称和接口地址选择协议；固定协议会严格使用你指定的协议，测速不会自动覆盖。",
  openAIRequestGroup: "请求分组决定请求体协议形态，括号内为发送给服务端的技术值。Responses 适用于新版 /v1/responses 接口；Chat Completions 适用于标准对话补全接口；Chat Completions 兼容模式用于部分字段不完全兼容的第三方网关（会跳过高级字段）。一般与接口端点保持对应。",
  openAIExtraParams: "开启后会把 JSON 对象覆盖到 OpenAI 请求体。同名字段以这里为准。OpenAI service_tier 支持 auto、default、flex、scale、priority。",
  customHeaders: "开启后会把 JSON 对象覆盖到最终请求头。同名请求头以这里为准，值必须是字符串。",
  anthropicExtraParams: "开启后会把 JSON 对象覆盖到 Anthropic 请求体。同名字段以这里为准。",
  anthropicMaxTokens: "Anthropic 模型单次回复允许生成的最大 Token 数。留空时使用默认值。",
  anthropicThinkingEffort: "Anthropic adaptive thinking 的思考强度。请求会固定使用新版 thinking.type=adaptive。",
  tooltipData: "模型列表 hover 时显示的备注说明。",
  balanceQueryURL: "自定义余额查询接口的完整 URL。支持 {{apiKey}}、{{baseUrl}}、{{accessToken}}、{{userId}}。",
  balanceQueryField: "从 JSON 响应中取值的点分路径，例如 data.balance 或 data.0.total_balance。仅自定义模板需要。",
  balanceQueryHeaders: "自定义余额查询请求头 JSON。支持 {{apiKey}}、{{baseUrl}}、{{accessToken}}、{{userId}}。",
  balanceProfile: "查询模板：自定义 / 通用模板 / New API / Token Plan / 官方。官方模板按接口地址识别具名供应商，通用模板请求 /user/balance。",
  balanceAccessToken: "New API 个人安全设置中生成的访问令牌（不是渠道 sk）。",
  balanceUserID: "New API 用户 ID，请求头 New-Api-User 使用。",
  balanceCodingPlanProvider: "Token Plan 供应商。智谱团队版与个人版 baseURL 相同，必须显式选择 Team。",
};

async function loadContext() {
  try {
    const ctx = await getModelEditorContext();
    editorIndex.value = typeof ctx.index === "number" ? ctx.index : -1;
    const parsed = JSON.parse(ctx.adapterJSON || "{}");
    const normalized = normalizeModelAdapter(parsed);
    Object.assign(draft, normalized);
    draft.balanceProfile = resolveBalanceProfileForAdapter(normalized);
    if (!draft.type) {
      draft.type = "openai";
    }
  } catch (_error) {
    Object.assign(draft, createEmptyModelAdapter());
    draft.type = "openai";
  } finally {
    loading.value = false;
    if (hasAdvancedOverrides.value) {
      advancedExpanded.value = true;
    }
  }
}

async function persistDraft() {
  if (draft.protocolMode !== PROTOCOL_MODE_FIXED) {
    draft.protocolMode = PROTOCOL_MODE_AUTO;
    draft.protocolGroup = autoProtocolGroup.value;
  }
  if (draft.type === "openai") {
    draft.openAIRequestGroup = draft.protocolGroup;
  } else if (draft.type === "anthropic") {
    draft.protocolGroup = PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  } else if (draft.type === "gemini") {
    draft.protocolGroup = PROTOCOL_GROUP_GEMINI_NATIVE;
  }
  const adapter = normalizeModelAdapter(draft);

  const singleCheck = validateModelAdapters([adapter]);
  if (singleCheck) {
    errorMessage.value = singleCheck;
    return { ok: false, error: singleCheck, adapter: null };
  }

  const result = await saveModelAdapterAt(editorIndex.value, adapter);
  if (!result.ok) {
    errorMessage.value = result.error;
    return { ok: false, error: result.error, adapter: null };
  }

  if (typeof result.index === "number") {
    editorIndex.value = result.index;
  }
  if (result.adapter) {
    Object.assign(draft, normalizeModelAdapter(result.adapter));
  }
  errorMessage.value = "";
  return {
    ok: true,
    error: "",
    adapter: result.adapter ? normalizeModelAdapter(result.adapter) : normalizeModelAdapter(draft),
  };
}

async function closeEditor() {
  if (isBrowserPreview) {
    await router.push("/model-config");
    return;
  }
  await runtimeWindow.Close();
}

async function handleSave() {
  const result = await persistDraft();
  if (!result.ok) {
    return;
  }
  await closeEditor();
}

async function handleCancel() {
  await closeEditor();
}

function applySupplierTemplate(id, { force = false } = {}) {
  const template = supplierTemplate(id);
  draft.supplierID = template.id;
  if (template.baseURL && (force || !draft.baseURL)) draft.baseURL = template.baseURL;
  if (template.type !== draft.type) {
    draft.type = template.type;
  }
  if (template.type === "openai") {
    if (force || !draft.openAIEndpoint) draft.openAIEndpoint = template.endpoint || OPENAI_ENDPOINT_CHAT_COMPLETIONS;
    if (force || !draft.openAIRequestGroup) draft.openAIRequestGroup = template.requestGroup || OPENAI_REQUEST_GROUP_CHAT_COMPLETIONS;
    draft.protocolGroup = draft.openAIRequestGroup;
  } else if (template.type === "anthropic") {
    draft.protocolGroup = PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  } else if (template.type === "gemini") {
    draft.protocolGroup = PROTOCOL_GROUP_GEMINI_NATIVE;
  }
  const defaultModel = template.models[0] || template.presets?.[0]?.model || "";
  if (force && defaultModel) {
    draft.modelID = defaultModel;
  }
  // Token Plan 供应商：默认切到 token_plan 模板并写入 codingPlanProvider（对齐 cc-switch 自动注入）
  if (force) {
    const planMap = {
      kimi_coding: "kimi",
      zhipu: "zhipu",
      zhipu_team: "zhipu_team",
      minimax: "minimax",
      zenmux: "zenmux",
      volcengine: "",
    };
    if (Object.prototype.hasOwnProperty.call(planMap, template.id)) {
      draft.balanceProfile = "token_plan";
      draft.balanceCodingPlanProvider = planMap[template.id];
    }
  }
  const usage = template.usage || {};
  if (force && usage.status === "fixed") {
    draft.balanceProfile = "official";
    draft.balanceCodingPlanProvider = "";
  } else if (force && usage.status === "token_plan") {
    draft.balanceProfile = "token_plan";
    draft.balanceCodingPlanProvider = usage.provider || "";
  } else if (force && usage.status === "general") {
    draft.balanceProfile = "general";
  } else if (force && (usage.status === "none" || usage.status === "custom_only")) {
    draft.balanceProfile = usage.status === "custom_only" ? "custom" : "none";
  }
}

function handleSupplierChange(id) {
  applySupplierTemplate(id, { force: true });
}

function handleSupplierPreset(modelID) {
  draft.modelID = modelID;
  const template = supplierTemplate(draft.supplierID);
  const preset = (template.presets || []).find((item) => item.model === modelID);
  if (preset?.baseURL) {
    draft.baseURL = preset.baseURL;
  }
  // 火山 Coding Plan 入口：便于 Token Plan 自动识别
  if (draft.supplierID === "volcengine" && String(preset?.baseURL || "").includes("/api/coding")) {
    draft.balanceProfile = "token_plan";
    draft.balanceCodingPlanProvider = "volcengine";
  }
}

function handleModelTypeChange(type) {
  draft.type = type;
  if (type === "anthropic" && draft.supplierID === "custom") draft.supplierID = "anthropic";
  if (type === "gemini" && draft.supplierID === "custom") draft.supplierID = "gemini";
  if (type === "openai" && (draft.supplierID === "anthropic" || draft.supplierID === "gemini")) draft.supplierID = "custom";
  draft.protocolMode = draft.protocolMode || PROTOCOL_MODE_AUTO;
  if (type === "openai") {
    if (!draft.openAIEndpoint) {
      draft.openAIEndpoint = OPENAI_ENDPOINT_RESPONSES;
    }
    ensureOpenAIRequestGroup();
    draft.protocolGroup = draft.protocolMode === PROTOCOL_MODE_FIXED
      ? (draft.protocolGroup || draft.openAIRequestGroup)
      : classifyModelProtocol(type, draft.modelID, draft.baseURL, draft.openAIEndpoint, "");
  } else if (type === "anthropic") {
    draft.protocolGroup = PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
    ensureAnthropicThinkingEffort();
  } else if (type === "gemini") {
    draft.protocolGroup = PROTOCOL_GROUP_GEMINI_NATIVE;
  }
}

function ensureV1Suffix() {
  if (draft.type !== "openai") return;
  let url = String(draft.baseURL || "").trim();
  if (!url) return;
  url = url.replace(/\/+$/, "");
  if (!/\/v\d+(\/.*)?$/.test(url)) {
    draft.baseURL = `${url}/v1`;
  } else {
    draft.baseURL = url;
  }
}

function handleQuickGroupModeChange(mode) {
  quickGroupMode.value = saveSupplierGroupMode(mode);
}

/** 进入独立「拉取模型」页；连接参数走 sessionStorage，避免密钥进 URL */
async function openCatalogPage() {
  catalogError.value = "";
  ensureV1Suffix();
  const baseURL = String(draft.baseURL || "").trim();
  const apiKey = String(draft.apiKey || "").trim();
  if (!baseURL || !apiKey) {
    catalogError.value = "请先填写接口地址和访问密钥";
    return;
  }
  const mode = saveSupplierGroupMode(quickGroupMode.value);
  try {
    sessionStorage.setItem(
      MODEL_CATALOG_DRAFT_KEY,
      JSON.stringify({
        type: draft.type,
        supplierID: draft.supplierID,
        baseURL,
        apiKey,
        modelCatalogURL: draft.modelCatalogURL || "",
        balanceProfile: draft.balanceProfile || "auto",
        balanceAccessToken: draft.balanceAccessToken || "",
        balanceUserID: draft.balanceUserID || "",
        balanceCodingPlanProvider: draft.balanceCodingPlanProvider || "",
        balanceQueryURL: draft.balanceQueryURL || "",
        balanceQueryField: draft.balanceQueryField || "",
        balanceQueryHeadersJSON: draft.balanceQueryHeadersJSON || "",
        modelCatalogStatus: supplierTemplate(draft.supplierID).modelCatalog?.status || "",
        appendModelCatalogCandidates: supplierTemplate(draft.supplierID).modelCatalog?.appendCandidates !== false,
        modelCatalogURLsJSON: JSON.stringify(supplierTemplate(draft.supplierID).modelCatalog?.urls || []),
        customHeadersEnabled: Boolean(draft.customHeadersEnabled),
        customHeadersJSON: draft.customHeadersJSON || "",
        tooltipData: String(draft.tooltipData || "").trim(),
        groupMode: mode,
        // 渠道名只在拉取页填写，避免与本页重复
        channelName: "",
      }),
    );
  } catch (error) {
    catalogError.value = toUserError(error) || "无法写入临时连接参数";
    return;
  }
  await router.push({ path: "/model-catalog" });
}

async function handleTest() {
  localTestFailure.value = "";
  try {
    const saved = await persistDraft();
    if (!saved.ok || !saved.adapter) {
      return;
    }
    const result = await runModelAdapterTest(saved.adapter);
    if (result?.adapterID) {
      lastTestAdapterID.value = result.adapterID;
    }
  } catch (error) {
    const latest = getModelAdapterTestResult(draft);
    if (latest?.adapterID) {
      lastTestAdapterID.value = latest.adapterID;
      return;
    }
    localTestFailure.value = toUserError(error);
  }
}

watch(
  () => draft.modelID,
  () => {
    if (draft.contextWindowTokens <= 0 && detectedContextWindow.value?.tokens) {
      draft.contextWindowTokens = detectedContextWindow.value.tokens;
    }
  },
);

watch(
  directModelTestResult,
  (result) => {
    if (!result?.adapterID) {
      return;
    }
    lastTestAdapterID.value = result.adapterID;
    if (result.status !== "running") {
      localTestFailure.value = "";
    }
  },
  { immediate: true },
);

watch(currentRequestHash, () => {
  localTestFailure.value = "";
});

// 快速添加（新建）模式下，根据 modelID 自动推断 provider type：
// 填入 claude-* 自动切到 anthropic、gemini-* 切到 gemini，避免误配成 openai 导致缓存失效。
// 仅在新建（isQuickMode）时生效；编辑已有渠道时不自动改 type，尊重用户已有配置。
watch(
  () => draft.modelID,
  (modelID) => {
    if (!isQuickMode.value) return;
    const inferred = inferProviderType(modelID, draft.type);
    if (inferred !== draft.type) {
      handleModelTypeChange(inferred);
    }
  },
);

watch(
  () => [draft.type, draft.modelID, draft.baseURL, draft.openAIEndpoint, draft.protocolMode],
  () => {
    if (draft.type === "anthropic") {
      draft.protocolGroup = PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
      return;
    }
    if (draft.type === "gemini") {
      draft.protocolGroup = PROTOCOL_GROUP_GEMINI_NATIVE;
      return;
    }
    if (draft.type !== "openai") return;
    if (draft.protocolMode === PROTOCOL_MODE_FIXED) {
      ensureOpenAIRequestGroup();
      draft.protocolGroup = draft.openAIRequestGroup;
      return;
    }
    draft.protocolMode = PROTOCOL_MODE_AUTO;
    draft.protocolGroup = autoProtocolGroup.value;
    draft.openAIRequestGroup = draft.protocolGroup;
  },
);

watch(
  () => draft.openAIRequestGroup,
  (group) => {
    if (draft.type === "openai" && draft.protocolMode === PROTOCOL_MODE_FIXED) {
      draft.protocolGroup = group;
      if (draft.openAIEndpoint !== OPENAI_ENDPOINT_CUSTOM) {
        draft.openAIEndpoint = group === OPENAI_REQUEST_GROUP_RESPONSES
          ? OPENAI_ENDPOINT_RESPONSES
          : OPENAI_ENDPOINT_CHAT_COMPLETIONS;
      }
    }
  },
);

watch(
  () => draft.openAIExtraParamsEnabled,
  (enabled) => {
    if (enabled) {
      ensureOpenAIExtraParamsJSON();
    }
  },
);

watch(
  () => draft.customHeadersEnabled,
  (enabled) => {
    if (enabled) {
      ensureCustomHeadersJSON();
    }
  },
);

watch(
  () => draft.anthropicExtraParamsEnabled,
  (enabled) => {
    if (enabled) {
      ensureAnthropicExtraParamsJSON();
    }
  },
);

onMounted(async () => {
  await loadContext();
});
</script>

<template>
  <div class="flex h-full flex-col text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between px-4 pb-2">
      <h2 class="text-base font-medium text-white">{{ title }}</h2>
      <div class="flex items-center gap-2">
        <Button variant="default" @click="handleCancel">取消</Button>
        <template v-if="manualAddMode || !isQuickMode">
          <Button variant="default" :disabled="isCurrentConfigTesting || appState.configSaving" @click="handleTest">
            {{ isCurrentConfigTesting ? "测试中..." : "保存并测试" }}
          </Button>
          <Button variant="primary" :disabled="appState.configSaving" @click="handleSave">
            {{ appState.configSaving ? "保存中..." : "保存" }}
          </Button>
        </template>
      </div>
    </div>

    <div v-if="loading" class="flex flex-1 items-center justify-center text-sm text-[#a3a3a3]">
      加载中...
    </div>

    <div v-else class="flex-1 overflow-y-auto min-h-0 px-4 pb-4">
      <div class="flex flex-col gap-4">
        <div v-if="isQuickMode && !manualAddMode" class="flex flex-col gap-3">
          <div class="center-row justify-between gap-3 rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2">
            <div class="center-row min-w-0 gap-2">
              <span class="icon-[mdi--information-outline] shrink-0 text-[16px] text-[#8f8f8f]"></span>
              <div class="min-w-0">
                <div class="truncate text-sm text-[#d4d4d4]">没有模型列表？手动添加单个模型</div>
                <div class="truncate text-xs text-[#8f8f8f]">适用于不提供 /models 接口的供应商</div>
              </div>
            </div>
            <Button variant="default" @click="manualAddMode = true">手动添加</Button>
          </div>

          <div class="flex flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#252525] p-3">
            <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
              <label class="flex flex-col gap-1">
                <span class="text-sm text-[#d4d4d4]">模型类型</span>
                <Select
                  :model-value="draft.type"
                  :options="providerTypeOptions"
                  button-class="h-9 text-sm"
                  @update:model-value="handleModelTypeChange"
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-sm text-[#d4d4d4]">归类方式</span>
                <Select
                  :model-value="quickGroupMode"
                  :options="quickGroupModeOptions"
                  button-class="h-9 text-sm"
                  @update:model-value="handleQuickGroupModeChange"
                />
                <span class="text-xs text-[#8f8f8f]">
                  {{ quickGroupMode === SUPPLIER_GROUP_MODE_NAME
                    ? "按渠道：拉取页填写一次渠道名"
                    : "按 URL：按接口地址自动汇总" }}
                </span>
              </label>
            </div>
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">{{ quickBaseURLLabel }}</span>
              <input
                v-model="draft.baseURL"
                type="text"
                :placeholder="interfacePlaceholder"
                class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
                @blur="ensureV1Suffix"
              />
            </label>
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">访问密钥</span>
              <Input
                v-model="draft.apiKey"
                type="password"
                allow-visibility-toggle
                placeholder="例如：sk-xxxxxx"
                autocomplete="off"
              />
            </label>
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">备注</span>
              <textarea
                v-model="draft.tooltipData"
                rows="2"
                placeholder="可选，例如：用于日常代码补全"
                class="resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
              ></textarea>
            </label>
            <Button
              variant="primary"
              :disabled="!draft.baseURL || !draft.apiKey"
              @click="openCatalogPage"
            >
              拉取模型
            </Button>
          </div>
          <div v-if="catalogError" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
            {{ catalogError }}
          </div>
        </div>

        <template v-else>
        <div class="rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2.5 text-sm text-[#d4d4d4]">
          <template v-if="manualAddMode">
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">供应商类型</span>
              <Select
                :model-value="draft.type"
                :options="providerTypeOptions"
                button-class="h-9 text-sm"
                @update:model-value="handleModelTypeChange"
              />
            </label>
          </template>
          <template v-else>
            <div class="center-row justify-start gap-2">
              <span class="center-row size-7 shrink-0 justify-center rounded-[6px] bg-[#232323]">
                <span :class="[providerIcon(draft.type), 'text-[16px]']"></span>
              </span>
              <span class="text-xs text-[#8f8f8f]">供应商</span>
              <span class="font-medium text-white">{{ providerLabel(draft.type) }}</span>
            </div>
          </template>
        </div>

        <div class="text-xs font-medium uppercase tracking-[0.08em] text-[#737373]">基础信息</div>

        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="flex flex-col gap-1 md:col-span-2">
            <span class="text-sm text-[#d4d4d4]">供应商模板</span>
            <Select :model-value="draft.supplierID" :options="supplierOptions" @update:model-value="handleSupplierChange" />
            <span class="text-xs text-[#8f8f8f]">选择固定供应商会自动填充接口地址、协议和常用模型；仍可在下方覆盖。</span>
            <span class="text-xs leading-5 text-[#737373]">
              模型目录：{{ currentSupplierCatalog.status === 'manual_only' ? '手动添加' : currentSupplierCatalog.source || '预设地址' }}
              · 用量：{{ currentSupplierUsage.status === 'none' ? '暂无自动查询' : currentSupplierUsage.source || currentSupplierUsage.status || '自动匹配' }}
            </span>
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.displayName" />
              <span>显示名称</span>
            </span>
            <input
              v-model="draft.displayName"
              type="text"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label class="flex flex-col gap-1">
            <span class="text-sm text-[#d4d4d4]">用户分组名称</span>
            <input
              v-model="draft.groupName"
              type="text"
              placeholder="可选，用于「按渠道」列表汇总"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
            <span class="text-xs text-[#8f8f8f]">留空时列表里显示为「默认分组」；与模型配置页的名称分组对应。</span>
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.modelID" />
              <span>模型标识</span>
            </span>
            <div class="flex gap-2">
              <input
                v-model="draft.modelID"
                type="text"
                placeholder="例如：gpt-4.1"
                class="h-9 min-w-0 flex-1 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
              />
              <Select
                v-if="supplierModelOptions.length"
                class="w-48 shrink-0"
                :model-value="draft.modelID"
                :options="supplierModelOptions"
                @update:model-value="draft.modelID = $event"
              />
              <Select
                v-if="supplierHasPresets"
                class="w-36 shrink-0"
                :model-value="draft.modelID"
                :options="supplierPresetOptions"
                @update:model-value="handleSupplierPreset"
              />
              <Button
                v-if="!manualAddMode && isQuickMode"
                variant="default"
                :disabled="!draft.baseURL || !draft.apiKey"
                @click="openCatalogPage"
              >
                拉取模型
              </Button>
            </div>
            <div v-if="catalogError" class="mt-1 text-xs text-[#fca5a5]">
              {{ catalogError }}
            </div>
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.apiKey" />
              <span>访问密钥</span>
            </span>
            <Input
              v-model="draft.apiKey"
              type="password"
              allow-visibility-toggle
              placeholder="例如：sk-xxxxxx"
              autocomplete="off"
            />
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.baseURL" />
              <span>接口地址</span>
            </span>
            <input
              v-model="draft.baseURL"
              type="text"
              :placeholder="interfacePlaceholder"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label class="flex flex-col gap-1 md:col-span-2">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.modelCatalogURL" />
              <span>模型目录 URL（可选）</span>
            </span>
            <input
              v-model="draft.modelCatalogURL"
              type="text"
              :placeholder="currentSupplierCatalog.urls?.[0] || '例如：https://api.example.com/v1/models'"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
            <span class="text-xs leading-5 text-[#737373]">留空使用供应商预设地址；手动添加供应商时，请填写返回模型数组的完整地址。</span>
          </label>

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.contextWindowTokens" />
              <span>上下文窗口</span>
            </span>

            <!-- 模型能力徽章 -->
            <div v-if="detectedCapabilities" class="flex flex-wrap items-center gap-1.5">
              <span v-if="detectedCapabilities.supportsVision" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#86efac]">
                <span class="icon-[mdi--eye-outline] text-[12px]"></span>视觉
              </span>
              <span v-if="detectedCapabilities.supportsThinking" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#93c5fd]">
                <span class="icon-[mdi--brain] text-[12px]"></span>思考
              </span>
              <span v-if="detectedCapabilities.supportsTools" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#fcd34d]">
                <span class="icon-[mdi--tools] text-[12px]"></span>工具
              </span>
              <span v-if="detectedCapabilities.supportsAudio" class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#c4b5fd]">
                <span class="icon-[mdi--microphone-outline] text-[12px]"></span>音频
              </span>
              <span class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#a3a3a3]">
                上下文 {{ (detectedCapabilities.contextWindowTokens / 1000).toFixed(0) }}K
              </span>
              <span class="inline-flex items-center gap-0.5 rounded-full border border-[#3f3f3f] bg-[#2a2a2a] px-2 py-0.5 text-[11px] text-[#a3a3a3]">
                最大输出 {{ (detectedCapabilities.maxOutputTokens / 1000).toFixed(0) }}K
              </span>
            </div>

            <!-- 图片不支持警告 -->
            <div v-if="detectedCapabilities && detectedCapabilities.supportsVision === false" class="rounded-[6px] border border-yellow-800/40 bg-yellow-900/20 px-3 py-1.5 text-[11px] text-yellow-200">
              ⚠️ 此模型不支持图片输入。发送图片时，后端会自动将图片替换为文字占位说明。
            </div>

            <!-- 读图 MCP 设置：一键为不支持图片的模型添加读图 MCP -->
            <div v-if="detectedCapabilities && detectedCapabilities.supportsVision === false" class="mt-1.5">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2.5 py-1 text-[11px] text-[#93c5fd] transition-colors hover:bg-[#2a2a2a]"
                @click="readerState.open ? (readerState.open = false) : openReaderPanel()"
              >
                <span class="icon-[mdi--image-search-outline] text-[12px]"></span>
                {{ readerState.open ? "收起读图 MCP 设置" : "读图 MCP 设置" }}
              </button>

              <div v-if="readerState.open" class="mt-2 space-y-2 rounded-[6px] border border-[#343434] bg-[#252525] p-3">
                <div class="space-y-1">
                  <span class="text-[11px] text-[#a3a3a3]">视觉网关地址（OpenAI 兼容，参考 vision-luna）</span>
                  <Input :model-value="readerState.url" placeholder="https://your-gateway.example.com/v1" autocomplete="off" @update:model-value="(value) => (readerState.url = value)" />
                </div>
                <div class="space-y-1">
                  <span class="text-[11px] text-[#a3a3a3]">API Key（网关无需鉴权可留空）</span>
                  <Input :model-value="readerState.apiKey" type="password" placeholder="sk-..." @update:model-value="(value) => (readerState.apiKey = value)" />
                </div>
                <div class="space-y-1">
                  <span class="text-[11px] text-[#a3a3a3]">视觉模型名</span>
                  <Input :model-value="readerState.model" placeholder="gpt-5.6-luna" @update:model-value="(value) => (readerState.model = value)" />
                </div>
                <div class="flex flex-wrap items-center gap-2">
                  <Button variant="primary" :disabled="readerState.busy" @click="handleEnableReaderMCP">
                    {{ readerState.busy ? "启用中..." : "启用读图 MCP" }}
                  </Button>
                </div>
                <div v-if="readerState.ok" class="text-[11px] text-[#6ee7a5]">{{ readerState.ok }}</div>
                <div v-if="readerState.error" class="text-[11px] text-[#fca5a5]">{{ readerState.error }}</div>
              </div>
            </div>

            <!-- 快捷档位按钮 -->
            <div class="inline-flex flex-wrap rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]" role="group" aria-label="上下文窗口档位">
              <button
                v-for="tier in CONTEXT_TIERS"
                :key="tier.tokens"
                type="button"
                class="relative rounded-[6px] px-2.5 py-1 font-medium transition-colors"
                :class="[
                  activeContextTier?.tokens === tier.tokens
                    ? 'bg-[#10AD5D]/25 text-[#6ee7a5]'
                    : 'text-[#a3a3a3] hover:text-white',
                ]"
                @click="selectContextTier(tier)"
              >
                {{ tier.label }}
                <span
                  v-if="recommendedContextTier?.tokens === tier.tokens && activeContextTier?.tokens !== tier.tokens"
                  class="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-[#10AD5D]"
                ></span>
              </button>
            </div>

            <input
              v-model="contextWindowTokensInput"
              type="text"
              inputmode="numeric"
              placeholder="例如：200000（留空用默认值）"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
            <div v-if="detectedContextWindow" class="text-xs text-[#6ee7a5]">
              已按模型名识别 {{ detectedContextWindow.tokens.toLocaleString() }} tokens（{{ detectedContextWindow.source }}），可直接覆盖。
            </div>
          </label>

          <div class="rounded-[8px] border border-[#343434] bg-[#252525] p-3 md:col-span-2">
            <div class="flex items-center justify-between gap-3">
              <div>
                <div class="text-sm text-[#d4d4d4]">价格（每百万 token）</div>
                <div class="mt-1 text-xs text-[#8f8f8f]">
                  用于请求明细和首页成本估算；留空时自动使用内置官方价。
                  <span v-if="draft.pricing?.source === 'manual'" class="text-[#6ee7a5]">· 手动配价</span>
                  <span v-else-if="draft.pricing?.source === 'catalog'" class="text-[#6ee7a5]">· 中转站探测价</span>
                  <span v-else-if="draft.pricing?.source" class="text-[#6ee7a5]">· {{ draft.pricing.source }}</span>
                  <span v-else class="text-[#8f8f8f]">· 未配置（将使用内置官方价）</span>
                </div>
              </div>
              <button
                v-if="pricingKnown"
                type="button"
                class="shrink-0 text-xs text-[#fca5a5] hover:text-white"
                @click="clearPricing"
              >
                清除价格
              </button>
            </div>
            <div class="mt-3 grid grid-cols-2 gap-3 md:grid-cols-5">
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">输入</span>
                <input v-model="pricingInput" type="text" inputmode="decimal" placeholder="0.00" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">输出</span>
                <input v-model="pricingOutput" type="text" inputmode="decimal" placeholder="0.00" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">缓存读取</span>
                <input v-model="pricingCacheRead" type="text" inputmode="decimal" placeholder="0.00" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">缓存写入</span>
                <input v-model="pricingCacheWrite" type="text" inputmode="decimal" placeholder="0.00" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">币种</span>
                <input v-model="pricingCurrency" type="text" maxlength="8" placeholder="USD" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm uppercase text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
            </div>
          </div>

          <label v-if="draft.type === 'openai' || draft.type === 'gemini'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.reasoningEffort" />
              <span>推理强度</span>
            </span>
            <Select
              v-model="draft.reasoningEffort"
              :options="reasoningEffortOptions"
            />
          </label>

          <label v-if="draft.type === 'anthropic'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.anthropicThinkingEffort" />
              <span>思考强度</span>
            </span>
            <Select
              v-model="draft.anthropicThinkingEffort"
              :options="anthropicThinkingEffortOptions"
            />
          </label>

        </div>

        <button
          type="button"
          class="center-row w-full justify-between gap-3 rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2 text-left transition-colors hover:border-[#4a4a4a]"
          @click="advancedExpanded = !advancedExpanded"
        >
          <div class="center-row min-w-0 gap-2 text-xs">
            <span class="icon-[mdi--tune-variant] shrink-0 text-[15px] text-[#8f8f8f]"></span>
            <span class="text-[#a3a3a3]">协议</span>
            <span class="truncate text-[#86efac]">{{ draft.protocolMode === PROTOCOL_MODE_FIXED ? '固定' : '自动' }} · {{ effectiveProtocolGroup || "未识别" }}</span>
          </div>
          <span class="center-row shrink-0 gap-1 text-xs text-[#8f8f8f]">
            <span>{{ advancedExpanded ? "收起" : "高级设置" }}</span>
            <span class="icon-[mdi--chevron-right] text-[16px] transition-transform" :class="{ 'rotate-90': advancedExpanded }"></span>
          </span>
        </button>

        <div v-if="advancedExpanded" class="flex flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <label class="flex flex-col gap-1">
              <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                <Tooltip :content="fieldTips.protocolMode" />
                <span>协议选择</span>
              </span>
              <Select v-model="draft.protocolMode" :options="protocolModeOptions" />
            </label>
            <div class="rounded-[8px] border border-[#343434] bg-[#232323] px-3 py-2">
              <div class="text-xs text-[#737373]">当前协议分组</div>
              <div class="mt-1 text-sm text-[#86efac]">{{ effectiveProtocolGroup || "未识别" }}</div>
              <div v-if="draft.protocolMode === PROTOCOL_MODE_AUTO" class="mt-1 text-xs text-[#8f8f8f]">
                根据模型类型、模型名称和接口地址自动归类
              </div>
            </div>
          </div>

          <div v-if="draft.type === 'openai' || draft.type === 'gemini'" class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.maxCompletionTokens" />
              <span>最大输出 Token</span>
            </span>
            <input
              v-model="maxCompletionTokensInput"
              type="text"
              inputmode="numeric"
              placeholder="例如：65536（留空用默认值）"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label v-if="draft.type === 'openai' && /gpt/i.test(draft.modelID || '')" class="center-row justify-between gap-3 rounded-[8px] border border-[#343434] bg-[#232323] px-3 py-2 text-sm text-[#d4d4d4]">
            <span>Fast 模式（priority）</span>
            <input v-model="draft.fastMode" type="checkbox" class="size-4 accent-[#10AD5D]" />
          </label>

          <label v-if="draft.type === 'openai'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.openAIEndpoint" />
              <span>接口端点</span>
            </span>
            <Select
              v-model="draft.openAIEndpoint"
              :options="openAIEndpointOptions"
            />
          </label>

          <label v-if="draft.type === 'openai' && draft.protocolMode === PROTOCOL_MODE_FIXED" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.openAIRequestGroup" />
              <span>固定请求协议</span>
            </span>
            <Select
              v-model="draft.openAIRequestGroup"
              :options="openAIRequestGroupOptions"
            />
          </label>
        </div>

        <div v-if="draft.type === 'openai'" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.openAIExtraParams" />
              <span>额外参数 JSON</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.openAIExtraParamsEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>
          <textarea
            v-if="draft.openAIExtraParamsEnabled"
            v-model="draft.openAIExtraParamsJSON"
            rows="5"
            spellcheck="false"
            class="mt-3 min-h-[120px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </div>

        <label v-if="draft.type === 'anthropic'" class="flex flex-col gap-1">
          <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
            <Tooltip :content="fieldTips.anthropicMaxTokens" />
            <span>最大输出 Token</span>
          </span>
          <input
            v-model="anthropicMaxTokensInput"
            type="text"
            inputmode="numeric"
            placeholder="例如：65536（留空用默认值）"
            class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </label>

        <div v-if="draft.type === 'anthropic'" class="rounded-[8px] border border-[#343434] bg-[#232323] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.anthropicExtraParams" />
              <span>Anthropic 额外参数 JSON</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.anthropicExtraParamsEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>
          <textarea
            v-if="draft.anthropicExtraParamsEnabled"
            v-model="draft.anthropicExtraParamsJSON"
            rows="5"
            spellcheck="false"
            class="mt-3 min-h-[120px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </div>

        <div class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="flex items-center justify-between gap-3">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.customHeaders" />
              <span>自定义请求头 JSON</span>
            </span>
            <label class="center-row gap-2 text-xs text-[#d4d4d4]">
              <input
                v-model="draft.customHeadersEnabled"
                type="checkbox"
                class="size-4 accent-[#10AD5D]"
              />
              <span>启用</span>
            </label>
          </div>
          <textarea
            v-if="draft.customHeadersEnabled"
            v-model="draft.customHeadersJSON"
            rows="5"
            spellcheck="false"
            class="mt-3 min-h-[120px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </div>

        <div class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="mb-2 text-sm text-[#d4d4d4]">余额 / 套餐查询</div>
          <p class="mb-3 text-xs leading-5 text-[#8f8f8f]">
            对齐 cc-switch：自定义、通用模板、New API、Token Plan 与官方余额查询分别使用对应的请求方式。
          </p>
          <label class="mb-3 flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.balanceProfile" />
              <span>查询模板</span>
            </span>
            <Select v-model="draft.balanceProfile" :options="balanceProfileOptions" button-class="h-9 text-sm" />
          </label>

          <div v-if="draft.balanceProfile === 'general'" class="mb-3 text-xs leading-5 text-[#8f8f8f]">
            通用模板请求接口地址下的 <code>/user/balance</code>，使用当前 API Key 读取 balance/remaining。
          </div>
          <div v-if="draft.balanceProfile === 'official'" class="mb-3 text-xs leading-5 text-[#8f8f8f]">
            官方模板按接口地址识别 DeepSeek、StepFun、SiliconFlow、OpenRouter、Novita 等官方余额接口。
          </div>

          <div
            v-if="draft.balanceProfile === 'newapi'"
            class="mb-3 grid grid-cols-1 gap-3 md:grid-cols-2"
          >
            <label class="flex flex-col gap-1">
              <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                <Tooltip :content="fieldTips.balanceAccessToken" />
                <span>访问令牌（New API）</span>
              </span>
              <Input
                v-model="draft.balanceAccessToken"
                type="password"
                allow-visibility-toggle
                placeholder="在个人安全设置里生成"
                autocomplete="off"
              />
            </label>
            <label class="flex flex-col gap-1">
              <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                <Tooltip :content="fieldTips.balanceUserID" />
                <span>用户 ID（New API）</span>
              </span>
              <input
                v-model="draft.balanceUserID"
                type="text"
                placeholder="例如：114514"
                class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
              />
            </label>
            <p class="md:col-span-2 text-xs text-[#8f8f8f]">
              New API 请求：GET 站点根 /api/user/self，Header 带 Bearer 访问令牌与 New-Api-User。
              额度按 quota÷500000 换算为 USD（与 cc-switch 一致）。
            </p>
          </div>

          <div
            v-if="draft.balanceProfile === 'token_plan'"
            class="mb-3 flex flex-col gap-1"
          >
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.balanceCodingPlanProvider" />
              <span>Token Plan 供应商</span>
            </span>
            <Select
              v-model="draft.balanceCodingPlanProvider"
              :options="codingPlanProviderOptions"
              button-class="h-9 text-sm"
            />
            <span class="text-xs text-[#8f8f8f]">
              自动检测：Kimi For Coding、智谱、MiniMax、ZenMux、火山方舟 Coding 入口。智谱团队版须手动选择。
            </span>
          </div>

          <div
            v-if="draft.balanceProfile === 'custom'"
            class="grid grid-cols-1 gap-3 md:grid-cols-2"
          >
            <label class="flex flex-col gap-1">
              <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                <Tooltip :content="fieldTips.balanceQueryURL" />
                <span>查询 URL</span>
              </span>
              <input
                v-model="draft.balanceQueryURL"
                type="text"
                placeholder="例如：{{baseUrl}}/api/user/self"
                class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
              />
            </label>
            <label v-if="draft.balanceProfile === 'custom'" class="flex flex-col gap-1">
              <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
                <Tooltip :content="fieldTips.balanceQueryField" />
                <span>取值字段</span>
              </span>
              <input
                v-model="draft.balanceQueryField"
                type="text"
                placeholder="例如：data.0.total_balance"
                class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
              />
            </label>
          </div>
          <label
            v-if="draft.balanceProfile === 'custom'"
            class="mt-3 flex flex-col gap-1"
          >
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.balanceQueryHeaders" />
              <span>请求头 JSON（可选）</span>
            </span>
            <textarea
              v-model="draft.balanceQueryHeadersJSON"
              rows="4"
              spellcheck="false"
              placeholder='{ "New-Api-User": "{{userId}}" }'
              class="min-h-[96px] w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 py-2 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>
        </div>
        </div>

        <label class="flex flex-col gap-1">
          <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
            <Tooltip :content="fieldTips.tooltipData" />
            <span>备注</span>
          </span>
          <textarea
            v-model="draft.tooltipData"
            rows="3"
            placeholder="例如：用于日常代码补全与问答"
            class="min-h-[96px] resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
        </label>

        <ModelAdapterTestCard
          :result="localTestFailure ? { status: 'error', error: '测试失败', summaryText: '测试失败', rawResponse: modelTestSummary } : activeModelTestResult"
          :stale="modelTestResultStale"
          :show-metrics="true"
          empty-text="尚未测试 — 点击右上角「保存并测试」检测该模型是否可用"
        />

        <div
          v-if="errorMessage"
          class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
        >
          {{ errorMessage }}
        </div>
        </template>
      </div>
    </div>
  </div>
</template>
