<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  deleteModelAdapterAt,
  getModelAdapterTestResultByID,
  openModelEditorWindow,
  reloadUserConfig,
  runModelAdapterTest,
  saveModelAdaptersBatch,
  startModelAdapterTest,
  toUserError,
} from "@/state/appState";
import { fetchModelCatalog } from "@/services/clientApi";
import { providerIcon } from "@/utils/providerMeta";
import {
  SUPPLIER_GROUP_MODE_CONNECTION,
  SUPPLIER_GROUP_MODE_NAME,
  adapterMatchesSupplierIdentity,
  displayGroupName,
  normalizeSupplierBaseURL,
  supplierIdentityFromRouteQuery,
} from "@/utils/supplierGrouping";
import { computed, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";

const OPENAI_ENDPOINT_RESPONSES = "/v1/responses";
const OPENAI_ENDPOINT_CHAT = "/v1/chat/completions";

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

// 第一个模型的 type/apiKey/customHeaders，用于拉取新模型
const supplierMeta = computed(() => supplierAdapters.value[0] || null);

// catalog 状态（拉取远程模型列表）
const catalogLoading = ref(false);
const catalogSaving = ref(false);
const catalogError = ref("");
const catalogModels = ref([]); // [{ id, contextWindowTokens, pricing }]
const selectedCatalogModels = ref(new Set());

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
  try {
    const result = await fetchModelCatalog({
      type: supplierMeta.value.type,
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

function createEmptyModelAdapter() {
  return {
    id: "", displayName: "", groupName: "", type: "openai",
    protocolMode: "auto", protocolGroup: "responses",
    baseURL: "", apiKey: "", tooltipData: "", modelID: "",
    reasoningEffort: "medium", openAIEndpoint: "/v1/responses",
    openAIRequestGroup: "responses",
    openAIExtraParamsEnabled: false, openAIExtraParamsJSON: "{\n}",
    customHeadersEnabled: false, customHeadersJSON: "{\n}",
    anthropicExtraParamsEnabled: false, anthropicExtraParamsJSON: "{\n}",
    contextWindowTokens: 0, maxCompletionTokens: 0,
    anthropicMaxTokens: 0, anthropicThinkingEffort: "xhigh",
    thinkingBudgetTokens: 0, pricing: null, fastMode: false, openAIServiceTier: "",
  };
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
      protocolGroup: supplierMeta.value.type === "anthropic" ? "messages" : "",
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
  try {
    // 并发上限 3，避免打满上游
    const queue = [...supplierAdapters.value];
    const concurrency = 3;
    const workers = Array.from({ length: Math.min(concurrency, queue.length) }, async () => {
      while (queue.length > 0) {
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
  // 目前测试是 fire-and-forget，无法真正取消；仅标记 UI 停止
  batchTesting.value = false;
}

onMounted(() => { void reloadUserConfig({ modelAdaptersOnly: true }).catch(() => {}); });
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
              <div class="text-xs text-[#8f8f8f]">{{ formatHost(subtitle) }} · {{ supplierAdapters.length }} 个模型</div>
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
            <Button variant="default" :disabled="catalogLoading || !supplierMeta" @click="handleFetchModels">
              {{ catalogLoading ? "拉取中..." : "拉取模型" }}
            </Button>
            <Button variant="primary" :disabled="appState.configSaving" @click="openEditor(null)">新增模型</Button>
          </div>
        </div>

        <!-- 远程拉取的模型选择列表 -->
        <div v-if="catalogModels.length > 0" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="mb-2 flex items-center justify-between text-xs text-[#a3a3a3]">
            <span>已选 {{ selectedCatalogCount }}/{{ catalogModels.length }}</span>
            <button type="button" class="text-[#6ee7a5]" @click="toggleAllCatalogModels">
              {{ allCatalogSelected ? "取消全选" : "全选" }}
            </button>
          </div>
          <div class="max-h-48 overflow-y-auto">
            <label v-for="model in catalogModels" :key="model.id" class="flex items-center gap-2 py-1 text-xs text-[#d4d4d4]">
              <input type="checkbox" class="size-4 accent-[#10AD5D]" :checked="isCatalogModelSelected(model.id)" @change="toggleCatalogModel(model.id)" />
              <span class="truncate">{{ model.id }}</span>
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

        <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(250px,1fr))]">
          <Card v-for="adapter in supplierAdapters" :key="adapter.id || `${adapter.baseURL}-${adapter.modelID}`">
            <div class="flex h-full min-h-[154px] flex-col justify-between gap-3">
              <div class="flex flex-col gap-2.5">
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-base font-medium text-white">{{ adapter.displayName }}</div>
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