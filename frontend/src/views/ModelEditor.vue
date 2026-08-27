<script setup>
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import ModelBehaviorFields from "@/components/model-editor/ModelBehaviorFields.vue";
import ModelCapabilitiesSection from "@/components/model-editor/ModelCapabilitiesSection.vue";
import ModelIdentityFields from "@/components/model-editor/ModelIdentityFields.vue";
import ModelPricingSection from "@/components/model-editor/ModelPricingSection.vue";
import ModelTestResultSection from "@/components/model-editor/ModelTestResultSection.vue";
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { getModelEditorContext } from "@/services/clientApi";
import { popModelEditorReturn, popModelEditorSeed, stashModelEditorReturn } from "@/utils/modelEditorSeed";
import { showModal } from "@/composables/useModal";
import { isModelCovered, resolveModelContextWindow, resolveModelCapabilities } from "@/utils/modelContext";
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
  ANTHROPIC_AUTH_MODE_AUTO,
  appState,
  BALANCE_QUERY_HEADERS_DEFAULT_JSON,
  buildModelAdapterTestRequestHash,
  classifyModelProtocol,
  inferProviderType,
  CUSTOM_HEADERS_DEFAULT_JSON,
  CREDENTIAL_SCOPE_ADAPTER_API_KEY,
  CREDENTIAL_SCOPE_CURSOR_ACCOUNT,
  EXTRA_PARAMS_DEFAULT_JSON,
  getModelAdapterTestResult,
  getModelAdapterTestResultByID,
  isModelAdapterTestResultStale,
  normalizeModelAdapter,
  MODEL_SOURCE_CURSOR_ACCOUNT,
  MODEL_SOURCE_THIRD_PARTY,
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
import { anthropicAuthModeOptions } from "@/utils/anthropicAuthMeta";
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRouter, useRoute } from "vue-router";
// 静态元数据（选项/档位/字段提示/余额头覆盖判定）已归位 utils/modelEditorMeta.js。
import {
  CONTEXT_TIERS,
  anthropicThinkingEffortOptions,
  fieldTips,
  hasBalanceQueryHeadersOverride,
  reasoningEffortOptions,
} from "@/utils/modelEditorMeta";

const createEmptyModelAdapter = () => ({
  id: "",
  source: MODEL_SOURCE_THIRD_PARTY,
  credentialScope: CREDENTIAL_SCOPE_ADAPTER_API_KEY,
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
  anthropicAuthMode: ANTHROPIC_AUTH_MODE_AUTO,
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
const modelSourceOptions = [
  { label: "第三方 API", value: MODEL_SOURCE_THIRD_PARTY, icon: "icon-[mdi--api]" },
  { label: "Cursor 账户", value: MODEL_SOURCE_CURSOR_ACCOUNT, icon: "icon-[mdi--account-key-outline]" },
];
const supplierOptions = computed(() => supplierSelectOptions(draft.supplierID));
const currentSupplierTemplate = computed(() => supplierTemplate(draft.supplierID));
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

const editorIndex = ref(-1);
const router = useRouter();
const route = useRoute();
const draft = reactive(createEmptyModelAdapter());
const errorMessage = ref("");
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

// 备用密钥池：textarea 每行一把密钥，去空、去重、剔除与主密钥重复项；
// 请求时后端按渠道维度轮换，单把密钥限流只冷却该密钥。
const apiKeysPoolText = computed({
  get() {
    return Array.isArray(draft.apiKeys) ? draft.apiKeys.join("\n") : "";
  },
  set(value) {
    const primary = String(draft.apiKey || "").trim();
    const seen = new Set();
    const pool = [];
    for (const line of String(value || "").split(/\r?\n/)) {
      const trimmed = line.trim();
      if (!trimmed || trimmed === primary || seen.has(trimmed)) continue;
      seen.add(trimmed);
      pool.push(trimmed);
    }
    draft.apiKeys = pool;
  },
});
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
const detectedContextWindow = computed(() => resolveModelContextWindow(draft.modelID));
// 已输入模型 ID 但目录未命中 → 未覆盖（区别于空输入时的 null）
const modelCovered = computed(() => !String(draft.modelID || "").trim() || isModelCovered(draft.modelID));

// 上下文窗口快捷档位
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
const isCursorAccountSource = computed(() => draft.source === MODEL_SOURCE_CURSOR_ACCOUNT);
const modelTestResultTitle = computed(() => isCursorAccountSource.value ? "账户模型状态" : "模型测试");
const modelTestResultEmptyText = computed(() => isCursorAccountSource.value
  ? "执行通道待真实协议验证；保存配置不会发起模型调用。"
  : "尚未测试 — 点击右上角「保存并测试」检测该模型是否可用");
const displayedModelTestResult = computed(() => {
  if (isCursorAccountSource.value) {
    return null;
  }
  if (localTestFailure.value) {
    return { status: "error", error: "测试失败", summaryText: "测试失败", rawResponse: modelTestSummary.value };
  }
  return activeModelTestResult.value;
});
const title = computed(() => {
  if (manualAddMode.value) return "手动添加模型";
  return isQuickMode.value ? "快速添加模型" : "编辑模型配置";
});

// 高级设置分区：始终默认收起，用户手动展开。
const advancedExpanded = ref(false);

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

async function loadContext() {
  // 独立窗口已收敛为主窗口路由：优先读 ?index=（负数=新建），
  // 新建时消费 sessionStorage 预填种子；后端窗口上下文仅作旧入口兜底。
  // 从「拉取模型」页返回时，优先回填离开前的草稿（含未保存修改）。
  const returned = popModelEditorReturn();
  if (returned) {
    editorIndex.value = Number.isInteger(returned.editorIndex) ? returned.editorIndex : -1;
    applyAdapterDraft(returned.draft);
    initialDraftSnapshot.value = snapshotOfDraft();
    loading.value = false;
    return;
  }
  const queryIndex = Number(route.query.index);
  if (Number.isInteger(queryIndex)) {
    editorIndex.value = queryIndex;
    if (queryIndex >= 0 && appState.modelAdapters[queryIndex]) {
      applyAdapterDraft(appState.modelAdapters[queryIndex]);
    } else {
      const seed = popModelEditorSeed();
      applyAdapterDraft(seed && Object.keys(seed).length ? seed : {});
    }
    initialDraftSnapshot.value = snapshotOfDraft();
    loading.value = false;
    return;
  }
  try {
    const ctx = await getModelEditorContext();
    editorIndex.value = typeof ctx.index === "number" ? ctx.index : -1;
    const parsed = JSON.parse(ctx.adapterJSON || "{}");
    const normalized = editorIndex.value < 0 && Object.keys(parsed || {}).length === 0
      ? createEmptyModelAdapter()
      : normalizeModelAdapter(parsed);
    Object.assign(draft, normalized);
    draft.balanceProfile = resolveBalanceProfileForAdapter(normalized);
    if (!draft.type) {
      draft.type = "openai";
    }
  } catch (_error) {
    Object.assign(draft, createEmptyModelAdapter());
    draft.type = "openai";
  } finally {
    initialDraftSnapshot.value = snapshotOfDraft();
    loading.value = false;
  }
}

// 草稿快照：用于取消/关闭时检测未保存修改。
const initialDraftSnapshot = ref("");

function applyAdapterDraft(source) {
  const seed = source && Object.keys(source).length ? source : {};
  const normalized = normalizeModelAdapter({ ...createEmptyModelAdapter(), ...seed });
  Object.assign(draft, normalized);
  draft.balanceProfile = resolveBalanceProfileForAdapter(normalized);
  if (!draft.type) {
    draft.type = "openai";
  }
}

function snapshotOfDraft() {
  return JSON.stringify(normalizeModelAdapter(draft));
}

const isDraftDirty = computed(() => {
  if (loading.value || !initialDraftSnapshot.value) return false;
  return snapshotOfDraft() !== initialDraftSnapshot.value;
});

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
  initialDraftSnapshot.value = snapshotOfDraft();
  errorMessage.value = "";
  return {
    ok: true,
    error: "",
    adapter: result.adapter ? normalizeModelAdapter(result.adapter) : normalizeModelAdapter(draft),
  };
}

async function closeEditor() {
  // 编辑器已收敛进主窗口：关闭即返回模型配置页，不再关闭 OS 窗口。
  try {
    if (window.history.state && window.history.state.back) {
      await router.back();
      return;
    }
    await router.push("/model-config");
  } catch (error) {
    errorMessage.value = toUserError(error) || "关闭窗口失败";
  }
}

async function handleSave() {
  const result = await persistDraft();
  if (!result.ok) {
    return;
  }
  await closeEditor();
}

function handleModelSourceChange(source) {
  if (source === MODEL_SOURCE_CURSOR_ACCOUNT) {
    manualAddMode.value = true;
    Object.assign(draft, {
      source: MODEL_SOURCE_CURSOR_ACCOUNT,
      credentialScope: CREDENTIAL_SCOPE_CURSOR_ACCOUNT,
      type: MODEL_SOURCE_CURSOR_ACCOUNT,
      supplierID: "cursor_account",
      baseURL: "",
      apiKey: "",
      protocolMode: PROTOCOL_MODE_AUTO,
      protocolGroup: "",
      openAIEndpoint: "",
      openAIRequestGroup: "",
      openAIExtraParamsEnabled: false,
      openAIExtraParamsJSON: OPENAI_EXTRA_PARAMS_DEFAULT_JSON,
      customHeadersEnabled: false,
      customHeadersJSON: "",
      anthropicExtraParamsEnabled: false,
      anthropicExtraParamsJSON: EXTRA_PARAMS_DEFAULT_JSON,
      modelCatalogURL: "",
      balanceQueryURL: "",
      balanceQueryField: "",
      balanceQueryHeaders: {},
      balanceQueryHeadersJSON: BALANCE_QUERY_HEADERS_DEFAULT_JSON,
      balanceProfile: "none",
      balanceAccessToken: "",
      balanceUserID: "",
      balanceCodingPlanProvider: "",
      tooltipData: draft.tooltipData || "Cursor 账户模型",
    });
    return;
  }
  draft.source = MODEL_SOURCE_THIRD_PARTY;
  draft.credentialScope = CREDENTIAL_SCOPE_ADAPTER_API_KEY;
  handleModelTypeChange("openai");
}

async function handleCancel() {
  if (isDraftDirty.value) {
    const confirmed = await showModal({
      title: "放弃修改",
      content: "当前模型配置有未保存的修改，确定放弃并关闭吗？",
      confirmText: "放弃修改",
      cancelText: "继续编辑",
    });
    if (!confirmed) {
      return;
    }
  }
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
    if (force || !draft.anthropicAuthMode) draft.anthropicAuthMode = ANTHROPIC_AUTH_MODE_AUTO;
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
    if (!draft.anthropicAuthMode) draft.anthropicAuthMode = ANTHROPIC_AUTH_MODE_AUTO;
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
        anthropicAuthMode: draft.anthropicAuthMode || ANTHROPIC_AUTH_MODE_AUTO,
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
  // 暂存当前草稿，返回编辑器时回填未保存的填写内容
  stashModelEditorReturn(normalizeModelAdapter(draft), editorIndex.value);
  await router.push({ path: "/model-catalog" });
}

async function handleTest() {
  if (isCursorAccountSource.value) {
    localTestFailure.value = "Cursor 账户模型执行通道尚待真实协议验证，当前不能测试调用。";
    return;
  }
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
    <div class="flex shrink-0 items-center justify-between border-b border-[#343434] bg-[#1f1f1f] px-4 py-2">
      <h2 class="text-base font-medium text-white">{{ title }}</h2>
      <div class="flex items-center gap-2">
        <Button variant="default" @click="handleCancel">取消</Button>
        <template v-if="manualAddMode || !isQuickMode">
          <Button variant="default" :disabled="isCursorAccountSource || isCurrentConfigTesting || appState.configSaving" @click="handleTest">
            {{ isCursorAccountSource ? "账户通道待验证" : (isCurrentConfigTesting ? "测试中..." : "保存并测试") }}
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

    <div v-else class="min-h-0 flex-1 overflow-y-auto px-4 pb-6">
      <div class="flex w-full flex-col gap-4">
        <div v-if="isQuickMode && !manualAddMode" class="flex flex-col gap-3">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex items-center gap-3">
              <span class="icon-[mdi--flash-outline] shrink-0 text-[18px] text-[#6ee7a5]"></span>
              <div>
                <div class="text-sm font-medium text-[#e5e5e5]">快捷添加</div>
                <div class="mt-0.5 text-xs leading-5 text-[#8f8f8f]">先填写连接信息，再从模型目录批量导入可用模型。</div>
              </div>
            </div>
            <div class="center-row gap-2 text-xs text-[#8f8f8f]">
              <span class="icon-[mdi--information-outline] shrink-0 text-[15px]"></span>
              <span>没有模型列表？适用于不提供 /models 接口的供应商</span>
              <Button variant="default" @click="manualAddMode = true">手动添加</Button>
            </div>
          </div>

          <div class="flex flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#252525] p-3">
            <div>
              <div class="text-sm font-medium text-[#e5e5e5]">供应商与连接</div>
              <div class="mt-0.5 text-xs text-[#8f8f8f]">选择模板会自动带入接口协议和常用模型，也可以手动覆盖。</div>
            </div>
            <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
              <label class="flex flex-col gap-1">
                <span class="text-sm text-[#d4d4d4]">模型来源</span>
                <Select
                  :model-value="draft.source"
                  :options="modelSourceOptions"
                  button-class="h-9 text-sm"
                  @update:model-value="handleModelSourceChange"
                />
              </label>
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
            <label class="flex flex-col gap-1 md:col-span-3">
              <span class="text-sm text-[#d4d4d4]">供应商模板</span>
              <div class="grid max-h-56 grid-cols-2 gap-1.5 overflow-y-auto pr-0.5 sm:grid-cols-4 lg:grid-cols-6">
                <button
                  v-for="option in supplierOptions"
                  :key="option.value"
                  type="button"
                  class="flex min-w-0 items-center gap-1.5 rounded-[6px] border px-2 py-1.5 text-left text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
                  :class="draft.supplierID === option.value
                    ? 'border-[#10AD5D] bg-[#10AD5D]/10 text-white'
                    : 'border-white/10 bg-black/15 text-[#a3a3a3] hover:border-white/25 hover:text-white'"
                  :title="option.label"
                  @click="handleSupplierChange(option.value)"
                >
                  <img
                    v-if="option.iconURL"
                    :src="option.iconURL"
                    :alt="option.label"
                    :class="['size-4 shrink-0 object-contain', option.iconLight ? 'brightness-0 invert' : '']"
                    loading="lazy"
                  />
                  <span class="min-w-0 truncate">{{ option.label }}</span>
                </button>
              </div>
              <span class="text-xs text-[#8f8f8f]">
                选择固定供应商会自动填充接口地址、协议和常用模型；仍可在下方覆盖。
              </span>
            </label>
            <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
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
              <label class="flex flex-col gap-1 md:col-span-1">
                <span class="text-sm text-[#d4d4d4]">备注</span>
                <textarea
                  v-model="draft.tooltipData"
                  rows="2"
                  placeholder="可选，例如：用于日常代码补全"
                  class="resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
                ></textarea>
              </label>
              <label class="flex flex-col gap-1 md:col-span-1">
                <span class="text-sm text-[#d4d4d4]">备用密钥（可选）</span>
                <textarea
                  v-model="apiKeysPoolText"
                  rows="2"
                  placeholder="每行一把，请求自动轮换"
                  autocomplete="off"
                  spellcheck="false"
                  class="resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 font-mono text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
                ></textarea>
              </label>
            </div>
            <div class="flex justify-end">
              <Button
                variant="primary"
                :disabled="!draft.baseURL || !draft.apiKey"
                @click="openCatalogPage"
              >
                拉取模型
              </Button>
            </div>
          </div>
          <div v-if="catalogError" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
            {{ catalogError }}
          </div>
        </div>

        <template v-else>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div :class="isCursorAccountSource ? 'md:col-span-2' : ''" class="rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2.5 text-sm text-[#d4d4d4]">
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">模型来源</span>
              <Select
                :model-value="draft.source"
                :options="modelSourceOptions"
                button-class="h-9 text-sm"
                @update:model-value="handleModelSourceChange"
              />
            </label>
          </div>

          <template v-if="!isCursorAccountSource">
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
                <label class="flex flex-col gap-1">
                  <span class="text-sm text-[#d4d4d4]">供应商</span>
                  <div class="center-row justify-start gap-2">
                    <span class="center-row size-7 shrink-0 justify-center rounded-[6px] bg-[#232323]">
                      <span :class="[providerIcon(draft.type), 'text-[16px]']"></span>
                    </span>
                    <span class="font-medium text-white">{{ providerLabel(draft.type) }}</span>
                  </div>
                </label>
              </template>
            </div>
          </template>
        </div>

        <div v-if="isCursorAccountSource" class="flex flex-col gap-4">
          <div class="rounded-[8px] border border-[#785b26] bg-[#302714] px-3 py-2.5 text-sm leading-6 text-[#ead7a0]">
            当前账户登录仅用于 Plugins、Skills 和 MCP 控制面。Agent 内置模型的真实 Cursor 请求协议尚未完成逐请求验证，因此该来源可以保存，但不会调用第三方 API，也不能测试执行。
          </div>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">显示名称</span>
              <input v-model="draft.displayName" type="text" placeholder="例如：Cursor 账户模型" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
            </label>
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">模型标识</span>
              <input v-model="draft.modelID" type="text" placeholder="按已验证的 Cursor 模型标识填写" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
            </label>
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">用户分组名称</span>
              <input v-model="draft.groupName" type="text" placeholder="可选" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
            </label>
            <label class="flex flex-col gap-1">
              <span class="text-sm text-[#d4d4d4]">备注</span>
              <input v-model="draft.tooltipData" type="text" placeholder="可选" class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
            </label>
          </div>
          <ModelTestResultSection
            :title="modelTestResultTitle"
            :empty-text="modelTestResultEmptyText"
          />
        </div>

        <template v-if="!isCursorAccountSource">
        <div class="text-xs font-medium uppercase tracking-[0.08em] text-[#737373]">基础信息</div>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div class="md:col-span-2 rounded-[8px] border border-[#343434] bg-[#252525] p-3">
            <div class="mb-3">
              <div class="text-sm font-medium text-[#e5e5e5]">供应商与连接</div>
              <div class="mt-0.5 text-xs leading-5 text-[#8f8f8f]">供应商模板、接口地址和访问密钥决定模型请求的入口。</div>
            </div>
            <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="flex flex-col gap-1 md:col-span-2">
            <span class="text-sm text-[#d4d4d4]">供应商模板</span>
            <div class="grid max-h-56 grid-cols-2 gap-1.5 overflow-y-auto pr-0.5 sm:grid-cols-4 lg:grid-cols-6">
              <button
                v-for="option in supplierOptions"
                :key="option.value"
                type="button"
                class="flex min-w-0 items-center gap-1.5 rounded-[6px] border px-2 py-1.5 text-left text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
                :class="draft.supplierID === option.value
                  ? 'border-[#10AD5D] bg-[#10AD5D]/10 text-white'
                  : 'border-white/10 bg-black/15 text-[#a3a3a3] hover:border-white/25 hover:text-white'"
                :title="option.label"
                @click="handleSupplierChange(option.value)"
              >
                <img
                  v-if="option.iconURL"
                  :src="option.iconURL"
                  :alt="option.label"
                  :class="['size-4 shrink-0 object-contain', option.iconLight ? 'brightness-0 invert' : '']"
                  loading="lazy"
                />
                <span class="min-w-0 truncate">{{ option.label }}</span>
              </button>
            </div>
            <span class="text-xs text-[#8f8f8f]">选择固定供应商会自动填充接口地址、协议和常用模型；仍可在下方覆盖。</span>
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
              class="h-9 min-w-0 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
          </label>

          <label class="flex min-w-0 flex-col gap-1">
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

          <label class="flex flex-col gap-1 md:col-span-2">
            <span class="text-sm text-[#d4d4d4]">备用密钥（可选）</span>
            <textarea
              v-model="apiKeysPoolText"
              rows="2"
              placeholder="每行一把，请求自动轮换；单把限流只冷却该密钥"
              autocomplete="off"
              spellcheck="false"
              class="resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 font-mono text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            ></textarea>
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
              class="h-9 min-w-0 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
            <span class="text-xs leading-5 text-[#737373]">留空使用供应商预设地址；手动添加供应商时，请填写返回模型数组的完整地址。</span>
          </label>
            </div>
          </div>

          <div class="md:col-span-2 rounded-[8px] border border-[#343434] bg-[#252525] p-3">
            <div class="mb-3">
              <div class="text-sm font-medium text-[#e5e5e5]">模型标识</div>
              <div class="mt-0.5 text-xs leading-5 text-[#8f8f8f]">用于区分模型、渠道分组和供应商预设。</div>
            </div>
            <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <ModelIdentityFields
            :display-name="draft.displayName"
            :group-name="draft.groupName"
            :model-i-d="draft.modelID"
            :supplier-model-options="supplierModelOptions"
            :supplier-preset-options="supplierPresetOptions"
            :manual-add-mode="manualAddMode"
            :quick-mode="isQuickMode"
            :can-fetch-catalog="Boolean(draft.baseURL && draft.apiKey)"
            :catalog-error="catalogError"
            :field-tips="fieldTips"
            @update:display-name="draft.displayName = $event"
            @update:group-name="draft.groupName = $event"
            @update:model-i-d="draft.modelID = $event"
            @select-preset="handleSupplierPreset"
            @fetch-catalog="openCatalogPage"
          />
          <div class="md:col-span-2 border-t border-[#343434] pt-3">
            <div class="text-sm font-medium text-[#e5e5e5]">模型能力与行为</div>
            <div class="mt-0.5 text-xs leading-5 text-[#8f8f8f]">上下文、图片能力、推理强度和价格会影响模型的实际使用方式。</div>
          </div>

          <ModelCapabilitiesSection
            v-model="contextWindowTokensInput"
            :capabilities="detectedCapabilities"
            :covered="modelCovered"
            :detected-context-window="detectedContextWindow"
            :active-tier="activeContextTier"
            :recommended-tier="recommendedContextTier"
            :context-tiers="CONTEXT_TIERS"
            :field-tips="fieldTips"
            @select-tier="selectContextTier"
            @open-vision-settings="router.push({ path: '/settings', query: { category: 'delegation' } })"
          />

          <ModelPricingSection
            v-model:input="pricingInput"
            v-model:output="pricingOutput"
            v-model:cache-read="pricingCacheRead"
            v-model:cache-write="pricingCacheWrite"
            v-model:currency="pricingCurrency"
            :known="pricingKnown"
            :source="draft.pricing?.source || ''"
            @clear="clearPricing"
          />

          <ModelBehaviorFields
            :type="draft.type"
            :max-completion-tokens="maxCompletionTokensInput"
            :anthropic-max-tokens="anthropicMaxTokensInput"
            :reasoning-effort="draft.reasoningEffort"
            :anthropic-thinking-effort="draft.anthropicThinkingEffort"
            :reasoning-effort-options="reasoningEffortOptions"
            :anthropic-thinking-effort-options="anthropicThinkingEffortOptions"
            :field-tips="fieldTips"
            @update:max-completion-tokens="maxCompletionTokensInput = $event"
            @update:anthropic-max-tokens="anthropicMaxTokensInput = $event"
            @update:reasoning-effort="draft.reasoningEffort = $event"
            @update:anthropic-thinking-effort="draft.anthropicThinkingEffort = $event"
          />

        </div>
        </div>

        <div class="overflow-hidden rounded-[8px] border border-[#343434] bg-[#252525]">
        <button
          type="button"
          class="center-row w-full justify-between gap-3 px-3 py-2 text-left transition-colors hover:bg-white/5 focus:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-[#10AD5D]"
          :aria-expanded="advancedExpanded"
          aria-controls="model-editor-advanced-settings"
          @click="advancedExpanded = !advancedExpanded"
        >
          <div class="center-row min-w-0 gap-2 text-xs">
            <span class="icon-[mdi--tune-variant] shrink-0 text-[15px] text-[#8f8f8f]"></span>
            <span class="text-[#a3a3a3]">协议与高级设置</span>
            <span class="truncate text-[#86efac]">{{ draft.protocolMode === PROTOCOL_MODE_FIXED ? '固定' : '自动' }} · {{ effectiveProtocolGroup || "未识别" }}</span>
            <span
              v-if="hasAdvancedOverrides"
              class="shrink-0 rounded-full border border-[#3f6f52] bg-[#173322] px-1.5 py-0.5 text-[10px] text-[#86efac]"
            >
              已覆盖
            </span>
          </div>
          <span class="center-row shrink-0 gap-1 text-xs text-[#8f8f8f]">
            <span>{{ advancedExpanded ? "收起" : "展开" }}</span>
            <span class="icon-[mdi--chevron-right] text-[16px] transition-transform" :class="{ 'rotate-90': advancedExpanded }"></span>
          </span>
        </button>

        <div
          v-if="advancedExpanded"
          id="model-editor-advanced-settings"
          class="flex flex-col gap-3 border-t border-[#343434] p-3"
        >
          <div>
            <div class="text-sm font-medium text-[#e5e5e5]">协议与高级参数</div>
            <div class="mt-0.5 text-xs leading-5 text-[#8f8f8f]">仅在需要覆盖自动识别、请求参数、请求头或余额策略时展开。</div>
          </div>
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

        <div v-if="draft.type === 'anthropic'" class="rounded-[8px] border border-[#343434] bg-[#232323] p-3">
          <label class="mb-3 flex flex-col gap-1">
            <span class="text-sm text-[#d4d4d4]">Anthropic 鉴权模式</span>
            <Select v-model="draft.anthropicAuthMode" :options="anthropicAuthModeOptions" />
            <span class="text-xs text-[#a3a3a3]">自定义 Authorization 或 X-Api-Key 请求头会完全接管鉴权，并抑制自动生成的鉴权头。</span>
          </label>
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

        <ModelTestResultSection
          :result="displayedModelTestResult"
          :stale="!isCursorAccountSource && modelTestResultStale"
          :error="errorMessage"
          :title="modelTestResultTitle"
          :empty-text="modelTestResultEmptyText"
        />
        </div>
        </template>
        </template>
      </div>
    </div>
  </div>
</template>
