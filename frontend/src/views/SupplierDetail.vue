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
import { computed, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";

const route = useRoute();
const router = useRouter();

const queryBaseURL = computed(() => String(route.query.baseURL || "").trim());
const queryGroupName = computed(() => String(route.query.groupName || "").trim());

const title = computed(() => queryGroupName.value || "默认分组");
const subtitle = computed(() => queryBaseURL.value);

const providers = [
  { value: "openai", label: "OpenAI / OAI", icon: "icon-[bxl--openai]" },
  { value: "anthropic", label: "Anthropic / A社", icon: "icon-[logos--claude-icon]" },
];

function providerIcon(type) {
  return providers.find((p) => p.value === type)?.icon || "";
}
function providerLabel(type) {
  return providers.find((p) => p.value === type)?.label || type;
}
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

// 该供应商下的模型（匹配 baseURL + groupName）
const supplierAdapters = computed(() =>
  appState.modelAdapters.filter((adapter) => {
    const baseURL = String(adapter.baseURL || "").trim();
    const groupName = String(adapter.groupName || "").trim();
    return baseURL === queryBaseURL.value && groupName === queryGroupName.value;
  })
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
  return `${queryBaseURL.value}::${modelID}`;
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
function toggleAllCatalogModels() {
  if (catalogModels.value.every((m) => isCatalogModelSelected(m.id))) {
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
      baseURL: queryBaseURL.value,
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
    baseURL: "", apiKey: "", tooltipData: "", modelID: "",
    reasoningEffort: "medium", openAIEndpoint: "/v1/responses",
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
    const adapters = selected.map((model) => ({
      ...createEmptyModelAdapter(),
      type: supplierMeta.value.type,
      baseURL: queryBaseURL.value,
      apiKey: supplierMeta.value.apiKey,
      customHeadersEnabled: Boolean(supplierMeta.value.customHeadersEnabled),
      customHeadersJSON: supplierMeta.value.customHeadersJSON || "",
      displayName: model.id,
      modelID: model.id,
      groupName: queryGroupName.value,
      tooltipData: `来自 ${formatHost(queryBaseURL.value)}`,
      contextWindowTokens: model.contextWindowTokens || 0,
      pricing: model.pricing || null,
      openAIEndpoint: supplierMeta.value.type === "openai" ? "/v1/responses" : "",
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
  try { await runModelAdapterTest(adapter); } catch (_e) { /* card shows result */ }
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
            <Button variant="default" :disabled="catalogLoading || !supplierMeta" @click="handleFetchModels">
              {{ catalogLoading ? "拉取中..." : "拉取模型" }}
            </Button>
            <Button variant="primary" :disabled="appState.configSaving" @click="openEditor(null)">新增模型</Button>
          </div>
        </div>

        <!-- 远程拉取的模型选择列表 -->
        <div v-if="catalogModels.length > 0" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
          <div class="mb-2 flex items-center justify-between text-xs text-[#a3a3a3]">
            <span>已选 {{ catalogModels.filter((m) => isCatalogModelSelected(m.id)).length }}/{{ catalogModels.length }}</span>
            <button type="button" class="text-[#6ee7a5]" @click="toggleAllCatalogModels">
              {{ catalogModels.every((m) => isCatalogModelSelected(m.id)) ? "取消全选" : "全选" }}
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
                    <div v-if="adapter.type === 'openai'" class="mt-0.5 truncate text-xs text-[#737373]">{{ adapter.openAIEndpoint || "/v1/responses" }}</div>
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