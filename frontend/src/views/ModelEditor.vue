<script setup>
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { getModelEditorContext, fetchModelCatalog } from "@/services/clientApi";
import { resolveModelContextWindow } from "@/utils/modelContext";
import {
  ANTHROPIC_THINKING_EFFORT_DEFAULT,
  appState,
  buildModelAdapterTestRequestHash,
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
  runModelAdapterTest,
  saveModelAdapterAt,
  saveModelAdaptersBatch,
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
  baseURL: "",
  apiKey: "",
  tooltipData: "",
  modelID: "",
  reasoningEffort: "medium",
  openAIEndpoint: OPENAI_ENDPOINT_RESPONSES,
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
});

const openAIEndpointOptions = [
  { label: "/v1/responses", value: OPENAI_ENDPOINT_RESPONSES, icon: "icon-[mdi--api]" },
  { label: "/v1/chat/completions", value: OPENAI_ENDPOINT_CHAT_COMPLETIONS, icon: "icon-[mdi--message-text-outline]" },
  { label: "自定义路径(请输入完整请求地址)", value: OPENAI_ENDPOINT_CUSTOM, icon: "icon-[mdi--pencil-outline]" },
];

const editorIndex = ref(-1);
const router = useRouter();
const draft = reactive(createEmptyModelAdapter());
const errorMessage = ref("");
const loading = ref(true);
const lastTestAdapterID = ref("");
const localTestFailure = ref("");
const catalogModels = ref([]);
const catalogGroups = ref([]);
const catalogLoading = ref(false);
const catalogError = ref("");
const selectedCatalogModels = ref(new Set());
const catalogSaving = ref(false);

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
const detectedContextWindow = computed(() => resolveModelContextWindow(draft.modelID));
const interfacePlaceholder = computed(() =>
  draft.type === "anthropic" ? "例如：https://api.anthropic.com" : "例如：https://api.openai.com/v1",
);
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

const title = computed(() => (editorIndex.value >= 0 ? "编辑模型配置" : "新增模型配置"));

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

const fieldTips = {
  displayName: "仅用于界面展示，便于你区分不同模型。",
  modelID: "请求实际发送给服务端的模型名称，例如 gpt-4.1 或 claude-sonnet。",
  baseURL: "模型服务的 API 根地址，通常为兼容 OpenAI 或 Anthropic 的接口入口。",
  apiKey: "调用该模型服务需要使用的访问密钥。",
  contextWindowTokens: "模型单次可接受的最大上下文 Token 数。留空时使用默认值。",
  reasoningEffort: "推理强度仅对部分支持 reasoning_effort 的模型生效，并不是所有模型都支持。越高通常越稳，但也可能更慢。",
  maxCompletionTokens: "单次回复允许生成的最大 Token 数。留空时使用默认值。",
  openAIEndpoint: "选择接口协议端点。选“自定义路径”时，请在接口地址栏填写完整请求地址（含 /chat/completions 或 /responses 路径后缀），系统会根据末段自动判断协议形态。",
  openAIExtraParams: "开启后会把 JSON 对象覆盖到 OpenAI 请求体。同名字段以这里为准。OpenAI service_tier 支持 auto、default、flex、scale、priority。",
  customHeaders: "开启后会把 JSON 对象覆盖到最终请求头。同名请求头以这里为准，值必须是字符串。",
  anthropicExtraParams: "开启后会把 JSON 对象覆盖到 Anthropic 请求体。同名字段以这里为准。",
  anthropicMaxTokens: "Anthropic 模型单次回复允许生成的最大 Token 数。留空时使用默认值。",
  anthropicThinkingEffort: "Anthropic adaptive thinking 的思考强度。请求会固定使用新版 thinking.type=adaptive。",
  tooltipData: "模型列表 hover 时显示的备注说明。",
};

async function loadContext() {
  try {
    const ctx = await getModelEditorContext();
    editorIndex.value = typeof ctx.index === "number" ? ctx.index : -1;
    const parsed = JSON.parse(ctx.adapterJSON || "{}");
    Object.assign(draft, normalizeModelAdapter(parsed));
    if (!draft.type) {
      draft.type = "openai";
    }
  } catch (_error) {
    Object.assign(draft, createEmptyModelAdapter());
    draft.type = "openai";
  } finally {
    loading.value = false;
  }
}

async function persistDraft() {
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

function handleModelTypeChange(type) {
  draft.type = type;
  if (type === "openai" && !draft.openAIEndpoint) {
    draft.openAIEndpoint = OPENAI_ENDPOINT_RESPONSES;
  } else if (type === "anthropic") {
    ensureAnthropicThinkingEffort();
  }
}

async function handleFetchModels() {
  catalogError.value = "";
  const baseURL = String(draft.baseURL || "").trim();
  const apiKey = String(draft.apiKey || "").trim();
  if (!baseURL || !apiKey) {
    catalogError.value = "请先填写接口地址和访问密钥";
    return;
  }

  catalogLoading.value = true;
  try {
    const result = await fetchModelCatalog({
      type: draft.type,
      baseURL,
      apiKey,
      customHeadersEnabled: Boolean(draft.customHeadersEnabled),
      customHeadersJSON: draft.customHeadersJSON || "",
    });
    const fetchedModels = Array.isArray(result?.models) ? result.models : [];
    const sourceURL = String(baseURL).trim();
    const group = {
      key: sourceURL,
      name: catalogGroups.value.find((item) => item.key === sourceURL)?.name || sourceURL,
      baseURL: sourceURL,
      type: draft.type,
      apiKey: draft.apiKey,
      customHeadersEnabled: Boolean(draft.customHeadersEnabled),
      customHeadersJSON: draft.customHeadersJSON || "",
      models: fetchedModels,
    };
    const existing = catalogGroups.value.filter((item) => item.key !== sourceURL);
    catalogGroups.value = [...existing, group];
    catalogModels.value = catalogGroups.value.flatMap((item) => item.models);
    selectedCatalogModels.value = new Set(
      fetchedModels.map((model) => catalogSelectionKey(sourceURL, model.id)),
    );
    if (fetchedModels.length === 0) {
      catalogError.value = "服务未返回可用模型";
    }
  } catch (error) {
    catalogModels.value = [];
    catalogError.value = toUserError(error);
  } finally {
    catalogLoading.value = false;
  }
}

function catalogSelectionKey(groupKey, modelID) {
  return `${groupKey}::${modelID}`;
}

function isCatalogModelSelected(groupKey, modelID) {
  return selectedCatalogModels.value.has(catalogSelectionKey(groupKey, modelID));
}

function toggleCatalogModel(groupKey, modelID) {
  const key = catalogSelectionKey(groupKey, modelID);
  const next = new Set(selectedCatalogModels.value);
  if (next.has(key)) next.delete(key); else next.add(key);
  selectedCatalogModels.value = next;
}

function toggleAllCatalogModels(group) {
  const keys = group.models.map((model) => catalogSelectionKey(group.key, model.id));
  const allSelected = keys.every((key) => selectedCatalogModels.value.has(key));
  const next = new Set(selectedCatalogModels.value);
  keys.forEach((key) => (allSelected ? next.delete(key) : next.add(key)));
  selectedCatalogModels.value = next;
}

async function handleBatchAddModels() {
  const selected = catalogGroups.value.flatMap((group) => group.models
    .filter((model) => selectedCatalogModels.value.has(catalogSelectionKey(group.key, model.id)))
    .map((model) => ({ group, model })));
  if (selected.length === 0) {
    catalogError.value = "请至少选择一个模型";
    return;
  }
  catalogSaving.value = true;
  try {
    const adapters = selected.map(({ group, model }) => normalizeModelAdapter({
      ...createEmptyModelAdapter(),
      type: group.type,
      baseURL: group.baseURL,
      apiKey: group.apiKey,
      customHeadersEnabled: group.customHeadersEnabled,
      customHeadersJSON: group.customHeadersJSON,
      displayName: model.id,
      modelID: model.id,
      groupName: group.name || group.baseURL,
      tooltipData: `来自 ${group.baseURL}`,
      contextWindowTokens: model.contextWindowTokens || 0,
      pricing: model.pricing || null,
    }));
    const result = await saveModelAdaptersBatch(adapters);
    if (!result.ok) {
      catalogError.value = result.error || "批量添加失败";
      return;
    }
    const added = Number(result.added || 0);
    const skipped = Number(result.skipped || 0);
    const updated = Number(result.updated || 0);
    catalogError.value = `新增 ${added} 个，跳过 ${skipped} 个重复项，更新 ${updated} 个`;
    selectedCatalogModels.value = new Set();
  } catch (error) {
    catalogError.value = toUserError(error);
  } finally {
    catalogSaving.value = false;
  }
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
        <Button variant="default" :disabled="isCurrentConfigTesting || appState.configSaving" @click="handleTest">
          {{ isCurrentConfigTesting ? "测试中..." : "保存并测试" }}
        </Button>
        <Button variant="primary" :disabled="appState.configSaving" @click="handleSave">
          {{ appState.configSaving ? "保存中..." : "保存" }}
        </Button>
      </div>
    </div>

    <div v-if="loading" class="flex flex-1 items-center justify-center text-sm text-[#a3a3a3]">
      加载中...
    </div>

    <div v-else class="flex-1 overflow-y-auto min-h-0 px-4 pb-4">
      <div class="flex flex-col gap-4">
        <div class="rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2 text-sm text-[#d4d4d4]">
          供应商：<span class="font-medium text-white">{{ draft.type === "anthropic" ? "Anthropic / A社" : "OpenAI / OAI" }}</span>
          <span class="ml-2 text-xs text-[#8f8f8f]">新增模型的供应商归属由模型配置分组决定</span>
        </div>

        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
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
              placeholder="按 URL 或渠道归类"
              class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
            />
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
              <Button
                variant="default"
                :disabled="catalogLoading || !draft.baseURL || !draft.apiKey"
                @click="handleFetchModels"
              >
                {{ catalogLoading ? "拉取中..." : "拉取模型" }}
              </Button>
            </div>
            <div v-if="catalogGroups.length > 0" class="mt-2 space-y-2">
              <div v-for="group in catalogGroups" :key="group.key" class="rounded-[8px] border border-[#343434] bg-[#252525] p-2">
                <div class="mb-2 flex items-center gap-2">
                  <span class="text-xs text-[#a3a3a3]">模型 URL 分组</span>
                  <input
                    v-model="group.name"
                    type="text"
                    placeholder="请输入分组名称"
                    class="h-8 min-w-0 flex-1 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
                  />
                </div>
                <div class="mb-2 flex items-center justify-between text-xs text-[#a3a3a3]">
                  <span>{{ group.baseURL }} · 已选 {{ group.models.filter((model) => isCatalogModelSelected(group.key, model.id)).length }}/{{ group.models.length }}</span>
                  <button type="button" class="text-[#6ee7a5]" @click="toggleAllCatalogModels(group)">
                    {{ group.models.every((model) => isCatalogModelSelected(group.key, model.id)) ? "取消全选" : "全选" }}
                  </button>
                </div>
                <div class="max-h-40 overflow-y-auto">
                  <label v-for="model in group.models" :key="model.id" class="flex items-center gap-2 py-1 text-xs text-[#d4d4d4]">
                    <input
                      type="checkbox"
                      class="size-4 accent-[#10AD5D]"
                      :checked="isCatalogModelSelected(group.key, model.id)"
                      @change="toggleCatalogModel(group.key, model.id)"
                    />
                    <span class="truncate">{{ model.id }}</span>
                  </label>
                </div>
              </div>
              <Button class="w-full" variant="primary" :disabled="catalogSaving" @click="handleBatchAddModels">
                {{ catalogSaving ? "添加中..." : "添加已选模型" }}
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

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.contextWindowTokens" />
              <span>上下文窗口</span>
            </span>
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

          <label v-if="draft.type === 'openai'" class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.reasoningEffort" />
              <span>推理强度</span>
            </span>
            <Select
              v-model="draft.reasoningEffort"
              :options="reasoningEffortOptions"
            />
          </label>

          <label v-if="draft.type === 'openai' && /gpt/i.test(draft.modelID || '')" class="center-row justify-between gap-3 rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2 text-sm text-[#d4d4d4]">
            <span>Fast 模式（priority）</span>
            <input v-model="draft.fastMode" type="checkbox" class="size-4 accent-[#10AD5D]" />
          </label>

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

        <div v-if="draft.type === 'openai'" class="grid grid-cols-1 gap-3 md:grid-cols-2">
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

          <label class="flex flex-col gap-1">
            <span class="center-row justify-start gap-1.5 text-sm text-[#d4d4d4]">
              <Tooltip :content="fieldTips.openAIEndpoint" />
              <span>接口端点</span>
            </span>
            <Select
              v-model="draft.openAIEndpoint"
              :options="openAIEndpointOptions"
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

        <div v-if="draft.type === 'anthropic'" class="rounded-[8px] border border-[#343434] bg-[#252525] p-3">
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
        />

        <div
          v-if="errorMessage"
          class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
        >
          {{ errorMessage }}
        </div>
      </div>
    </div>
  </div>
</template>
