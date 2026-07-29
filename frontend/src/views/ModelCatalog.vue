<script setup>
/**
 * 拉取模型独立页：
 * - 从 sessionStorage 读取连接参数（避免 apiKey 进 URL）
 * - 按「URL / 渠道」归类；渠道名只在本页填写一次
 * - 选模型 → 可选探测 → 批量添加
 */
import Button from "@/components/ui/Button.vue";
import { fetchModelCatalog } from "@/services/clientApi";
import { useModelProbe } from "@/composables/useModelProbe";
import { isBrowserPreview, runtimeWindow } from "@/services/runtimeAdapter";
import {
  classifyModelProtocol,
  createEmptyModelAdapter,
  inferProviderType,
  normalizeModelAdapter,
  PROTOCOL_MODE_AUTO,
  saveModelAdaptersBatch,
  toUserError,
} from "@/state/appState";
import {
  SUPPLIER_GROUP_MODE_CONNECTION,
  SUPPLIER_GROUP_MODE_NAME,
  normalizeSupplierGroupMode,
} from "@/utils/supplierGrouping";
import { MODEL_CATALOG_DRAFT_KEY } from "@/utils/modelCatalogDraft";
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const loading = ref(true);
const catalogLoading = ref(false);
const catalogSaving = ref(false);
const catalogError = ref("");
const models = ref([]);
const selected = ref(new Set());
const channelName = ref("");
const draft = ref(null);
const catalogProbe = useModelProbe();

const groupMode = computed(() =>
  normalizeSupplierGroupMode(draft.value?.groupMode || SUPPLIER_GROUP_MODE_CONNECTION),
);
const isNameMode = computed(() => groupMode.value === SUPPLIER_GROUP_MODE_NAME);
const selectedCount = computed(() =>
  models.value.reduce((n, m) => n + (selected.value.has(m.id) ? 1 : 0), 0),
);
const allSelected = computed(
  () => models.value.length > 0 && models.value.every((m) => selected.value.has(m.id)),
);
const sourceLabel = computed(() => {
  const base = String(draft.value?.baseURL || "").trim();
  if (!base) return "";
  try {
    return new URL(base).host || base;
  } catch {
    return base;
  }
});

function pricingSummary(pricing) {
  if (!pricing?.known) return "价格未知";
  const parts = [];
  if (pricing.input != null) parts.push(`入 ${pricing.input}`);
  if (pricing.output != null) parts.push(`出 ${pricing.output}`);
  return `${pricing.currency || "USD"} / 1M · ${parts.join(" / ") || "已配置"}`;
}

function selectionKey(modelID) {
  return String(modelID || "");
}

function isSelected(modelID) {
  return selected.value.has(selectionKey(modelID));
}

function toggleModel(modelID) {
  const key = selectionKey(modelID);
  const next = new Set(selected.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  selected.value = next;
}

function toggleAll() {
  if (allSelected.value) {
    selected.value = new Set();
    return;
  }
  selected.value = new Set(models.value.map((m) => selectionKey(m.id)));
}

function buildProbeAdapter(model) {
  const d = draft.value || {};
  // 按模型名推断 provider type，避免 claude/gemini 被套用 openai 协议探测导致结果失真。
  const inferredType = inferProviderType(model.id, d.type || "openai");
  return normalizeModelAdapter({
    ...createEmptyModelAdapter(),
    type: inferredType,
    baseURL: d.baseURL || "",
    apiKey: d.apiKey || "",
    customHeadersEnabled: Boolean(d.customHeadersEnabled),
    customHeadersJSON: d.customHeadersJSON || "",
    displayName: model.id,
    modelID: model.id,
    tooltipData: `探测 ${model.id}`,
    protocolMode: PROTOCOL_MODE_AUTO,
    protocolGroup: classifyModelProtocol(inferredType, model.id, d.baseURL || "", "", ""),
    openAIRequestGroup:
      inferredType === "openai"
        ? classifyModelProtocol(inferredType, model.id, d.baseURL || "", "", "")
        : "",
  });
}

async function handleProbe() {
  if (!models.value.length) return;
  await catalogProbe.probeAll(models.value, buildProbeAdapter, { concurrency: 3 });
  const next = new Set();
  for (const model of models.value) {
    if (catalogProbe.statusOf(selectionKey(model.id)) === "ok") {
      next.add(selectionKey(model.id));
    }
  }
  selected.value = next;
}

async function fetchModels() {
  catalogError.value = "";
  const d = draft.value;
  if (!d) {
    catalogError.value = "缺少连接参数，请从「快速添加」重新进入";
    return;
  }
  const baseURL = String(d.baseURL || "").trim();
  const apiKey = String(d.apiKey || "").trim();
  if (!baseURL || !apiKey) {
    catalogError.value = "请先填写接口地址和访问密钥";
    return;
  }

  catalogLoading.value = true;
  catalogProbe.reset();
  try {
    const result = await fetchModelCatalog({
      type: d.type || "openai",
      baseURL,
      apiKey,
      customHeadersEnabled: Boolean(d.customHeadersEnabled),
      customHeadersJSON: d.customHeadersJSON || "",
    });
    const fetched = Array.isArray(result?.models) ? result.models : [];
    models.value = fetched;
    selected.value = new Set(fetched.map((m) => selectionKey(m.id)));
    if (!fetched.length) {
      catalogError.value = "服务未返回可用模型";
    }
  } catch (error) {
    models.value = [];
    selected.value = new Set();
    catalogError.value = toUserError(error);
  } finally {
    catalogLoading.value = false;
  }
}

function resolveGroupName() {
  const d = draft.value || {};
  const baseURL = String(d.baseURL || "").trim();
  if (isNameMode.value) {
    return String(channelName.value || "").trim();
  }
  // 按 URL：写入 baseURL，便于「连接分组」与列表展示对齐
  return baseURL;
}

async function handleBatchAdd() {
  catalogError.value = "";
  const d = draft.value;
  if (!d) return;

  const picked = models.value.filter((m) => isSelected(m.id));
  if (!picked.length) {
    catalogError.value = "请至少选择一个模型";
    return;
  }

  const groupName = resolveGroupName();
  if (isNameMode.value && !groupName) {
    catalogError.value = "按渠道归类时请填写渠道名称";
    return;
  }

  catalogSaving.value = true;
  try {
    const baseURL = String(d.baseURL || "").trim();
    const adapters = picked.map((model) => {
      // 按模型名推断 provider type，让 claude→anthropic、gemini→gemini 走原生协议，
        // 避免错误套用渠道级 openai 协议导致缓存失效。
      const inferredType = inferProviderType(model.id, d.type || "openai");
      return normalizeModelAdapter({
        ...createEmptyModelAdapter(),
        type: inferredType,
        baseURL,
        apiKey: d.apiKey || "",
        customHeadersEnabled: Boolean(d.customHeadersEnabled),
        customHeadersJSON: d.customHeadersJSON || "",
        displayName: model.id,
        modelID: model.id,
        protocolMode: PROTOCOL_MODE_AUTO,
        protocolGroup: classifyModelProtocol(inferredType, model.id, baseURL, "", ""),
        openAIRequestGroup:
          inferredType === "openai"
            ? classifyModelProtocol(inferredType, model.id, baseURL, "", "")
            : "",
        groupName,
        tooltipData: String(d.tooltipData || "").trim() || `来自 ${baseURL}`,
        contextWindowTokens: model.contextWindowTokens || 0,
        pricing: model.pricing || null,
      });
    });
    const result = await saveModelAdaptersBatch(adapters);
    if (!result.ok) {
      catalogError.value = result.error || "批量添加失败";
      return;
    }
    try {
      sessionStorage.removeItem(MODEL_CATALOG_DRAFT_KEY);
    } catch {
      /* ignore */
    }
    await leavePage();
  } catch (error) {
    catalogError.value = toUserError(error);
  } finally {
    catalogSaving.value = false;
  }
}

async function leavePage() {
  // 批量添加成功：回模型配置列表（浏览器）或关闭独立编辑窗（桌面）
  if (isBrowserPreview) {
    await router.push("/model-config");
    return;
  }
  await runtimeWindow.Close();
}

async function handleBack() {
  // 返回编辑页，保留同一 webview 里的会话（不关窗）
  if (window.history.length > 1) {
    router.back();
    return;
  }
  await router.push({ path: "/model-editor", query: { index: "-1" } });
}

onMounted(async () => {
  try {
    const raw = sessionStorage.getItem(MODEL_CATALOG_DRAFT_KEY);
    const parsed = raw ? JSON.parse(raw) : null;
    if (!parsed || typeof parsed !== "object") {
      catalogError.value = "缺少连接参数，请从「快速添加」重新进入";
      draft.value = null;
      return;
    }
    draft.value = {
      type: parsed.type || "openai",
      baseURL: String(parsed.baseURL || "").trim(),
      apiKey: String(parsed.apiKey || "").trim(),
      customHeadersEnabled: Boolean(parsed.customHeadersEnabled),
      customHeadersJSON: parsed.customHeadersJSON || "",
      tooltipData: String(parsed.tooltipData || "").trim(),
      groupMode: normalizeSupplierGroupMode(parsed.groupMode),
    };
    // 渠道模式：可预填已有渠道名；URL 模式不需要
    channelName.value = String(parsed.channelName || "").trim();
    await fetchModels();
  } catch (error) {
    catalogError.value = toUserError(error);
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <div class="flex h-full flex-col text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-3 border-b border-[#343434] px-4 py-3">
      <div class="min-w-0">
        <h2 class="text-base font-medium text-white">拉取模型</h2>
        <p class="truncate text-xs text-[#8f8f8f]">
          {{ sourceLabel || "从供应商 /models 拉取并批量添加" }}
          · {{ isNameMode ? "按渠道归类" : "按 URL 归类" }}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <Button variant="default" @click="handleBack">返回</Button>
        <Button
          variant="default"
          :disabled="catalogLoading || !draft"
          @click="fetchModels"
        >
          {{ catalogLoading ? "拉取中..." : "重新拉取" }}
        </Button>
      </div>
    </div>

    <div v-if="loading" class="flex flex-1 items-center justify-center text-sm text-[#a3a3a3]">
      加载中...
    </div>

    <div v-else class="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 py-4">
      <div
        v-if="isNameMode"
        class="flex flex-col gap-1 rounded-[8px] border border-[#343434] bg-[#252525] p-3"
      >
        <span class="text-sm text-[#d4d4d4]">渠道名称</span>
        <input
          v-model="channelName"
          type="text"
          placeholder="例如：官方 / 中转A / 公司内网"
          class="h-9 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
        />
        <span class="text-xs text-[#8f8f8f]">按渠道归类时，本页所选模型会写入同一渠道名。</span>
      </div>

      <div
        v-else
        class="rounded-[8px] border border-[#343434] bg-[#252525] px-3 py-2 text-xs text-[#a3a3a3]"
      >
        按 URL 归类：添加后将按接口地址
        <span class="text-[#d4d4d4]">{{ draft?.baseURL || "-" }}</span>
        汇总到同一连接。
      </div>

      <div
        v-if="catalogLoading && !models.length"
        class="flex min-h-[160px] items-center justify-center rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] text-sm text-[#a3a3a3]"
      >
        正在拉取模型列表...
      </div>

      <div
        v-else-if="models.length"
        class="flex min-h-0 flex-1 flex-col gap-2 rounded-[8px] border border-[#343434] bg-[#252525] p-3"
      >
        <div class="flex items-center justify-between gap-2 text-xs text-[#a3a3a3]">
          <span>已选 {{ selectedCount }}/{{ models.length }}</span>
          <button type="button" class="text-[#6ee7a5]" @click="toggleAll">
            {{ allSelected ? "取消全选" : "全选" }}
          </button>
        </div>
        <div class="max-h-[min(420px,50vh)] min-h-0 overflow-y-auto">
          <label
            v-for="model in models"
            :key="model.id"
            class="flex items-center gap-2 border-b border-[#2f2f2f] py-1.5 text-sm text-[#d4d4d4] last:border-b-0"
          >
            <input
              type="checkbox"
              class="size-4 accent-[#10AD5D]"
              :checked="isSelected(model.id)"
              @change="toggleModel(model.id)"
            />
            <span class="min-w-0 flex-1 truncate">{{ model.id }}</span>
            <span class="hidden shrink-0 text-[11px] text-[#8f8f8f] sm:inline">{{ pricingSummary(model.pricing) }}</span>
            <span
              v-if="catalogProbe.statusOf(selectionKey(model.id)) === 'checking'"
              class="shrink-0 rounded-full border border-[#164e63] bg-[#0b2530] px-1.5 py-0.5 text-[10px] text-[#67e8f9]"
            >检测中</span>
            <span
              v-else-if="catalogProbe.statusOf(selectionKey(model.id)) === 'ok'"
              class="shrink-0 rounded-full border border-[#14532d] bg-[#102418] px-1.5 py-0.5 text-[10px] text-[#86efac]"
            >✓ 可用</span>
            <span
              v-else-if="catalogProbe.statusOf(selectionKey(model.id)) === 'fail'"
              :title="catalogProbe.messageOf(selectionKey(model.id))"
              class="shrink-0 rounded-full border border-[#4b1d1d] bg-[#2a1313] px-1.5 py-0.5 text-[10px] text-[#fca5a5]"
            >✗ 不可用</span>
          </label>
        </div>
        <div class="flex gap-2 pt-1">
          <Button
            class="flex-1"
            variant="default"
            :disabled="catalogProbe.probing.value || catalogSaving || !models.length"
            @click="handleProbe"
          >
            {{ catalogProbe.probing.value ? "检测中..." : "检测可用性" }}
          </Button>
          <Button
            class="flex-1"
            variant="primary"
            :disabled="catalogSaving || selectedCount === 0"
            @click="handleBatchAdd"
          >
            {{ catalogSaving ? "添加中..." : `添加已选 (${selectedCount})` }}
          </Button>
        </div>
      </div>

      <div
        v-else-if="!catalogLoading"
        class="flex min-h-[160px] items-center justify-center rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] text-sm text-[#a3a3a3]"
      >
        暂无模型列表，可点击「重新拉取」。
      </div>

      <div
        v-if="catalogError"
        class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
      >
        {{ catalogError }}
      </div>
    </div>
  </div>
</template>