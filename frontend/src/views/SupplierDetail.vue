<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  createEmptyModelAdapter,
  deleteModelAdapterAt,
  deleteModelAdaptersBatch,
  getModelAdapterTestResultByID,
  openModelEditorWindow,
  reloadUserConfig,
  runModelAdapterTest,
  saveModelAdaptersBatch,
  startModelAdapterTest,
  resolveBalanceProfileForAdapter,
  syncBalanceConfigToSameURL,
  toUserError,
  updateModelAdaptersBySupplier,
} from "@/state/appState";
import { fetchModelCatalog, queryProviderBalance } from "@/services/clientApi";
import { useModelProbe } from "@/composables/useModelProbe";
import { providerIcon, providerSelectOptions } from "@/utils/providerMeta";
import { supplierSelectOptions } from "@/utils/supplierCatalog";
import {
  SUPPLIER_GROUP_MODE_CONNECTION,
  SUPPLIER_GROUP_MODE_NAME,
  adapterMatchesSupplierIdentity,
  displayGroupName,
  normalizeSupplierBaseURL,
  supplierIdentityFromRouteQuery,
} from "@/utils/supplierGrouping";
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const OPENAI_ENDPOINT_RESPONSES = "/v1/responses";
const OPENAI_ENDPOINT_CHAT = "/v1/chat/completions";
const PROTOCOL_GROUP_ANTHROPIC_MESSAGES = "messages";
const PROTOCOL_GROUP_GEMINI_NATIVE = "gemini_native";

const route = useRoute();
const router = useRouter();

const supplierIdentity = computed(() => supplierIdentityFromRouteQuery(route.query));
const queryBaseURL = computed(() => String(route.query.baseURL || "").trim());
const queryGroupName = computed(() => String(route.query.groupName || "").trim());

const title = computed(() => {
  const mode = supplierIdentity.value.mode;
  if (mode === SUPPLIER_GROUP_MODE_CONNECTION) {
    try {
      const host = new URL(normalizeSupplierBaseURL(queryBaseURL.value) || queryBaseURL.value).host;
      return host || queryBaseURL.value || "连接";
    } catch {
      return queryBaseURL.value || "连接";
    }
  }
  if (mode === SUPPLIER_GROUP_MODE_NAME) {
    return displayGroupName(queryGroupName.value);
  }
  return displayGroupName(queryGroupName.value);
});
const subtitle = computed(() => {
  const mode = supplierIdentity.value.mode;
  if (mode === SUPPLIER_GROUP_MODE_NAME) {
    const hosts = [
      ...new Set(
        supplierAdapters.value.map((a) => {
          try {
            return new URL(String(a.baseURL || "").trim()).host;
          } catch {
            return String(a.baseURL || "").trim();
          }
        }).filter(Boolean),
      ),
    ];
    if (hosts.length === 0) return "-";
    if (hosts.length === 1) return hosts[0];
    return `${hosts.length} 个连接`;
  }
  if (mode === SUPPLIER_GROUP_MODE_CONNECTION) {
    const names = [
      ...new Set(
        supplierAdapters.value.map((a) => displayGroupName(a.groupName)),
      ),
    ];
    if (names.length <= 1) return names[0] || "默认分组";
    return `${names.length} 个名称`;
  }
  return queryBaseURL.value;
});

function formatHost(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  try { return new URL(text).host || text; } catch { return text.replace(/^https?:\/\//, ""); }
}
function maskSecret(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  if (text.length <= 8) return `${"*".repeat(Math.max(text.length - 2, 0))}${text.slice(-2)}`;
  return `${text.slice(0, 4)}****${text.slice(-4)}`;
}

function formatOpenAIRequestGroup(group, endpoint) {
  const normalizedGroup = String(group || "").trim();
  if (normalizedGroup === "responses") return "Responses";
  if (normalizedGroup === "chat_completions") return "Chat Completions";
  if (normalizedGroup === "chat_completions_compat") return "Chat Completions / Compat";
  return String(endpoint || "").trim() === OPENAI_ENDPOINT_RESPONSES ? "Responses" : "Chat Completions";
}

function defaultOpenAIRequestGroup(endpoint) {
  return String(endpoint || "").trim() === OPENAI_ENDPOINT_RESPONSES ? "responses" : "chat_completions";
}

function resolvedOpenAIEndpoint(adapter) {
  return String(adapter?.openAIEndpoint || "").trim() || OPENAI_ENDPOINT_RESPONSES;
}

function resolvedOpenAIRequestGroup(adapter) {
  return String(adapter?.protocolGroup || adapter?.openAIRequestGroup || "").trim() || defaultOpenAIRequestGroup(resolvedOpenAIEndpoint(adapter));
}

function transportBadgeText(adapter) {
  if (adapter?.type !== "openai") return "";
  return `${resolvedOpenAIEndpoint(adapter)} · ${formatOpenAIRequestGroup(resolvedOpenAIRequestGroup(adapter), resolvedOpenAIEndpoint(adapter))}`;
}

// 该供应商下的模型（按路由 mode：name / connection / legacy）
const supplierAdapters = computed(() =>
  appState.modelAdapters.filter((adapter) =>
    adapterMatchesSupplierIdentity(adapter, supplierIdentity.value),
  ),
);

// 搜索 / 过滤 / 排序
const modelSearch = ref("");
const statusFilter = ref("all"); // all | ok | fail | untested
const sortMode = ref("name"); // name | speed | recent

const statusFilterOptions = [
  { value: "all", label: "全部" },
  { value: "ok", label: "可用" },
  { value: "fail", label: "失败" },
  { value: "untested", label: "未测" },
];
const sortModeOptions = [
  { value: "name", label: "名称" },
  { value: "speed", label: "速度 t/s" },
  { value: "recent", label: "最近测试" },
];

function adapterHealth(adapter) {
  const result = getModelAdapterTestResultByID(adapter?.id);
  if (!result || !result.status) return "untested";
  if (result.status === "success") return "ok";
  if (result.status === "error") return "fail";
  return "untested"; // running 等归为未测
}

function adapterSpeed(adapter) {
  const result = getModelAdapterTestResultByID(adapter?.id);
  const val = Number(result?.tokensPerSecond ?? 0);
  return Number.isFinite(val) ? val : 0;
}

function adapterTestedAt(adapter) {
  const result = getModelAdapterTestResultByID(adapter?.id);
  const t = Date.parse(result?.testedAt || "");
  return Number.isFinite(t) ? t : 0;
}

const healthStats = computed(() => {
  let ok = 0;
  let fail = 0;
  let untested = 0;
  for (const adapter of supplierAdapters.value) {
    const h = adapterHealth(adapter);
    if (h === "ok") ok += 1;
    else if (h === "fail") fail += 1;
    else untested += 1;
  }
  return { ok, fail, untested, total: supplierAdapters.value.length };
});

const visibleAdapters = computed(() => {
  const q = modelSearch.value.trim().toLowerCase();
  let list = supplierAdapters.value.filter((adapter) => {
    if (statusFilter.value !== "all" && adapterHealth(adapter) !== statusFilter.value) {
      return false;
    }
    if (!q) return true;
    const hay = `${adapter.displayName || ""} ${adapter.modelID || ""} ${adapter.baseURL || ""}`.toLowerCase();
    return hay.includes(q);
  });
  list = [...list];
  if (sortMode.value === "name") {
    list.sort((a, b) =>
      String(a.displayName || a.modelID || "").localeCompare(String(b.displayName || b.modelID || ""), "zh-CN"),
    );
  } else if (sortMode.value === "speed") {
    list.sort((a, b) => adapterSpeed(b) - adapterSpeed(a));
  } else if (sortMode.value === "recent") {
    list.sort((a, b) => adapterTestedAt(b) - adapterTestedAt(a));
  }
  return list;
});

// 第一个模型的 type/apiKey/customHeaders，用于拉取新模型；混合供应商分组不传具名 supplierID，避免误触发官方专用接口。
const supplierMeta = computed(() => {
  const first = supplierAdapters.value[0];
  if (!first) return null;
  const supplierIDs = new Set(
    supplierAdapters.value
      .map((adapter) => String(adapter.supplierID || "custom").trim().toLowerCase() || "custom"),
  );
  return {
    ...first,
    supplierID: supplierIDs.size === 1 ? first.supplierID : "custom",
  };
});

// 余额/额度查询（非阻塞，失败不影响页面）
// data：当前用于展示的余额（成功值，或瞬时失败时保留的上次成功值）。
// stale：为 true 表示 data 是被保留的上次成功值，当前查询是瞬时失败（数据可能过期）。
const balanceState = reactive({ loading: false, loaded: false, data: null, stale: false });

function currencySymbol(currency) {
  const code = String(currency || "").toUpperCase();
  if (code === "USD") return "$";
  if (code === "CNY" || code === "RMB") return "¥";
  if (code === "EUR") return "€";
  return "";
}

function formatMoney(value, currency) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  const symbol = currencySymbol(currency);
  const num = Number(value).toFixed(2);
  return symbol ? `${symbol}${num}` : `${num} ${String(currency || "").toUpperCase()}`.trim();
}

function balanceSourceLabel(source) {
  if (source === "openai_billing") return "openai billing";
  if (source === "sub2api_usage") return "sub2api usage";
  if (source === "newapi") return "New API";
  if (source === "token_plan") return "Token Plan";
  if (source === "configured") return "自定义查询";
  if (source === "deepseek") return "DeepSeek";
  if (source === "stepfun") return "阶跃星辰";
  if (source === "siliconflow") return "SiliconFlow";
  if (source === "openrouter") return "OpenRouter";
  if (source === "novita") return "Novita";
  if (source === "moonshot") return "Moonshot / Kimi";
  return String(source || "").trim();
}

const balanceSecondary = computed(() => {
  const data = balanceState.data;
  if (!data || !data.supported) return "";
  const source = balanceSourceLabel(data.source);
  const hasUsedTotal =
    (data.used != null && Number.isFinite(Number(data.used))) ||
    (data.total != null && Number.isFinite(Number(data.total)));
  let text = "";
  if (hasUsedTotal) {
    text = `已用 ${formatMoney(data.used, data.currency)} / 总额 ${formatMoney(data.total, data.currency)}`;
  }
  if (source) text += `${text ? " · " : ""}来源: ${source}`;
  if (balanceState.stale) text += `${text ? " · " : ""}数据可能过期`;
  return text;
});

// 主展示文案：有 total/used 时形如「余额 R / 总额 T / 已用 U」，否则回退「余额 R」。
const balancePrimary = computed(() => {
  const data = balanceState.data;
  if (!data || !data.supported) return "";
  if (data.unlimited) return "余额 不限额";
  if (data.source === "token_plan" || data.currency === "%") {
    const used = data.used != null && Number.isFinite(Number(data.used)) ? Number(data.used) : null;
    const remaining = data.remaining != null && Number.isFinite(Number(data.remaining)) ? Number(data.remaining) : null;
    const plan = String(data.planName || "").trim();
    const head = plan ? `${plan} · ` : "";
    if (used != null) return `${head}已用 ${used.toFixed(0)}%` + (remaining != null ? ` / 剩余 ${remaining.toFixed(0)}%` : "");
    if (remaining != null) return `${head}剩余 ${remaining.toFixed(0)}%`;
  }
  const parts = [`余额 ${formatMoney(data.remaining, data.currency)}`];
  if (data.total != null && Number.isFinite(Number(data.total))) {
    parts.push(`总额 ${formatMoney(data.total, data.currency)}`);
  }
  if (data.used != null && Number.isFinite(Number(data.used))) {
    parts.push(`已用 ${formatMoney(data.used, data.currency)}`);
  }
  if (data.planName) parts.unshift(String(data.planName));
  return parts.join(" / ");
});

async function loadBalance(forceRefresh = false) {
  const meta = supplierMeta.value;
  if (!meta || balanceState.loading) return;
  balanceState.loading = true;
  try {
    const request = {
      type: meta.type,
      supplierID: meta.supplierID,
      baseURL: meta.baseURL || queryBaseURL.value,
      apiKey: meta.apiKey,
    };
    if (forceRefresh) request.forceRefresh = true;
    const result = await queryProviderBalance(request);
    const normalized = result || { supported: false, message: "无返回结果" };
    if (normalized.supported) {
      // 成功：更新展示值，清除过期标记。
      balanceState.data = normalized;
      balanceState.stale = false;
    } else if (normalized.transient && balanceState.data && balanceState.data.supported) {
      // 瞬时失败且已有上次成功值：保留旧值（keep-last-good），仅标记数据可能过期。
      balanceState.stale = true;
    } else {
      // 确定性失败（或从未成功过）：清空为不可用。
      balanceState.data = normalized;
      balanceState.stale = false;
    }
  } catch (_e) {
    // invoke 层异常按瞬时处理：有上次成功值则保留，否则置不可用。
    if (balanceState.data && balanceState.data.supported) {
      balanceState.stale = true;
    } else {
      balanceState.data = { supported: false, message: "查询失败" };
      balanceState.stale = false;
    }
  } finally {
    balanceState.loading = false;
    balanceState.loaded = true;
  }
}

// 供应商元信息就绪后自动查询一次（首挂载时 appState 可能尚未填充）
watch(supplierMeta, (meta, prev) => {
  if (meta && !prev && !balanceState.loaded && !balanceState.loading) void loadBalance();
});

// catalog 状态（拉取远程模型列表）
const catalogLoading = ref(false);
const catalogSaving = ref(false);
const catalogError = ref("");
const catalogModels = ref([]); // [{ id, contextWindowTokens, pricing }]
const selectedCatalogModels = ref(new Set());

// 可用性探测
const catalogProbe = useModelProbe();

function protocolGroupForType(type) {
  if (type === "anthropic") return PROTOCOL_GROUP_ANTHROPIC_MESSAGES;
  if (type === "gemini") return PROTOCOL_GROUP_GEMINI_NATIVE;
  return "";
}

function buildProbeAdapter(model) {
  if (!supplierMeta.value) return { ...createEmptyModelAdapter(), modelID: model.id, tooltipData: `探测 ${model.id}` };
  return {
    ...createEmptyModelAdapter(),
    type: supplierMeta.value.type,
    supplierID: supplierMeta.value.supplierID,
    baseURL: supplierMeta.value.baseURL || queryBaseURL.value,
    apiKey: supplierMeta.value.apiKey,
    customHeadersEnabled: Boolean(supplierMeta.value.customHeadersEnabled),
    customHeadersJSON: supplierMeta.value.customHeadersJSON || "",
    displayName: model.id,
    modelID: model.id,
    tooltipData: `探测 ${model.id}`,
    protocolMode: "auto",
    protocolGroup: protocolGroupForType(supplierMeta.value.type),
    openAIEndpoint: supplierMeta.value.type === "openai" ? OPENAI_ENDPOINT_RESPONSES : "",
    anthropicThinkingEffort: supplierMeta.value.type === "anthropic" ? "xhigh" : "",
  };
}

async function handleProbeCatalog() {
  if (!catalogModels.value.length) return;
  await catalogProbe.probeAll(catalogModels.value, buildProbeAdapter, { concurrency: 3 });
  // 探测完成后仅保留可用模型的勾选
  const next = new Set();
  for (const model of catalogModels.value) {
    if (catalogProbe.statusOf(model.id) === "ok") {
      next.add(catalogSelectionKey(model.id));
    }
  }
  selectedCatalogModels.value = next;
}

function catalogSelectionKey(modelID) {
  const base =
    supplierMeta.value?.baseURL ||
    queryBaseURL.value ||
    "";
  return `${base}::${modelID}`;
}
function isCatalogModelSelected(modelID) {
  return selectedCatalogModels.value.has(catalogSelectionKey(modelID));
}
function toggleCatalogModel(modelID) {
  const key = catalogSelectionKey(modelID);
  const next = new Set(selectedCatalogModels.value);
  if (next.has(key)) next.delete(key); else next.add(key);
  selectedCatalogModels.value = next;
}
const selectedCatalogCount = computed(() =>
  catalogModels.value.reduce((acc, m) => acc + (isCatalogModelSelected(m.id) ? 1 : 0), 0),
);
const allCatalogSelected = computed(
  () => catalogModels.value.length > 0 && catalogModels.value.every((m) => isCatalogModelSelected(m.id)),
);

function toggleAllCatalogModels() {
  if (allCatalogSelected.value) {
    selectedCatalogModels.value = new Set();
  } else {
    selectedCatalogModels.value = new Set(catalogModels.value.map((m) => catalogSelectionKey(m.id)));
  }
}

async function handleFetchModels() {
  catalogError.value = "";
  if (!supplierMeta.value) {
    catalogError.value = "当前供应商没有已有模型，无法确定拉取参数";
    return;
  }
  catalogLoading.value = true;
  catalogProbe.reset();
  try {
    const result = await fetchModelCatalog({
      type: supplierMeta.value.type,
      supplierID: supplierMeta.value.supplierID,
      baseURL: supplierMeta.value.baseURL || queryBaseURL.value,
      apiKey: supplierMeta.value.apiKey,
      customHeadersEnabled: Boolean(supplierMeta.value.customHeadersEnabled),
      customHeadersJSON: supplierMeta.value.customHeadersJSON || "",
    });
    const fetched = Array.isArray(result?.models) ? result.models : [];
    if (!fetched.length) {
      catalogError.value = "服务未返回可用模型";
      catalogModels.value = [];
      return;
    }
    catalogModels.value = fetched;
    selectedCatalogModels.value = new Set(fetched.map((m) => catalogSelectionKey(m.id)));
  } catch (error) {
    catalogModels.value = [];
    catalogError.value = toUserError(error);
  } finally {
    catalogLoading.value = false;
  }
}

async function handleBatchAddModels() {
  catalogError.value = "";
  if (!supplierMeta.value) return;
  const selected = catalogModels.value.filter((m) => isCatalogModelSelected(m.id));
  if (!selected.length) {
    catalogError.value = "请至少选择一个模型";
    return;
  }
  catalogSaving.value = true;
  try {
    const seedBaseURL = supplierMeta.value.baseURL || queryBaseURL.value;
    const seedGroupName =
      supplierIdentity.value.mode === SUPPLIER_GROUP_MODE_NAME
        ? queryGroupName.value
        : String(supplierMeta.value.groupName || queryGroupName.value || "").trim();
    const adapters = selected.map((model) => ({
      ...createEmptyModelAdapter(),
      type: supplierMeta.value.type,
      supplierID: supplierMeta.value.supplierID,
      baseURL: seedBaseURL,
      apiKey: supplierMeta.value.apiKey,
      customHeadersEnabled: Boolean(supplierMeta.value.customHeadersEnabled),
      customHeadersJSON: supplierMeta.value.customHeadersJSON || "",
      displayName: model.id,
      modelID: model.id,
      groupName: seedGroupName,
      tooltipData: `来自 ${formatHost(seedBaseURL)}`,
      contextWindowTokens: model.contextWindowTokens || 0,
      pricing: model.pricing || null,
      protocolMode: "auto",
      protocolGroup: protocolGroupForType(supplierMeta.value.type),
      openAIEndpoint: supplierMeta.value.type === "openai" ? OPENAI_ENDPOINT_RESPONSES : "",
      openAIRequestGroup: "",
      anthropicThinkingEffort: supplierMeta.value.type === "anthropic" ? "xhigh" : "",
    }));
    const result = await saveModelAdaptersBatch(adapters);
    if (!result.ok) {
      catalogError.value = result.error || "批量添加失败";
      return;
    }
    // 清空选择列表
    catalogModels.value = [];
    selectedCatalogModels.value = new Set();
    catalogProbe.reset();
    await reloadUserConfig({ modelAdaptersOnly: true });
  } catch (error) {
    catalogError.value = toUserError(error);
  } finally {
    catalogSaving.value = false;
  }
}

function groupIndex(adapter) {
  return appState.modelAdapters.indexOf(adapter);
}

async function openEditor(adapter) {
  const index = adapter ? groupIndex(adapter) : -1;
  const seed = supplierMeta.value || createEmptyModelAdapter();
  const draft = adapter
    ? { ...adapter }
    : { ...createEmptyModelAdapter(), ...seed, id: "", displayName: "", modelID: "" };
  try {
    await openModelEditorWindow(index, draft);
  } catch (error) {
    await showModal({ title: "打开失败", content: String(error || "操作失败").trim() });
  }
}

async function deleteAdapter(adapter) {
  const index = groupIndex(adapter);
  if (index < 0) return;
  const confirmed = await showModal({
    title: "删除模型",
    content: `确定删除「${adapter.displayName || adapter.modelID}」吗？`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  const result = await deleteModelAdapterAt(index);
  if (!result.ok) {
    await showModal({ title: "删除失败", content: String(result.error || "操作失败").trim() });
  }
}

async function duplicateAdapter(adapter) {
  const draft = { ...adapter, id: "", displayName: `${adapter.displayName || adapter.modelID}-副本` };
  try {
    await openModelEditorWindow(-1, draft);
  } catch (error) {
    await showModal({ title: "打开失败", content: String(error || "操作失败").trim() });
  }
}

function testResult(adapter) { return getModelAdapterTestResultByID(adapter?.id); }
function isTesting(adapter) { return testResult(adapter)?.status === "running"; }
async function testAdapter(adapter) {
  try {
    await runModelAdapterTest(adapter);
    await reloadUserConfig({ modelAdaptersOnly: true });
  } catch (_e) {
    /* card shows result */
  }
}

// 批量测试进度
const batchTesting = ref(false);
// 取消标志：worker 每轮循环检查，置位后不再从队列取新任务（已在途的请求会自然结束）
const batchCancelled = ref(false);
const batchProgress = computed(() => {
  const total = supplierAdapters.value.length;
  if (total === 0) return { total: 0, done: 0, pct: 0 };
  const done = supplierAdapters.value.filter((a) => {
    const r = testResult(a);
    return r && r.status !== "running";
  }).length;
  return { total, done, pct: Math.round((done / total) * 100) };
});

async function testAllAdapters() {
  if (batchTesting.value || supplierAdapters.value.length === 0) return;
  batchTesting.value = true;
  batchCancelled.value = false;
  try {
    // 并发上限 3，避免打满上游
    const queue = [...supplierAdapters.value];
    const concurrency = 3;
    const workers = Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
      while (queue.length > 0) {
        if (batchCancelled.value) break;
        const adapter = queue.shift();
        if (!adapter) break;
        try { await runModelAdapterTest(adapter); } catch (_e) { /* ignore */ }
      }
    });
    await Promise.allSettled(workers);
    await reloadUserConfig({ modelAdaptersOnly: true });
  } finally {
    batchTesting.value = false;
  }
}

function cancelBatchTest() {
  // 置位取消标志：worker 不再从队列领取新任务，已在途请求完成后整体停止
  batchCancelled.value = true;
  batchTesting.value = false;
}

// 多选批量操作
const selectionMode = ref(false);
const selectedAdapterIDs = ref(new Set());

function adapterSelectionKey(adapter) {
  return adapter?.id || `${adapter?.baseURL}-${adapter?.modelID}`;
}
function isAdapterSelected(adapter) {
  return selectedAdapterIDs.value.has(adapterSelectionKey(adapter));
}
function toggleAdapterSelection(adapter) {
  const key = adapterSelectionKey(adapter);
  const next = new Set(selectedAdapterIDs.value);
  if (next.has(key)) next.delete(key); else next.add(key);
  selectedAdapterIDs.value = next;
}
const allVisibleSelected = computed(
  () => visibleAdapters.value.length > 0 && visibleAdapters.value.every((a) => isAdapterSelected(a)),
);
function toggleSelectAllVisible() {
  if (allVisibleSelected.value) {
    selectedAdapterIDs.value = new Set();
  } else {
    selectedAdapterIDs.value = new Set(visibleAdapters.value.map((a) => adapterSelectionKey(a)));
  }
}
const selectedAdapters = computed(() =>
  supplierAdapters.value.filter((a) => isAdapterSelected(a)),
);
function toggleSelectionMode() {
  selectionMode.value = !selectionMode.value;
  if (!selectionMode.value) selectedAdapterIDs.value = new Set();
}

async function testSelectedAdapters() {
  const targets = selectedAdapters.value;
  if (batchTesting.value || targets.length === 0) return;
  batchTesting.value = true;
  batchCancelled.value = false;
  try {
    const queue = [...targets];
    const concurrency = 3;
    const workers = Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
      while (queue.length > 0) {
        if (batchCancelled.value) break;
        const adapter = queue.shift();
        if (!adapter) break;
        try { await runModelAdapterTest(adapter); } catch (_e) { /* ignore */ }
      }
    });
    await Promise.allSettled(workers);
    await reloadUserConfig({ modelAdaptersOnly: true });
  } finally {
    batchTesting.value = false;
  }
}

async function deleteSelectedAdapters() {
  const targets = selectedAdapters.value;
  if (targets.length === 0) return;
  const confirmed = await showModal({
    title: "删除选中模型",
    content: `确定删除选中的 ${targets.length} 个模型吗？此操作不可撤销。`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  await removeAdapters(targets);
  selectedAdapterIDs.value = new Set();
}

async function deleteFailedAdapters() {
  const targets = supplierAdapters.value.filter((a) => adapterHealth(a) === "fail");
  if (targets.length === 0) return;
  const confirmed = await showModal({
    title: "删除失败模型",
    content: `确定删除 ${targets.length} 个测试失败的模型吗？此操作不可撤销。`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  await removeAdapters(targets);
}

async function removeAdapters(targets) {
  const list = Array.isArray(targets) ? targets.filter(Boolean) : [];
  if (list.length === 0) return;
  // 一次性原子删除，避免逐个删除的多次落盘与中途失败的半删状态
  const result = await deleteModelAdaptersBatch(list);
  if (!result.ok) {
    await showModal({ title: "删除失败", content: String(result.error || "操作失败").trim() });
  }
  await reloadUserConfig({ modelAdaptersOnly: true });
}

onMounted(async () => {
  await reloadUserConfig({ modelAdaptersOnly: true }).catch(() => {});
  if (route.query.edit === "1" && !bulkEditExpanded.value) {
    toggleBulkEdit();
  }
  if (supplierMeta.value && !balanceState.loaded && !balanceState.loading) void loadBalance();
});

// ─── 批量编辑供应商配置 ─────────────────────────────────────────────────────
const bulkEditExpanded = ref(false);
const bulkEditSaving = ref(false);
const bulkEditError = ref("");
const bulkEditConflicts = ref([]);
const balanceTestState = reactive({ loading: false, data: null });
const balanceSyncState = reactive({ loading: false, message: "", ok: false });

function createBulkEditDraft() {
  const first = supplierAdapters.value[0] || {};
  return {
    type: first.type || "openai",
    supplierID: first.supplierID || "",
    baseURL: first.baseURL || "",
    apiKey: first.apiKey || "",
    groupName: first.groupName || "",
    tooltipData: first.tooltipData || "",
    protocolMode: first.protocolMode || "auto",
    openAIEndpoint: first.openAIEndpoint || "",
    customHeadersEnabled: first.customHeadersEnabled || false,
    customHeadersJSON: first.customHeadersJSON || "",
    balanceQueryURL: first.balanceQueryURL || "",
    balanceQueryField: first.balanceQueryField || "",
    balanceQueryHeadersJSON: first.balanceQueryHeadersJSON || "",
    balanceProfile: resolveBalanceProfileForAdapter(first),
    balanceAccessToken: first.balanceAccessToken || "",
    balanceUserID: first.balanceUserID || "",
    balanceCodingPlanProvider: first.balanceCodingPlanProvider || "",
  };
}
const bulkEditDraft = reactive(createBulkEditDraft());

function toggleBulkEdit() {
  bulkEditExpanded.value = !bulkEditExpanded.value;
  if (bulkEditExpanded.value) {
    Object.assign(bulkEditDraft, createBulkEditDraft());
    bulkEditError.value = "";
    bulkEditConflicts.value = [];
    balanceTestState.data = null;
    balanceSyncState.message = "";
    balanceSyncState.ok = false;
  }
}

function parseBalanceHeaders(value) {
  const text = String(value || "").trim();
  if (!text) return undefined;
  try {
    const parsed = JSON.parse(text);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      throw new Error("请求头必须是 JSON 对象");
    }
    return parsed;
  } catch (error) {
    throw new Error(`查询请求头格式错误：${error?.message || "请输入 JSON 对象"}`);
  }
}

async function testBulkEditBalance() {
  if (balanceTestState.loading) return;
  balanceTestState.loading = true;
  balanceTestState.data = null;
  try {
    const request = {
      type: String(bulkEditDraft.type || "").trim(),
      supplierID: String(bulkEditDraft.supplierID || "").trim(),
      baseURL: String(bulkEditDraft.baseURL || "").trim(),
      apiKey: String(bulkEditDraft.apiKey || "").trim(),
      forceRefresh: true,
      balanceProfile: String(bulkEditDraft.balanceProfile || "").trim(),
      balanceAccessToken: String(bulkEditDraft.balanceAccessToken || "").trim(),
      balanceUserID: String(bulkEditDraft.balanceUserID || "").trim(),
      balanceCodingPlanProvider: String(bulkEditDraft.balanceCodingPlanProvider || "").trim(),
      balanceQueryURL: String(bulkEditDraft.balanceQueryURL || "").trim(),
      balanceQueryField: String(bulkEditDraft.balanceQueryField || "").trim(),
    };
    const headers = parseBalanceHeaders(bulkEditDraft.balanceQueryHeadersJSON);
    if (headers) request.balanceQueryHeaders = headers;
    const result = await queryProviderBalance(request);
    balanceTestState.data = result || { supported: false, message: "无返回结果" };
  } catch (error) {
    balanceTestState.data = { supported: false, message: toUserError(error) };
  } finally {
    balanceTestState.loading = false;
  }
}

// 同 URL 下分组数：用于判断同步按钮是否有意义（多分组时才有价值）。
const sameURLGroupCount = computed(() => {
  const targetBase = String(bulkEditDraft.baseURL || "").trim();
  if (!targetBase) return 0;
  return new Set(
    appState.modelAdapters
      .filter((a) => String(a.baseURL || "").trim() === targetBase)
      .map((a) => String(a.groupName || "").trim() || "__default__"),
  ).size;
});

async function syncBalanceToSameURL() {
  if (balanceSyncState.loading) return;
  const baseURL = String(bulkEditDraft.baseURL || "").trim();
  if (!baseURL) {
    balanceSyncState.ok = false;
    balanceSyncState.message = "请先填写接口地址";
    return;
  }
  balanceSyncState.loading = true;
  balanceSyncState.message = "";
  try {
    const patch = {
      balanceProfile: String(bulkEditDraft.balanceProfile || "").trim(),
      balanceQueryURL: String(bulkEditDraft.balanceQueryURL || "").trim(),
      balanceQueryField: String(bulkEditDraft.balanceQueryField || "").trim(),
      balanceQueryHeadersJSON: String(bulkEditDraft.balanceQueryHeadersJSON || "").trim(),
      balanceAccessToken: String(bulkEditDraft.balanceAccessToken || "").trim(),
      balanceUserID: String(bulkEditDraft.balanceUserID || "").trim(),
      balanceCodingPlanProvider: String(bulkEditDraft.balanceCodingPlanProvider || "").trim(),
    };
    const result = await syncBalanceConfigToSameURL(baseURL, patch);
    if (!result.ok) {
      balanceSyncState.ok = false;
      balanceSyncState.message = String(result.error || "同步失败").trim();
      return;
    }
    await reloadUserConfig({ modelAdaptersOnly: true });
    balanceSyncState.ok = true;
    balanceSyncState.message = `已同步到 ${result.updated} 个模型（保留各自 API Key 与分组）`;
  } catch (error) {
    balanceSyncState.ok = false;
    balanceSyncState.message = toUserError(error);
  } finally {
    balanceSyncState.loading = false;
  }
}

async function saveBulkEdit(force = false) {
  bulkEditSaving.value = true;
  bulkEditError.value = "";
  bulkEditConflicts.value = [];
  try {
    const result = await updateModelAdaptersBySupplier(
      supplierIdentity.value,
      { ...bulkEditDraft },
      { forceOverwrite: force },
    );
    if (!result.ok) {
      bulkEditError.value = result.error || "保存失败";
      bulkEditConflicts.value = result.conflicts || [];
      return;
    }
    // 保存成功：提示用户并关闭弹窗
    await showModal({
      title: "保存成功",
      content: `已更新 ${supplierAdapters.value.length} 个模型的供应商配置。`,
      confirmText: "确定",
    });
    bulkEditExpanded.value = false;
    await reloadUserConfig({ modelAdaptersOnly: true });
  } catch (err) {
    bulkEditError.value = toUserError(err);
  } finally {
    bulkEditSaving.value = false;
  }
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <div class="flex flex-col gap-4 pb-2">
        <!-- 顶部：返回 + 供应商信息 -->
        <div class="flex items-center justify-between gap-3 border-b border-[#343434] pb-3">
          <div class="flex items-center gap-3">
            <button type="button" class="text-[#8f8f8f] hover:text-white" @click="router.back()">← 返回</button>
            <div>
              <h2 class="text-base font-medium text-white">{{ title }}</h2>
              <div class="center-row flex-wrap gap-2 text-xs text-[#8f8f8f]">
                <span>{{ formatHost(subtitle) }} · {{ supplierAdapters.length }} 个模型</span>
                <span v-if="healthStats.ok > 0" class="rounded-full bg-[#10AD5D]/15 px-2 py-0.5 text-[#6ee7a5]">可用 {{ healthStats.ok }}</span>
                <span v-if="healthStats.fail > 0" class="rounded-full bg-[#f87171]/15 px-2 py-0.5 text-[#fca5a5]">失败 {{ healthStats.fail }}</span>
                <span v-if="healthStats.untested > 0" class="rounded-full bg-[#3f3f3f]/60 px-2 py-0.5 text-[#a3a3a3]">未测 {{ healthStats.untested }}</span>
                <!-- 余额 / 额度 -->
                <span v-if="balanceState.loading" class="center-row gap-1 text-[#8f8f8f]">
                  <span class="icon-[mdi--loading] animate-spin text-[13px]"></span>查询余额…
                </span>
                <template v-else-if="balanceState.data && balanceState.data.supported">
                  <span
                    class="center-row gap-1 rounded-full px-2 py-0.5"
                    :class="balanceState.stale ? 'bg-[#a3a3a3]/15 text-[#c9c9c9]' : 'bg-[#10AD5D]/15 text-[#6ee7a5]'"
                    :title="balanceState.data.unlimited ? '该账户额度不限' : balanceSecondary"
                  >{{ balancePrimary }}<span v-if="balanceState.stale" class="text-[11px] text-[#8f8f8f]">（可能过期）</span></span>
                  <span v-if="!balanceState.data.unlimited && balanceSecondary" class="hidden text-[#666] sm:inline">{{ balanceSecondary }}</span>
                </template>
                <span
                  v-else-if="balanceState.loaded"
                  class="text-[#737373]"
                  :title="(balanceState.data && balanceState.data.message) || '余额不可用'"
                >余额不可用</span>
                <button
                  v-if="supplierMeta && !balanceState.loading"
                  type="button"
                  class="center-row text-[#8f8f8f] transition-colors hover:text-white"
                  title="刷新余额"
                  @click="loadBalance(true)"
                >
                  <span class="icon-[mdi--refresh] text-[13px]"></span>
                </button>
              </div>
            </div>
          </div>
          <div class="center-row gap-2">
            <Button
              variant="default"
              :disabled="batchTesting || supplierAdapters.length === 0"
              @click="testAllAdapters"
            >
              {{ batchTesting
                ? `测试中 ${batchProgress.done}/${batchProgress.total}`
                : `一键测试 (${supplierAdapters.length})` }}
            </Button>
            <Button variant="default" :disabled="supplierAdapters.length === 0" @click="toggleSelectionMode">
              {{ selectionMode ? "退出多选" : "多选" }}
            </Button>
            <Button variant="default" :disabled="supplierAdapters.length === 0" @click="toggleBulkEdit">
              编辑供应商
            </Button>
            <Button variant="default" :disabled="catalogLoading || !supplierMeta" @click="handleFetchModels">
              {{ catalogLoading ? "拉取中..." : "拉取模型" }}
            </Button>
            <Button variant="primary" :disabled="appState.configSaving" @click="openEditor(null)">新增模型</Button>
          </div>

          <Teleport to="body">
            <div
              v-if="bulkEditExpanded"
              class="fixed inset-0 z-999 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
              @click.self="!bulkEditSaving && (bulkEditExpanded = false)"
            >
              <div
                class="flex max-h-[calc(100vh-2rem)] w-full max-w-3xl flex-col overflow-hidden rounded-[10px] border border-[#4a4a4a] bg-[#252525] shadow-2xl"
                role="dialog"
                aria-modal="true"
                aria-labelledby="supplier-bulk-edit-title"
              >
                <div class="flex items-center justify-between border-b border-[#343434] px-5 py-4">
                  <div>
                    <h3 id="supplier-bulk-edit-title" class="text-base font-medium text-white">编辑供应商</h3>
                    <p class="mt-1 text-xs text-[#a3a3a3]">保存后同步更新该供应商下的全部模型连接配置。</p>
                  </div>
                  <button
                    type="button"
                    class="center-row size-8 justify-center rounded-[6px] text-[#8f8f8f] transition-colors hover:bg-white/10 hover:text-white disabled:opacity-50"
                    :disabled="bulkEditSaving"
                    title="关闭"
                    @click="bulkEditExpanded = false"
                  >
                    <span class="icon-[mdi--close] text-[18px]"></span>
                  </button>
                </div>
                <div class="overflow-y-auto p-5">
                  <div class="flex flex-col gap-3">
            <div class="text-xs text-[#a3a3a3]">修改以下字段将覆盖该供应商下全部 {{ supplierAdapters.length }} 个模型的对应配置。模型级配置（模型 ID、上下文、价格等）不受影响。</div>
            <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">模型类型</span>
                <select v-model="bulkEditDraft.type" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]">
                  <option v-for="opt in providerSelectOptions()" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
                </select>
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">供应商模板</span>
                <select v-model="bulkEditDraft.supplierID" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]">
                  <option value="">不使用模板</option>
                  <option v-for="opt in supplierSelectOptions(bulkEditDraft.type)" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
                </select>
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">接口地址</span>
                <input v-model="bulkEditDraft.baseURL" type="text" placeholder="https://..." class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">API Key</span>
                <input v-model="bulkEditDraft.apiKey" type="password" placeholder="sk-..." class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">分组名称</span>
                <input v-model="bulkEditDraft.groupName" type="text" placeholder="可选" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">备注</span>
                <input v-model="bulkEditDraft.tooltipData" type="text" placeholder="可选" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">协议模式</span>
                <select v-model="bulkEditDraft.protocolMode" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]">
                  <option value="auto">自动</option>
                  <option value="fixed">固定</option>
                </select>
              </label>
              <label v-if="bulkEditDraft.type === 'openai' || bulkEditDraft.type === 'gemini'" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">协议端点</span>
                <select v-model="bulkEditDraft.openAIEndpoint" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]">
                  <option value="">/v1/chat/completions</option>
                  <option value="/v1/responses">/v1/responses</option>
                </select>
              </label>
            </div>

            <!-- 自定义请求头 -->
            <label class="flex items-center gap-2 text-xs text-[#a3a3a3]">
              <input v-model="bulkEditDraft.customHeadersEnabled" type="checkbox" class="accent-[#10AD5D]" />
              <span>自定义请求头</span>
            </label>
            <textarea
              v-if="bulkEditDraft.customHeadersEnabled"
              v-model="bulkEditDraft.customHeadersJSON"
              rows="2"
              placeholder='{"X-Custom": "value"}'
              class="rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 py-1.5 font-mono text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            ></textarea>

            <!-- 余额查询配置 -->
            <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
              <label class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">查询模板</span>
                <select v-model="bulkEditDraft.balanceProfile" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]">
                  <option value="custom">自定义</option>
                  <option value="general">通用模板</option>
                  <option value="newapi">New API</option>
                  <option value="token_plan">Token Plan</option>
                  <option value="official">官方</option>
                </select>
              </label>
              <div v-if="bulkEditDraft.balanceProfile === 'general'" class="text-xs leading-5 text-[#737373] md:col-span-2">
                通用模板将请求接口地址下的 <code>/user/balance</code>，使用当前 API Key 查询 balance。
              </div>
              <div v-if="bulkEditDraft.balanceProfile === 'official'" class="text-xs leading-5 text-[#737373] md:col-span-2">
                官方模板按接口地址识别 DeepSeek、StepFun、SiliconFlow、OpenRouter、Novita 等官方余额接口。
              </div>
              <label v-if="bulkEditDraft.balanceProfile === 'token_plan'" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">Token Plan 供应商</span>
                <select v-model="bulkEditDraft.balanceCodingPlanProvider" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]">
                  <option value="">自动检测</option>
                  <option value="kimi">Kimi For Coding</option>
                  <option value="zhipu">Zhipu GLM</option>
                  <option value="zhipu_team">Zhipu GLM Team</option>
                  <option value="minimax">MiniMax</option>
                  <option value="zenmux">ZenMux</option>
                  <option value="volcengine">火山方舟</option>
                </select>
              </label>
              <label v-if="bulkEditDraft.balanceProfile === 'newapi'" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">访问令牌（New API）</span>
                <input v-model="bulkEditDraft.balanceAccessToken" type="text" placeholder="安全设置生成" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label v-if="bulkEditDraft.balanceProfile === 'newapi'" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">用户 ID（New API）</span>
                <input v-model="bulkEditDraft.balanceUserID" type="text" placeholder="例如 114514" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label v-if="bulkEditDraft.balanceProfile === 'custom'" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">余额查询地址</span>
                <input v-model="bulkEditDraft.balanceQueryURL" type="text" placeholder="可选" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label v-if="bulkEditDraft.balanceProfile === 'custom'" class="flex flex-col gap-1">
                <span class="text-xs text-[#a3a3a3]">余额字段路径</span>
                <input v-model="bulkEditDraft.balanceQueryField" type="text" placeholder="可选" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
              <label v-if="bulkEditDraft.balanceProfile === 'custom'" class="flex flex-col gap-1 md:col-span-2">
                <span class="text-xs text-[#a3a3a3]">查询请求头</span>
                <input v-model="bulkEditDraft.balanceQueryHeadersJSON" type="text" placeholder="{}" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]" />
              </label>
            </div>

            <div class="flex flex-wrap items-center gap-2 rounded-[7px] border border-[#3f3f3f] bg-[#202020] px-3 py-2">
              <Button variant="default" :disabled="balanceTestState.loading || bulkEditSaving" @click="testBulkEditBalance">
                <span v-if="balanceTestState.loading" class="icon-[mdi--loading] mr-1 animate-spin text-[14px]"></span>
                {{ balanceTestState.loading ? "测试中…" : "测试余额" }}
              </Button>
              <span class="text-xs text-[#737373]">使用当前未保存的连接与余额配置，不会保存修改。</span>
              <div v-if="balanceTestState.data" class="basis-full text-xs" :class="balanceTestState.data.supported ? 'text-[#6ee7a5]' : 'text-[#fca5a5]'">
                <template v-if="balanceTestState.data.supported">
                  <span class="font-medium">{{ balanceTestState.data.unlimited ? "余额不限额" : `余额 ${formatMoney(balanceTestState.data.remaining, balanceTestState.data.currency)}` }}</span>
                  <span v-if="balanceTestState.data.total != null"> · 总额 {{ formatMoney(balanceTestState.data.total, balanceTestState.data.currency) }}</span>
                  <span v-if="balanceTestState.data.used != null"> · 已用 {{ formatMoney(balanceTestState.data.used, balanceTestState.data.currency) }}</span>
                  <span v-if="balanceTestState.data.currency"> · 币种 {{ balanceTestState.data.currency }}</span>
                  <span v-if="balanceTestState.data.source"> · 来源 {{ balanceSourceLabel(balanceTestState.data.source) }}</span>
                  <span v-if="balanceTestState.data.planName"> · {{ balanceTestState.data.planName }}</span>
                </template>
                <span v-else>{{ balanceTestState.data.message || "余额查询失败" }}</span>
              </div>
              <Button
                variant="default"
                :disabled="balanceSyncState.loading || bulkEditSaving"
                :title="sameURLGroupCount > 1 ? `将余额配置同步到同 URL 下的 ${sameURLGroupCount} 个分组（保留各自 API Key 与分组名）` : '同一中转站下暂无其他分组'"
                @click="syncBalanceToSameURL"
              >
                <span v-if="balanceSyncState.loading" class="icon-[mdi--sync] mr-1 animate-spin text-[14px]"></span>
                <span v-else class="icon-[mdi--sync] mr-1 text-[14px]"></span>
                {{ balanceSyncState.loading ? "同步中…" : "同步到同 URL 分组" }}
              </Button>
              <span class="text-xs text-[#737373]">
                仅同步余额查询配置，保留各分组 API Key{{ sameURLGroupCount > 1 ? `（当前 ${sameURLGroupCount} 个分组）` : "" }}
              </span>
              <div
                v-if="balanceSyncState.message"
                class="basis-full text-xs"
                :class="balanceSyncState.ok ? 'text-[#6ee7a5]' : 'text-[#fca5a5]'"
              >{{ balanceSyncState.message }}</div>
            </div>

            <!-- 冲突提示 -->
            <div v-if="bulkEditConflicts.length > 0" class="rounded-[6px] border border-yellow-800/40 bg-yellow-900/20 px-3 py-2 text-xs text-yellow-200">
              ⚠️ 检测到 {{ bulkEditConflicts.length }} 个模型保存后与其他供应商下的配置重复。
              <button type="button" class="ml-2 underline" @click="saveBulkEdit(true)">强制覆盖</button>
            </div>
            <div v-if="bulkEditError && bulkEditConflicts.length === 0" class="text-xs text-red-400">{{ bulkEditError }}</div>

            <!-- 保存 / 取消 -->
            <div class="center-row gap-2">
              <Button variant="primary" :disabled="bulkEditSaving" @click="saveBulkEdit(false)">
                {{ bulkEditSaving ? "保存中..." : `保存（覆盖 ${supplierAdapters.length} 个模型）` }}
              </Button>
              <Button variant="default" @click="bulkEditExpanded = false">取消</Button>
            </div>
                  </div>
                </div>
              </div>
            </div>
          </Teleport>
        </div>

        <!-- 搜索 / 过滤 / 排序 工具条 -->
        <div v-if="supplierAdapters.length > 0" class="flex flex-wrap items-center gap-2">
          <div class="relative">
            <span class="icon-[mdi--magnify] pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-[16px] text-[#737373]"></span>
            <input
              v-model="modelSearch"
              type="text"
              placeholder="搜索模型名 / 标识"
              class="h-8 w-52 rounded-[8px] border border-[#3f3f3f] bg-[#232323] pl-7 pr-7 text-[12px] text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
            <button
              v-if="modelSearch"
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-[#737373] hover:text-white"
              @click="modelSearch = ''"
            >
              <span class="icon-[mdi--close-circle] text-[14px]"></span>
            </button>
          </div>
          <div class="inline-flex rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]" role="group" aria-label="状态过滤">
            <button
              v-for="opt in statusFilterOptions"
              :key="opt.value"
              type="button"
              class="rounded-[6px] px-2.5 py-1 transition-colors"
              :class="statusFilter === opt.value ? 'bg-[#10AD5D]/25 text-[#6ee7a5]' : 'text-[#a3a3a3] hover:text-white'"
              @click="statusFilter = opt.value"
            >
              {{ opt.label }}
            </button>
          </div>
          <div class="inline-flex rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]" role="group" aria-label="排序方式">
            <button
              v-for="opt in sortModeOptions"
              :key="opt.value"
              type="button"
              class="rounded-[6px] px-2.5 py-1 transition-colors"
              :class="sortMode === opt.value ? 'bg-[#10AD5D]/25 text-[#6ee7a5]' : 'text-[#a3a3a3] hover:text-white'"
              @click="sortMode = opt.value"
            >
              {{ opt.label }}
            </button>
          </div>
          <span class="text-[12px] text-[#737373]">显示 {{ visibleAdapters.length }}/{{ supplierAdapters.length }}</span>
          <Button
            v-if="healthStats.fail > 0"
            variant="text"
            class="ml-auto text-[#f87171] hover:text-[#fca5a5]"
            @click="deleteFailedAdapters"
          >
            <span class="icon-[mdi--trash-can-outline] mr-1 text-[14px]"></span>删除失败模型 ({{ healthStats.fail }})
          </Button>
        </div>

        <!-- 多选批量操作条 -->
        <div v-if="selectionMode" class="center-row flex-wrap justify-between gap-2 rounded-[8px] border border-[#10AD5D]/30 bg-[#10AD5D]/5 px-3 py-2">
          <div class="center-row gap-3 text-xs text-[#d4d4d4]">
            <button type="button" class="text-[#6ee7a5]" @click="toggleSelectAllVisible">
              {{ allVisibleSelected ? "取消全选" : "全选当前" }}
            </button>
            <span>已选 {{ selectedAdapters.length }} 个</span>
          </div>
          <div class="center-row gap-2">
            <Button variant="default" :disabled="batchTesting || selectedAdapters.length === 0" @click="testSelectedAdapters">
              {{ batchTesting ? "测试中..." : "测试选中" }}
            </Button>
            <Button variant="text" class="text-[#f87171] hover:text-[#fca5a5]" :disabled="selectedAdapters.length === 0" @click="deleteSelectedAdapters">
              删除选中
            </Button>
          </div>
        </div>

        <!-- 远程拉取的模型选择列表 -->
        <div v-if="catalogModels.length > 0" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="mb-2 flex items-center justify-between gap-2 text-xs text-[#a3a3a3]">
            <span>已选 {{ selectedCatalogCount }}/{{ catalogModels.length }}</span>
            <div class="center-row gap-3">
              <button
                type="button"
                class="text-[#67e8f9] disabled:opacity-50"
                :disabled="catalogProbe.probing.value"
                @click="handleProbeCatalog"
              >
                {{ catalogProbe.probing.value ? "检测中..." : "检测可用性" }}
              </button>
              <button type="button" class="text-[#6ee7a5]" @click="toggleAllCatalogModels">
                {{ allCatalogSelected ? "取消全选" : "全选" }}
              </button>
            </div>
          </div>
          <div class="max-h-48 overflow-y-auto">
            <label v-for="model in catalogModels" :key="model.id" class="flex items-center gap-2 py-1 text-xs text-[#d4d4d4]">
              <input type="checkbox" class="size-4 accent-[#10AD5D]" :checked="isCatalogModelSelected(model.id)" @change="toggleCatalogModel(model.id)" />
              <span class="truncate">{{ model.id }}</span>
              <span
                v-if="catalogProbe.statusOf(model.id) === 'checking'"
                class="ml-auto shrink-0 rounded-full border border-[#164e63] bg-[#0b2530] px-1.5 py-0.5 text-[10px] text-[#67e8f9]"
              >检测中</span>
              <span
                v-else-if="catalogProbe.statusOf(model.id) === 'ok'"
                class="ml-auto shrink-0 rounded-full border border-[#14532d] bg-[#102418] px-1.5 py-0.5 text-[10px] text-[#86efac]"
              >✓ 可用</span>
              <span
                v-else-if="catalogProbe.statusOf(model.id) === 'fail'"
                :title="catalogProbe.messageOf(model.id)"
                class="ml-auto shrink-0 rounded-full border border-[#4b1d1d] bg-[#2a1313] px-1.5 py-0.5 text-[10px] text-[#fca5a5]"
              >✗ {{ catalogProbe.messageOf(model.id) || '不可用' }}</span>
            </label>
          </div>
          <Button class="mt-2 w-full" variant="primary" :disabled="catalogSaving" @click="handleBatchAddModels">
            {{ catalogSaving ? "添加中..." : "添加已选模型" }}
          </Button>
        </div>

        <div v-if="catalogError" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
          {{ catalogError }}
        </div>

        <!-- 已配置的模型列表 -->
        <div v-if="!supplierAdapters.length" class="rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] px-4 py-8 text-center text-sm text-[#a3a3a3]">
          该供应商下暂无模型。点击上方"拉取模型"从远程获取，或"新增模型"手动添加。
        </div>

        <div v-else-if="!visibleAdapters.length" class="rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] px-4 py-8 text-center text-sm text-[#a3a3a3]">
          没有符合当前搜索/过滤条件的模型。
        </div>

        <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(250px,1fr))]">
          <Card
            v-for="adapter in visibleAdapters"
            :key="adapter.id || `${adapter.baseURL}-${adapter.modelID}`"
            :class="selectionMode && isAdapterSelected(adapter) ? 'border-[#10AD5D]/50' : ''"
          >
            <div class="flex h-full min-h-[154px] flex-col justify-between gap-3">
              <div class="flex flex-col gap-2.5">
                <div class="flex items-start justify-between gap-3">
                  <label v-if="selectionMode" class="mt-1 shrink-0 cursor-pointer">
                    <input type="checkbox" class="size-4 accent-[#10AD5D]" :checked="isAdapterSelected(adapter)" @change="toggleAdapterSelection(adapter)" />
                  </label>
                  <div class="min-w-0 flex-1">
                    <div class="center-row gap-1.5">
                      <span class="truncate text-base font-medium text-white">{{ adapter.displayName }}</span>
                      <span
                        v-if="adapterHealth(adapter) === 'ok'"
                        class="shrink-0 rounded-full bg-[#10AD5D]/15 px-1.5 py-0.5 text-[10px] text-[#6ee7a5]"
                      >可用</span>
                      <span
                        v-else-if="adapterHealth(adapter) === 'fail'"
                        class="shrink-0 rounded-full bg-[#f87171]/15 px-1.5 py-0.5 text-[10px] text-[#fca5a5]"
                      >失败</span>
                    </div>
                    <div class="mt-1 truncate text-sm text-[#8f8f8f]">{{ adapter.modelID }}</div>
                    <div v-if="adapter.type === 'openai'" class="mt-1 flex flex-wrap gap-1.5 text-xs text-[#737373]">
                      <span class="rounded-full border border-[#3a3a3a] bg-[#232323] px-2 py-0.5">
                        {{ resolvedOpenAIEndpoint(adapter) }}
                      </span>
                      <span class="rounded-full border border-[#24553c] bg-[#173524] px-2 py-0.5 text-[#86efac]">
                        {{ formatOpenAIRequestGroup(resolvedOpenAIRequestGroup(adapter), resolvedOpenAIEndpoint(adapter)) }}
                      </span>
                    </div>
                  </div>
                  <span :class="[providerIcon(adapter.type), 'text-[20px] shrink-0']"></span>
                </div>
                <div class="grid grid-cols-2 gap-2 text-sm text-[#a3a3a3]">
                  <div class="rounded-[8px] bg-[#232323] px-3 py-2">
                    <div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">Host</div>
                    <div class="mt-1 truncate text-[#d4d4d4]">{{ formatHost(adapter.baseURL) }}</div>
                  </div>
                  <div class="rounded-[8px] bg-[#232323] px-3 py-2">
                    <div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">API Key</div>
                    <div class="mt-1 truncate text-[#d4d4d4]">{{ maskSecret(adapter.apiKey) }}</div>
                  </div>
                </div>
                <ModelAdapterTestCard compact title="测试" empty-text="未测试" :result="testResult(adapter)" />
              </div>
              <div class="center-row flex-wrap justify-end gap-2 border-t border-[#343434] pt-3">
                <Button variant="default" :disabled="appState.configSaving || isTesting(adapter)" @click="testAdapter(adapter)">{{ isTesting(adapter) ? "测试中..." : "测试" }}</Button>
                <Button variant="default" :disabled="appState.configSaving" @click="openEditor(adapter)">编辑</Button>
                <Button variant="default" :disabled="appState.configSaving" @click="duplicateAdapter(adapter)">复制</Button>
                <Button variant="text" :disabled="appState.configSaving" @click="deleteAdapter(adapter)">删除</Button>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </div>
  </div>
</template>